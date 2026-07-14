//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type grokRateLimitAccountRepo struct {
	AccountRepository
	setErrorCalls        int
	lastErrorMessage     string
	rateLimitedCalls     int
	lastRateLimitedID    int64
	lastRateLimitResetAt time.Time
}

func (r *grokRateLimitAccountRepo) SetError(_ context.Context, _ int64, errorMessage string) error {
	r.setErrorCalls++
	r.lastErrorMessage = errorMessage
	return nil
}

func (r *grokRateLimitAccountRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.lastRateLimitedID = id
	r.lastRateLimitResetAt = resetAt
	return nil
}

func TestRateLimitServiceGrokSpendingLimit403UsesBillingPeriodRateLimit(t *testing.T) {
	resetAt := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	repo := &grokRateLimitAccountRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       501,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Extra: map[string]any{
			grokBillingSnapshotExtraKey: &xai.BillingSnapshot{
				CreditUsagePercent:     100,
				CreditRemainingPercent: 0,
				CurrentPeriodEnd:       resetAt.Format(time.RFC3339Nano),
			},
		},
	}

	shouldFailover := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"code":"personal-team-blocked:spending-limit","error":"You have run out of credits or need a Grok subscription."}`),
	)

	require.True(t, shouldFailover)
	require.Zero(t, repo.setErrorCalls, "quota exhaustion must not permanently disable a healthy Grok account")
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, account.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, resetAt, repo.lastRateLimitResetAt, time.Second)
	require.NotNil(t, account.RateLimitResetAt)
}

func TestRateLimitServiceGrokPermissionDeniedCreditsMessageUsesBillingPeriodRateLimit(t *testing.T) {
	resetAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	repo := &grokRateLimitAccountRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       502,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			grokBillingSnapshotExtraKey: map[string]any{
				"credit_usage_percent":     100,
				"credit_remaining_percent": 0,
				"current_period_end":       resetAt.Format(time.RFC3339Nano),
			},
		},
	}

	shouldFailover := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"code":"permission-denied","error":"Your team has either used all available credits or reached its monthly spending limit."}`),
	)

	require.True(t, shouldFailover)
	require.Zero(t, repo.setErrorCalls)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, resetAt, repo.lastRateLimitResetAt, time.Second)
}

func TestRateLimitServiceGrokGeneric403StillMarksRealEntitlementError(t *testing.T) {
	repo := &grokRateLimitAccountRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 503, Platform: PlatformGrok, Type: AccountTypeOAuth}

	shouldFailover := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"code":"permission-denied","error":"This account is not entitled to use Grok."}`),
	)

	require.True(t, shouldFailover)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Zero(t, repo.rateLimitedCalls)
}

func TestOpenAIGatewayGrokSpendingLimit402RateLimitsFreeAccount(t *testing.T) {
	resetAt := time.Now().Add(36 * time.Hour).UTC().Truncate(time.Second)
	repo := &grokRateLimitAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{
		ID:       505,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			grokBillingSnapshotExtraKey: &xai.BillingSnapshot{
				CreditUsagePercent:     100,
				CreditRemainingPercent: 0,
				CurrentPeriodEnd:       resetAt.Format(time.RFC3339Nano),
			},
		},
	}

	svc.handleGrokAccountUpstreamError(
		context.Background(),
		account,
		http.StatusPaymentRequired,
		http.Header{},
		[]byte(`{"code":"personal-team-blocked:spending-limit","error":"You have run out of credits or need a Grok subscription."}`),
	)

	require.Zero(t, repo.setErrorCalls, "402 quota exhaustion must not permanently disable a healthy free Grok account")
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, resetAt, repo.lastRateLimitResetAt, time.Second)
	require.NotNil(t, account.RateLimitResetAt)
	require.WithinDuration(t, resetAt, *account.RateLimitResetAt, time.Second)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account), "account must be blocked from re-scheduling")
}

func TestResolveGrokQuotaResetAtFallsBackToShortCooldown(t *testing.T) {
	now := time.Now().UTC()
	resetAt := resolveGrokQuotaResetAt(&Account{Platform: PlatformGrok}, now)
	require.WithinDuration(t, now.Add(grokQuotaExhaustedFallbackCooldown), resetAt, time.Second)
}

func TestOpenAIGatewayGrokSpendingLimit403SkipsEntitlementPause(t *testing.T) {
	resetAt := time.Now().Add(36 * time.Hour).UTC().Truncate(time.Second)
	repo := &grokRateLimitAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{
		ID:       504,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			grokBillingSnapshotExtraKey: &xai.BillingSnapshot{
				CreditUsagePercent:     100,
				CreditRemainingPercent: 0,
				CurrentPeriodEnd:       resetAt.Format(time.RFC3339Nano),
			},
		},
	}

	svc.handleGrokAccountUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"code":"personal-team-blocked:spending-limit","error":"You have run out of credits"}`),
	)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, resetAt, repo.lastRateLimitResetAt, time.Second)
	require.NotNil(t, account.RateLimitResetAt)
	require.WithinDuration(t, resetAt, *account.RateLimitResetAt, time.Second)
}
