package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type grokFreeQuotaAccountRepo struct {
	AccountRepository
	rateLimitedCalls     int
	lastRateLimitResetAt time.Time
	tempUnschedCalls     int
	lastTempUnschedUntil time.Time
}

func (r *grokFreeQuotaAccountRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.lastRateLimitResetAt = resetAt
	return nil
}

func (r *grokFreeQuotaAccountRepo) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, _ string) error {
	r.tempUnschedCalls++
	r.lastTempUnschedUntil = until
	return nil
}

func TestOpenAIGatewayGrokFreeUsageExhausted429RateLimitsForRollingWindow(t *testing.T) {
	repo := &grokFreeQuotaAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{
		ID:       1758,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			grokBillingSnapshotExtraKey: &xai.BillingSnapshot{
				CreditUsagePercent:     0,
				CreditRemainingPercent: 100,
			},
		},
	}
	body := []byte(`{"code":"subscription:free-usage-exhausted","error":"You've used all the included free usage for model grok-4.5-build-free for now. Usage resets over a rolling 24-hour window - tokens (actual/limit): 2166478/2000000."}`)
	before := time.Now()

	svc.handleGrokAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Zero(t, repo.tempUnschedCalls)
	require.WithinDuration(t, before.Add(grokFreeUsageExhaustedCooldown), repo.lastRateLimitResetAt, time.Second)
	require.NotNil(t, account.RateLimitResetAt)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestResolveGrokFreeUsageResetPreservesExistingFutureLimit(t *testing.T) {
	now := time.Now().UTC()
	existing := now.Add(3 * time.Hour)
	account := &Account{Platform: PlatformGrok, RateLimitResetAt: &existing}
	body := []byte(`{"code":"subscription:free-usage-exhausted"}`)

	resetAt := resolveGrokQuotaResetAtForResponse(account, body, now)

	require.WithinDuration(t, existing, resetAt, time.Second)
}

func TestOpenAIGatewayGrokGeneric429KeepsShortCooldown(t *testing.T) {
	repo := &grokFreeQuotaAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 61, Platform: PlatformGrok, Type: AccountTypeOAuth}
	before := time.Now()

	svc.handleGrokAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":"burst rate limit"}`))

	require.Zero(t, repo.rateLimitedCalls)
	require.Equal(t, 1, repo.tempUnschedCalls)
	require.WithinDuration(t, before.Add(2*time.Minute), repo.lastTempUnschedUntil, time.Second)
}
