package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShouldStopOpenAIFailover_BoundsGrokQuotaExhaustionAfterOneAlternate(t *testing.T) {
	svc := &OpenAIGatewayService{}
	body := []byte(`{"code":"personal-team-blocked:spending-limit","error":"You have run out of credits"}`)
	oauth := &Account{ID: 46, Platform: PlatformGrok, Type: AccountTypeOAuth}
	apiKey := &Account{ID: 47, Platform: PlatformGrok, Type: AccountTypeAPIKey}

	// The first failed account may be followed by one mixed CLI/API-key
	// candidate; the second failed account ends this request's probe loop.
	require.False(t, svc.ShouldStopOpenAIFailover(oauth, http.StatusPaymentRequired, body, 1))
	require.True(t, svc.ShouldStopOpenAIFailover(oauth, http.StatusPaymentRequired, body, 2))
	require.False(t, svc.ShouldStopOpenAIFailover(apiKey, http.StatusForbidden, body, 1))
	require.True(t, svc.ShouldStopOpenAIFailover(apiKey, http.StatusForbidden, body, 2))
}

func TestShouldStopOpenAIFailoverWithContext_ContinuesPastTwoAccountsWithinBudget(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 48, Platform: PlatformGrok, Type: AccountTypeOAuth}
	body := []byte(`{"code":"subscription:free-usage-exhausted"}`)
	ctx := WithGrokQuotaFailoverBudget(context.Background())

	for failedSwitches := 1; failedSwitches <= 10; failedSwitches++ {
		require.False(t, svc.ShouldStopOpenAIFailoverWithContext(ctx, account, http.StatusTooManyRequests, body, failedSwitches))
	}
}

func TestShouldStopOpenAIFailoverWithContext_ZeroRPMContinuesWithinBudget(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 1718, Platform: PlatformGrok, Type: AccountTypeOAuth}
	body := []byte(`{"code":"resource-exhausted","error":"Requests per Minute (actual/limit): 0/0"}`)
	ctx := WithGrokQuotaFailoverBudget(context.Background())

	for failedSwitches := 1; failedSwitches <= 10; failedSwitches++ {
		require.False(t, svc.ShouldStopOpenAIFailoverWithContext(ctx, account, http.StatusTooManyRequests, body, failedSwitches))
	}
	require.False(t, shouldRetryOpenAIAccountOnSameAccount(account, http.StatusTooManyRequests, body, true))
	require.False(t, isGrokQuotaExhausted(account, body), "zero RPM is model-scoped, not global credit exhaustion")
}

func TestShouldStopOpenAIFailoverWithContext_StopsWhenQuotaDiscoveryBudgetExpires(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 49, Platform: PlatformGrok, Type: AccountTypeAPIKey}
	body := []byte(`{"error":{"type":"insufficient_quota","message":"No credits left"}}`)
	expiredCtx := context.WithValue(context.Background(), grokQuotaFailoverDeadlineContextKey{}, &grokQuotaFailoverBudgetState{deadline: time.Now().Add(-time.Second)})

	require.True(t, svc.ShouldStopOpenAIFailoverWithContext(expiredCtx, account, http.StatusForbidden, body, 1))
}

func TestShouldStopOpenAIFailover_DoesNotTreatGenericGrokForbiddenAsQuota(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 48, Platform: PlatformGrok, Type: AccountTypeOAuth}
	forbiddenBody := []byte(`{"code":"permission-denied","error":"This account is not entitled to use Grok."}`)
	unsupportedModelBody := []byte(`{"error":{"message":"Requested model is not supported by this API key/group"}}`)

	require.False(t, svc.ShouldStopOpenAIFailover(account, http.StatusForbidden, forbiddenBody, 2))
	require.False(t, svc.ShouldStopOpenAIFailover(account, http.StatusBadRequest, unsupportedModelBody, 2))
	require.True(t, svc.ShouldStopOpenAIFailover(account, http.StatusBadRequest, []byte(`{"code":"personal-team-blocked:spending-limit"}`), 2))
}

func TestShouldRetryOpenAIAccountOnSameAccount_SkipsGrokQuotaPoolRetry(t *testing.T) {
	account := &Account{
		ID:       49,
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode": true,
		},
	}
	body := []byte(`{"code":"personal-team-blocked:spending-limit","error":"You have run out of credits"}`)

	require.False(t, shouldRetryOpenAIAccountOnSameAccount(account, http.StatusForbidden, body, false))
	require.True(t, shouldRetryOpenAIAccountOnSameAccount(account, http.StatusForbidden, []byte(`{"error":"temporary permission denied"}`), false))
}

func TestShouldRetryOpenAIAccountOnSameAccount_SkipsModelCapabilityPoolRetry(t *testing.T) {
	account := &Account{
		ID:       50,
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode": true,
		},
	}
	body := []byte(`{"error":{"message":"Requested model is not supported by this API key/group"}}`)

	require.False(t, shouldRetryOpenAIAccountOnSameAccount(account, http.StatusBadRequest, body, false))
}

type grokModelCooldownRepo struct {
	AccountRepository
	calls  int
	model  string
	reset  time.Time
	reason string
}

func (r *grokModelCooldownRepo) SetModelRateLimit(_ context.Context, _ int64, model string, resetAt time.Time, reason ...string) error {
	r.calls++
	r.model = model
	r.reset = resetAt
	if len(reason) > 0 {
		r.reason = reason[0]
	}
	return nil
}

func TestHandleGrokAccountUpstreamErrorPersistsModelCapabilityCooldown(t *testing.T) {
	repo := &grokModelCooldownRepo{}
	svc := &OpenAIGatewayService{
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := &Account{ID: 51, Platform: PlatformGrok, Type: AccountTypeOAuth}
	body := []byte(`{"error":{"message":"Requested model is not supported by this API key/group"}}`)

	svc.handleGrokAccountUpstreamError(context.Background(), account, http.StatusBadRequest, http.Header{}, body, "grok-4.5")

	require.Equal(t, 1, repo.calls)
	require.Equal(t, "grok-4.5", repo.model)
	require.Equal(t, upstreamAccountModelUnsupportedReason, repo.reason)
	require.WithinDuration(t, time.Now().Add(upstreamModelNotFoundCooldown), repo.reset, 5*time.Second)
}

func TestResolveGrokQuotaResetAtUsesDayFallbackWithoutBillingWindow(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	account := &Account{ID: 52, Platform: PlatformGrok, Type: AccountTypeOAuth}

	resetAt := resolveGrokQuotaResetAt(account, now)

	require.Equal(t, now.Add(24*time.Hour), resetAt)
}
