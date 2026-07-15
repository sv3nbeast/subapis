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
