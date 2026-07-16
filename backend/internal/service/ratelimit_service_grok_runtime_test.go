package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type grok403RuntimeRepo struct {
	AccountRepository
	tempCalls  int
	errorCalls int
	errorMsg   string
}

func (r *grok403RuntimeRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

func (r *grok403RuntimeRepo) SetError(_ context.Context, _ int64, message string) error {
	r.errorCalls++
	r.errorMsg = message
	return nil
}

type grok403RuntimeCounter struct{ count int64 }

func (c *grok403RuntimeCounter) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	return c.count, nil
}

func (c *grok403RuntimeCounter) ResetOpenAI403Count(context.Context, int64) error { return nil }

func TestGrok403RuntimePolicyRequiresConsecutiveThreshold(t *testing.T) {
	account := &Account{ID: 91, Platform: PlatformGrok, Type: AccountTypeOAuth}
	firstRepo := &grok403RuntimeRepo{}
	first := NewRateLimitService(firstRepo, nil, &config.Config{}, nil, nil)
	first.SetOpenAI403CounterCache(&grok403RuntimeCounter{count: 1})
	require.True(t, first.HandleUpstreamError(context.Background(), account, http.StatusForbidden, http.Header{}, []byte(`{"error":"Access denied"}`)))
	require.Equal(t, 1, firstRepo.tempCalls)
	require.Zero(t, firstRepo.errorCalls)

	thresholdRepo := &grok403RuntimeRepo{}
	threshold := NewRateLimitService(thresholdRepo, nil, &config.Config{}, nil, nil)
	threshold.SetOpenAI403CounterCache(&grok403RuntimeCounter{count: 3})
	require.True(t, threshold.HandleUpstreamError(context.Background(), account, http.StatusForbidden, http.Header{}, []byte(`{"error":"Access denied"}`)))
	require.Zero(t, thresholdRepo.tempCalls)
	require.Equal(t, 1, thresholdRepo.errorCalls)
	require.Contains(t, thresholdRepo.errorMsg, "consecutive_403=3/3")
}

func TestGrok403RuntimePolicyDoesNotPenalizeRequestScopedForbidden(t *testing.T) {
	repo := &grok403RuntimeRepo{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(&grok403RuntimeCounter{count: 3})
	account := &Account{ID: 92, Platform: PlatformGrok, Type: AccountTypeOAuth}

	require.True(t, service.HandleUpstreamError(
		context.Background(), account, http.StatusForbidden, http.Header{},
		[]byte(`{"error":"upstream policy rejected request"}`),
	))
	require.Zero(t, repo.tempCalls)
	require.Zero(t, repo.errorCalls)
}
