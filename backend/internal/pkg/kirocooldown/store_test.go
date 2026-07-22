package kirocooldown

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestClearEarliestTransientCooldownEmptyKeysIsSafe(t *testing.T) {
	store := NewStore(redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"}))

	cleared, err := store.ClearEarliestTransientCooldown(context.Background(), nil)
	if err != nil {
		t.Fatalf("ClearEarliestTransientCooldown(nil) error = %v", err)
	}
	if cleared {
		t.Fatal("ClearEarliestTransientCooldown(nil) cleared = true, want false")
	}
}

func TestMark429WithRetryAfterPrefersBoundedUpstreamValue(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	cooldown, err := store.Mark429WithRetryAfter(context.Background(), "credential-a", 12*time.Second)
	require.NoError(t, err)
	require.Equal(t, 12*time.Second, cooldown)

	state, err := store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, CooldownReason429, state.Reason)
	require.Equal(t, 1, state.FailCount)
	require.Greater(t, state.Remaining, 10*time.Second)
	require.LessOrEqual(t, state.Remaining, 12*time.Second)

	cooldown, err = store.Mark429WithRetryAfter(context.Background(), "credential-b", time.Second)
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, cooldown)

	cooldown, err = store.Mark429WithRetryAfter(context.Background(), "credential-c", 10*time.Minute)
	require.NoError(t, err)
	require.Equal(t, MaxCooldown, cooldown)
}

func TestObserve429EscalatesOnlyAfterRepeatedLogicalFailures(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	for attempt := 1; attempt <= 2; attempt++ {
		result, err := store.Observe429(context.Background(), "credential-a", 5*time.Second, 3, 2*time.Minute)
		require.NoError(t, err)
		require.Equal(t, attempt, result.ObservationCount)
		require.Equal(t, 5*time.Second, result.Cooldown)
		require.False(t, result.Escalated)
		require.Zero(t, result.HardFailureCount)

		state, err := store.GetState(context.Background(), "credential-a")
		require.NoError(t, err)
		require.NotNil(t, state)
		require.Equal(t, CooldownReason429Observed, state.Reason)
		require.Zero(t, state.FailCount)
	}

	result, err := store.Observe429(context.Background(), "credential-a", 5*time.Second, 3, 2*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 3, result.ObservationCount)
	require.Equal(t, time.Minute, result.Cooldown)
	require.True(t, result.Escalated)
	require.Equal(t, 1, result.HardFailureCount)

	state, err := store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, CooldownReason429, state.Reason)
	require.Equal(t, 1, state.FailCount)
	require.NoError(t, client.HSet(
		context.Background(),
		RedisKey("credential-a"),
		"cooldown_until_ms",
		time.Now().Add(-time.Second).UnixMilli(),
	).Err())
	for range 3 {
		result, err = store.Observe429(context.Background(), "credential-a", 5*time.Second, 3, 2*time.Minute)
		require.NoError(t, err)
	}
	require.True(t, result.Escalated)
	require.Equal(t, 2, result.HardFailureCount)
	require.Equal(t, 2*time.Minute, result.Cooldown)

}

func TestMarkSuccessPreservingCooldownClearsSoft429Observation(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	first, err := store.Observe429(context.Background(), "credential-a", 5*time.Second, 3, 2*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, first.ObservationCount)
	require.NoError(t, store.MarkSuccessPreservingCooldown(context.Background(), "credential-a"))

	state, err := store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.Nil(t, state)

	afterSuccess, err := store.Observe429(context.Background(), "credential-a", 5*time.Second, 3, 2*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, afterSuccess.ObservationCount)
	require.False(t, afterSuccess.Escalated)
}

func TestMark429AdmissionSuccessPreservesNewerEvidenceAndHardCooldown(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	staleAdmission := time.Now().Add(-time.Second)
	_, err := store.Observe429(context.Background(), "credential-a", 5*time.Second, 3, 2*time.Minute)
	require.NoError(t, err)
	cleared, err := store.Mark429AdmissionSuccess(context.Background(), "credential-a", staleAdmission)
	require.NoError(t, err)
	require.False(t, cleared)
	state, err := store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.Equal(t, CooldownReason429Observed, state.Reason)

	cleared, err = store.Mark429AdmissionSuccess(context.Background(), "credential-a", time.Now().Add(time.Second))
	require.NoError(t, err)
	require.True(t, cleared)
	state, err = store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.Nil(t, state)

	for range 3 {
		_, err = store.Observe429(context.Background(), "credential-b", 5*time.Second, 3, 2*time.Minute)
		require.NoError(t, err)
	}
	cleared, err = store.Mark429AdmissionSuccess(context.Background(), "credential-b", time.Now().Add(time.Second))
	require.NoError(t, err)
	require.False(t, cleared)
	state, err = store.GetState(context.Background(), "credential-b")
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, CooldownReason429, state.Reason)
	require.Equal(t, 1, state.FailCount)

	require.NoError(t, client.HSet(
		context.Background(),
		RedisKey("credential-b"),
		"cooldown_until_ms",
		time.Now().Add(-time.Second).UnixMilli(),
	).Err())
	cleared, err = store.Mark429AdmissionSuccess(context.Background(), "credential-b", time.Now())
	require.NoError(t, err)
	require.True(t, cleared, "a success after hard cooldown expiry resets the hard backoff history")
	var result *RateLimitObservationResult
	for range 3 {
		result, err = store.Observe429(context.Background(), "credential-b", 5*time.Second, 3, 2*time.Minute)
		require.NoError(t, err)
	}
	require.True(t, result.Escalated)
	require.Equal(t, 1, result.HardFailureCount)
	require.Equal(t, time.Minute, result.Cooldown)
}

func TestObserve429DoesNotMultiplyActiveHardCooldown(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	for range 3 {
		_, err := store.Observe429(context.Background(), "credential-a", 5*time.Second, 3, 2*time.Minute)
		require.NoError(t, err)
	}
	concurrent, err := store.Observe429(context.Background(), "credential-a", 5*time.Second, 3, 2*time.Minute)
	require.NoError(t, err)
	require.False(t, concurrent.Escalated)
	require.Zero(t, concurrent.ObservationCount)
	require.Equal(t, 1, concurrent.HardFailureCount)
	require.Greater(t, concurrent.Cooldown, 55*time.Second)

	state, err := store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.Equal(t, 1, state.FailCount)
}

func Test429RetryLeaseAllowsOneOwnerAndProtectsRelease(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	owner, acquired, err := store.Acquire429RetryLease(context.Background(), "credential-a", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotEmpty(t, owner)

	_, acquired, err = store.Acquire429RetryLease(context.Background(), "credential-a", time.Minute)
	require.NoError(t, err)
	require.False(t, acquired)

	require.NoError(t, store.Release429RetryLease(context.Background(), "credential-a", "not-the-owner"))
	_, acquired, err = store.Acquire429RetryLease(context.Background(), "credential-a", time.Minute)
	require.NoError(t, err)
	require.False(t, acquired, "a stale owner must not delete another request's lease")

	require.NoError(t, store.Release429RetryLease(context.Background(), "credential-a", owner))
	_, acquired, err = store.Acquire429RetryLease(context.Background(), "credential-a", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
}

func TestGetStatesReturnsOnlyActiveCooldowns(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	_, err := store.Mark429WithRetryAfter(context.Background(), "credential-a", 30*time.Second)
	require.NoError(t, err)
	_, err = store.MarkUnresponsive(context.Background(), "credential-b", 30*time.Second, 2*time.Minute)
	require.NoError(t, err)

	states, err := store.GetStates(context.Background(), []string{"credential-a", "credential-b", "credential-missing", "credential-a"})
	require.NoError(t, err)
	require.Len(t, states, 2)
	require.Equal(t, CooldownReason429, states["credential-a"].Reason)
	require.Equal(t, CooldownReasonUnresponsive, states["credential-b"].Reason)
}

func TestObserveUnresponsiveRequiresRepeatedSameScopeBeforeCooldown(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	first, err := store.ObserveUnresponsive(
		context.Background(),
		"credential-a",
		"response_header|endpoint:q|proxy:1",
		30*time.Second,
		2*time.Minute,
		2,
		2*time.Minute,
	)
	require.NoError(t, err)
	require.Equal(t, 1, first.FailureCount)
	require.Zero(t, first.Cooldown)
	require.False(t, first.Escalated)
	state, err := store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.Nil(t, state, "the first ambiguous timeout must not open the credential circuit")

	second, err := store.ObserveUnresponsive(
		context.Background(),
		"credential-a",
		"response_header|endpoint:q|proxy:1",
		30*time.Second,
		2*time.Minute,
		2,
		2*time.Minute,
	)
	require.NoError(t, err)
	require.Equal(t, 2, second.FailureCount)
	require.Equal(t, 30*time.Second, second.Cooldown)
	require.True(t, second.Escalated)

	state, err = store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, CooldownReasonUnresponsive, state.Reason)
	require.Equal(t, 1, state.FailCount, "hard cooldown backoff counts circuit openings, not soft observations")
}

func TestObserveUnresponsiveScopesAndWindowDoNotCrossContaminate(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	observe := func(scope string) *UnresponsiveObservationResult {
		result, err := store.ObserveUnresponsive(
			context.Background(),
			"credential-a",
			scope,
			30*time.Second,
			2*time.Minute,
			2,
			2*time.Minute,
		)
		require.NoError(t, err)
		return result
	}

	require.Equal(t, 1, observe("endpoint-a").FailureCount)
	require.Equal(t, 1, observe("endpoint-b").FailureCount)
	require.Equal(t, 1, observe("endpoint-a").FailureCount, "switching scope resets the soft streak")
	mr.FastForward(2*time.Minute + time.Second)
	require.Equal(t, 1, observe("endpoint-a").FailureCount, "expired soft evidence must not open a later circuit")

	state, err := store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.Nil(t, state)
}

func TestMarkSuccessPreservingCooldownClearsSoftUnresponsiveEvidence(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	first, err := store.ObserveUnresponsive(context.Background(), "credential-a", "endpoint-a", 30*time.Second, 2*time.Minute, 2, 2*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, first.FailureCount)
	require.NoError(t, store.MarkSuccessPreservingCooldown(context.Background(), "credential-a"))

	afterSuccess, err := store.ObserveUnresponsive(context.Background(), "credential-a", "endpoint-a", 30*time.Second, 2*time.Minute, 2, 2*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, afterSuccess.FailureCount)
	require.False(t, afterSuccess.Escalated)
}

func TestMarkSuccessPreservingCooldownDoesNotClearConcurrent429(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	_, err := store.Mark429WithRetryAfter(context.Background(), "credential-a", 30*time.Second)
	require.NoError(t, err)
	require.NoError(t, store.MarkSuccessPreservingCooldown(context.Background(), "credential-a"))

	state, err := store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, CooldownReason429, state.Reason)
	require.Equal(t, 1, state.FailCount)
	require.Greater(t, state.Remaining, 25*time.Second)

	mr.FastForward(31 * time.Second)
	cooldown, err := store.Mark429WithRetryAfter(context.Background(), "credential-a", 0)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, cooldown, "concurrent success must not reset the active 429 streak")
}

func TestTransientCooldownNeverShortensAnActiveCooldown(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	cooldown, err := store.Mark429WithRetryAfter(context.Background(), "credential-a", 2*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, cooldown)

	cooldown, err = store.Mark429WithRetryAfter(context.Background(), "credential-a", 5*time.Second)
	require.NoError(t, err)
	require.Greater(t, cooldown, 119*time.Second)

	state, err := store.GetState(context.Background(), "credential-a")
	require.NoError(t, err)
	require.Equal(t, CooldownReason429, state.Reason)
	require.Greater(t, state.Remaining, 119*time.Second)
}

func TestTransientCooldownBackoffCountersAreIndependentByReason(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewStore(client)

	_, err := store.Mark429WithRetryAfter(context.Background(), "credential-a", 5*time.Second)
	require.NoError(t, err)
	mr.FastForward(6 * time.Second)

	cooldown, err := store.MarkUnresponsive(context.Background(), "credential-a", 30*time.Second, 2*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, cooldown)
	mr.FastForward(31 * time.Second)

	cooldown, err = store.Mark429WithRetryAfter(context.Background(), "credential-a", 0)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, cooldown, "the earlier 429 streak remains independent from unresponsive failures")
}

func TestClearEarliestTransientCooldownUnavailableStore(t *testing.T) {
	store := NewStore(nil)

	cleared, err := store.ClearEarliestTransientCooldown(context.Background(), []string{"token"})
	if err == nil {
		t.Fatal("ClearEarliestTransientCooldown unavailable store error = nil")
	}
	if cleared {
		t.Fatal("ClearEarliestTransientCooldown unavailable store cleared = true, want false")
	}
}
