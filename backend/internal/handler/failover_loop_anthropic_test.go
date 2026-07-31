package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type anthropicSoft429CommitterMock struct {
	mockTempUnscheduler
	commitCalls int
}

func (m *anthropicSoft429CommitterMock) CommitAnthropicSoftRateLimit(_ context.Context, _ int64, err *service.UpstreamFailoverError, _ ...*service.Account) {
	m.commitCalls++
	err.AnthropicSoftRateLimitCommitted = true
}

func newAnthropicSoft429FailoverError() *service.UpstreamFailoverError {
	return &service.UpstreamFailoverError{
		StatusCode:             http.StatusTooManyRequests,
		ResponseBody:           []byte(`{"error":{"type":"rate_limit_error"}}`),
		ResponseHeaders:        http.Header{},
		RetryableOnSameAccount: true,
		AnthropicSoftRateLimit: true,
	}
}

func TestFailoverState_AnthropicSoft429RetriesOnceThenSwitches(t *testing.T) {
	originalDelay := anthropicSoftRateLimitRetryDelay
	anthropicSoftRateLimitRetryDelay = time.Millisecond
	t.Cleanup(func() { anthropicSoftRateLimitRetryDelay = originalDelay })

	mock := &anthropicSoft429CommitterMock{}
	state := NewFailoverState(2, false)
	err := newAnthropicSoft429FailoverError()

	start := time.Now()
	action := state.HandleFailoverError(context.Background(), mock, 101, service.PlatformAnthropic, err)
	require.Equal(t, FailoverContinue, action)
	require.GreaterOrEqual(t, time.Since(start), time.Millisecond)
	require.Equal(t, 1, state.AnthropicSoft429Retries[101])
	require.Equal(t, int64(101), state.ForceAccountID)
	require.Empty(t, state.FailedAccountIDs)
	require.Zero(t, mock.commitCalls)

	// The second 429 commits the adaptive cooldown and excludes the account.
	action = state.HandleFailoverError(context.Background(), mock, 101, service.PlatformAnthropic, err)
	require.Equal(t, FailoverContinue, action)
	require.Equal(t, 1, state.SwitchCount)
	require.Contains(t, state.FailedAccountIDs, int64(101))
	require.Zero(t, state.ForceAccountID)
	require.Equal(t, 1, mock.commitCalls)
	require.True(t, err.AnthropicSoftRateLimitCommitted)
	require.False(t, err.RetryableOnSameAccount)
}

func TestFailoverState_AnthropicSoft429HonorsCancellation(t *testing.T) {
	originalDelay := anthropicSoftRateLimitRetryDelay
	anthropicSoftRateLimitRetryDelay = time.Second
	t.Cleanup(func() { anthropicSoftRateLimitRetryDelay = originalDelay })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := NewFailoverState(1, false)
	action := state.HandleFailoverError(ctx, &anthropicSoft429CommitterMock{}, 101, service.PlatformAnthropic, newAnthropicSoft429FailoverError())
	require.Equal(t, FailoverCanceled, action)
}

func TestFailoverState_AnthropicSoft429UnavailableForcedRetrySwitchesAccount(t *testing.T) {
	state := NewFailoverState(2, false)
	state.ForceAccountID = 101
	state.AnthropicSoft429Retries[101] = anthropicSoftRateLimitRetryCount
	state.LastFailoverErr = newAnthropicSoft429FailoverError()

	action := state.HandleSelectionExhausted(context.Background(), errors.New("forced account unavailable"))

	require.Equal(t, FailoverContinue, action)
	require.Zero(t, state.ForceAccountID)
	require.Contains(t, state.FailedAccountIDs, int64(101))
	require.Equal(t, 1, state.SwitchCount)
}

func TestFailoverState_AnthropicSoft429StopsAfterTwoAccounts(t *testing.T) {
	originalDelay := anthropicSoftRateLimitRetryDelay
	anthropicSoftRateLimitRetryDelay = time.Millisecond
	t.Cleanup(func() { anthropicSoftRateLimitRetryDelay = originalDelay })

	mock := &anthropicSoft429CommitterMock{}
	state := NewFailoverState(10, false)

	first := newAnthropicSoft429FailoverError()
	require.Equal(t, FailoverContinue, state.HandleFailoverError(context.Background(), mock, 101, service.PlatformAnthropic, first))
	require.Equal(t, FailoverContinue, state.HandleFailoverError(context.Background(), mock, 101, service.PlatformAnthropic, first))
	require.Equal(t, 1, state.SwitchCount)

	second := newAnthropicSoft429FailoverError()
	require.Equal(t, FailoverContinue, state.HandleFailoverError(context.Background(), mock, 202, service.PlatformAnthropic, second))
	require.Equal(t, FailoverExhausted, state.HandleFailoverError(context.Background(), mock, 202, service.PlatformAnthropic, second))

	require.Equal(t, 1, state.SwitchCount, "second rejected credential must not rotate to a third account")
	require.Len(t, state.AnthropicSoft429Accounts, anthropicSoftRateLimitMaxAccounts)
	require.Contains(t, state.FailedAccountIDs, int64(101))
	require.Contains(t, state.FailedAccountIDs, int64(202))
	require.Equal(t, 2, mock.commitCalls)
}
