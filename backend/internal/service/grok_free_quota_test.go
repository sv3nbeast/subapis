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
	modelRateLimitCalls  int
	lastModel            string
	lastModelResetAt     time.Time
	lastModelReason      string
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

func (r *grokFreeQuotaAccountRepo) SetModelRateLimit(_ context.Context, _ int64, model string, resetAt time.Time, reason ...string) error {
	r.modelRateLimitCalls++
	r.lastModel = model
	r.lastModelResetAt = resetAt
	if len(reason) > 0 {
		r.lastModelReason = reason[0]
	}
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

func TestResolveGrokFreeUsageResetDoesNotReuseShortExistingLimit(t *testing.T) {
	now := time.Now().UTC()
	existing := now.Add(3 * time.Hour)
	account := &Account{Platform: PlatformGrok, RateLimitResetAt: &existing}
	body := []byte(`{"code":"subscription:free-usage-exhausted"}`)

	resetAt := resolveGrokQuotaResetAtForResponse(account, body, now)

	require.WithinDuration(t, now.Add(grokFreeUsageExhaustedCooldown), resetAt, time.Second)
}

func TestResolveGrokFreeUsageResetPreservesLaterProviderLimit(t *testing.T) {
	now := time.Now().UTC()
	existing := now.Add(48 * time.Hour)
	account := &Account{Platform: PlatformGrok, RateLimitResetAt: &existing}
	body := []byte(`{"code":"subscription:free-usage-exhausted"}`)

	resetAt := resolveGrokQuotaResetAtForResponse(account, body, now)

	require.WithinDuration(t, existing, resetAt, time.Second)
}

func TestIsGrokQuotaExhaustedResponseRecognizesSpendingLimitWithoutAccount(t *testing.T) {
	require.True(t, IsGrokQuotaExhaustedResponse([]byte(`{"code":"personal-team-blocked:spending-limit"}`)))
	require.True(t, IsGrokQuotaExhaustedResponse([]byte(`{"error":{"message":"You have run out of credits"}}`)))
	require.True(t, IsGrokQuotaExhaustedResponse([]byte(`{"error":{"type":"insufficient_quota","message":"Your team has no credits left"}}`)))
	require.True(t, IsGrokQuotaExhaustedResponse([]byte(`{"detail":{"code":"quota_exceeded"},"message":"Quota exceeded"}`)))
	require.False(t, IsGrokQuotaExhaustedResponse([]byte(`{"error":{"message":"This account is not entitled to use Grok."}}`)))
	require.False(t, IsGrokQuotaExhaustedResponse([]byte(`{"error":{"message":"Requested model is not supported by this API key/group"}}`)))
}

func TestGrokQuotaSnapshotZeroImmediatelyRateLimitsAPIKeyAccount(t *testing.T) {
	repo := &grokFreeQuotaAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	limit, remaining := int64(100), int64(0)
	resetAt := time.Now().Add(45 * time.Minute).UTC()
	resetUnix := resetAt.Unix()
	snapshot := &xai.QuotaSnapshot{
		Requests: &xai.QuotaWindow{
			Limit:     &limit,
			Remaining: &remaining,
			ResetUnix: &resetUnix,
		},
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	account := &Account{ID: 63, Platform: PlatformGrok, Type: AccountTypeAPIKey}

	require.True(t, svc.markGrokQuotaExhaustedFromSnapshot(context.Background(), account, snapshot))
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, resetAt, repo.lastRateLimitResetAt, time.Second)
	require.NotNil(t, account.RateLimitResetAt)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestGrokQuotaSnapshotResetIgnoresHealthyLaterWindow(t *testing.T) {
	now := time.Now().UTC()
	limit, exhausted, available := int64(100), int64(0), int64(90)
	requestReset := now.Add(10 * time.Minute).Unix()
	tokenReset := now.Add(24 * time.Hour).Unix()
	snapshot := &xai.QuotaSnapshot{
		Requests:  &xai.QuotaWindow{Limit: &limit, Remaining: &exhausted, ResetUnix: &requestReset},
		Tokens:    &xai.QuotaWindow{Limit: &limit, Remaining: &available, ResetUnix: &tokenReset},
		UpdatedAt: now.Format(time.RFC3339),
	}

	isExhausted, resetAt := isGrokQuotaSnapshotExhausted(snapshot, now)
	require.True(t, isExhausted)
	require.WithinDuration(t, time.Unix(requestReset, 0), resetAt, time.Second)
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

func TestOpenAIGatewayGrokZeroRPM429UsesModelCooldown(t *testing.T) {
	repo := &grokFreeQuotaAccountRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 1718, Platform: PlatformGrok, Type: AccountTypeOAuth}
	body := []byte(`{"code":"resource-exhausted","error":"Too many requests for team test and model grok-4.5. Your team's rate limit is - Requests per Minute (actual/limit): 0/0."}`)
	before := time.Now()

	svc.handleGrokAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body, "grok-4.5")

	require.Equal(t, 1, repo.modelRateLimitCalls)
	require.Equal(t, "grok-4.5", repo.lastModel)
	require.Equal(t, grokZeroRPMModelRateLimitReason, repo.lastModelReason)
	require.WithinDuration(t, before.Add(grokZeroRPMModelCooldown), repo.lastModelResetAt, time.Second)
	require.Zero(t, repo.rateLimitedCalls)
	require.Zero(t, repo.tempUnschedCalls)
}

func TestGrokZeroRPMClassifierRequiresZeroLimit(t *testing.T) {
	require.True(t, isGrokZeroRPMRateLimitResponse([]byte(`{"code":"resource-exhausted","error":"Requests per Minute (actual/limit): 0/0"}`)))
	require.True(t, isGrokZeroRPMRateLimitResponse([]byte(`{"error":{"message":"Requests per Minute (actual/limit): 0 / 0"}}`)))
	require.False(t, isGrokZeroRPMRateLimitResponse([]byte(`{"error":"Requests per Minute (actual/limit): 10/10"}`)))
	require.False(t, isGrokZeroRPMRateLimitResponse([]byte(`{"error":"burst rate limit"}`)))
}

func TestHandleOpenAIAccountUpstreamError_Grok429UsesOnlyGrokCooldown(t *testing.T) {
	repo := &grokFreeQuotaAccountRepo{}
	svc := &OpenAIGatewayService{
		accountRepo:      repo,
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := &Account{ID: 62, Platform: PlatformGrok, Type: AccountTypeOAuth}
	body := []byte(`{"code":"subscription:free-usage-exhausted","error":"You've used all the included free usage for now. Usage resets over a rolling 24-hour window."}`)
	before := time.Now()

	shouldDisable := svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body)

	require.False(t, shouldDisable)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Zero(t, repo.tempUnschedCalls)
	require.WithinDuration(t, before.Add(grokFreeUsageExhaustedCooldown), repo.lastRateLimitResetAt, time.Second)
}
