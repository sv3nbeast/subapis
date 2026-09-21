//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestGrokMediaGenerationEligibility(t *testing.T) {
	weeklyUsagePercent := 12.5
	forbiddenBilling := &xai.BillingSummary{
		StatusCode:        http.StatusForbidden,
		WeeklyStatusCode:  http.StatusForbidden,
		MonthlyStatusCode: http.StatusForbidden,
	}
	weeklyAllowance := &xai.BillingSummary{
		PeriodType:       "weekly",
		UsagePercent:     &weeklyUsagePercent,
		StatusCode:       http.StatusOK,
		WeeklyStatusCode: http.StatusOK,
	}
	weeklyForbidden := &xai.BillingSummary{
		StatusCode:        http.StatusOK,
		WeeklyStatusCode:  http.StatusForbidden,
		MonthlyStatusCode: http.StatusOK,
	}
	monthlyForbidden := &xai.BillingSummary{
		StatusCode:        http.StatusOK,
		WeeklyStatusCode:  http.StatusOK,
		MonthlyStatusCode: http.StatusForbidden,
	}

	tests := []struct {
		name       string
		account    *Account
		want       bool
		wantReason string
	}{
		{name: "nil account", account: nil, want: false, wantReason: "not_grok"},
		{name: "non grok account", account: &Account{Platform: PlatformOpenAI}, want: false, wantReason: "not_grok"},
		{name: "non oauth grok account stays eligible", account: &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey}, want: true, wantReason: "non_oauth"},
		{name: "unobserved oauth fails closed", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}, want: false, wantReason: "billing_unobserved"},
		{name: "weekly paid usage is eligible without inferring from period type", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{grokBillingExtraKey: weeklyAllowance}}, want: true, wantReason: "eligible"},
		{name: "billing forbidden is rejected", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{grokBillingExtraKey: forbiddenBilling}}, want: false, wantReason: "billing_forbidden"},
		{name: "weekly billing forbidden is rejected after partial success", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{grokBillingExtraKey: weeklyForbidden}}, want: false, wantReason: "billing_forbidden"},
		{name: "monthly billing forbidden is rejected after partial success", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{grokBillingExtraKey: monthlyForbidden}}, want: false, wantReason: "billing_forbidden"},
		{name: "malformed billing observation fails closed", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{grokBillingExtraKey: make(chan int)}}, want: false, wantReason: "billing_unobserved"},
		{name: "malformed override falls back to observations", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{GrokMediaEligibleExtraKey: "false", grokBillingExtraKey: weeklyAllowance}}, want: true, wantReason: "eligible"},
		{name: "explicit disable wins", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{GrokMediaEligibleExtraKey: false}}, want: false, wantReason: "override_disabled"},
		{name: "explicit enable wins over forbidden probe", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: map[string]any{GrokMediaEligibleExtraKey: true, grokBillingExtraKey: forbiddenBilling}}, want: true, wantReason: "override_enabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := tt.account.GrokMediaGenerationEligibility()
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.wantReason, reason)
		})
	}
}

// 本地 Grok 媒体资格对 billing_inconclusive 保持 fail-closed（de94a8d3d：付费凭证
// 才放行，不确定仍拒），官方 a77423066 改为放行未采纳；对应官方用例
// “inconclusive successful billing remains eligible” 与本用例官方版断言不适用。
func TestGrokMediaCapabilityKeepsOnlyUnobservedOAuthAsProbeCandidate(t *testing.T) {
	unobserved := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}
	eligible, reason := unobserved.GrokMediaGenerationEligibility()
	require.False(t, eligible)
	require.Equal(t, "billing_unobserved", reason)
	require.True(t, unobserved.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGrokMediaGeneration))

	inconclusive := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{grokBillingExtraKey: &xai.BillingSummary{
			StatusCode: http.StatusOK,
			Partial:    true,
		}},
	}
	require.False(t, inconclusive.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGrokMediaGeneration))
}

func TestGrokMediaCapabilityFiltersOnlyGeneration(t *testing.T) {
	account := &Account{
		ID:          1,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra:       map[string]any{GrokMediaEligibleExtraKey: false},
	}

	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGrokMediaGeneration))
	require.False(t, isOpenAICompatibleAccountEligibleForRequest(
		context.Background(), account, PlatformGrok, "grok-imagine-video", false,
		OpenAIEndpointCapabilityGrokMediaGeneration,
	))
}

func TestNormalizeGrokMediaEligibilityExtra(t *testing.T) {
	t.Run("boolean override is accepted", func(t *testing.T) {
		extra, err := normalizeGrokMediaEligibilityExtra(PlatformGrok, map[string]any{GrokMediaEligibleExtraKey: false})

		require.NoError(t, err)
		require.Equal(t, false, extra[GrokMediaEligibleExtraKey])
	})

	t.Run("null clears override", func(t *testing.T) {
		extra, err := normalizeGrokMediaEligibilityExtra(PlatformGrok, map[string]any{GrokMediaEligibleExtraKey: nil})

		require.NoError(t, err)
		require.NotContains(t, extra, GrokMediaEligibleExtraKey)
	})

	t.Run("malformed override is rejected", func(t *testing.T) {
		_, err := normalizeGrokMediaEligibilityExtra(PlatformGrok, map[string]any{GrokMediaEligibleExtraKey: "false"})

		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	})

	t.Run("other platforms ignore provider owned value", func(t *testing.T) {
		extra := map[string]any{GrokMediaEligibleExtraKey: "provider-owned"}
		normalized, err := normalizeGrokMediaEligibilityExtra(PlatformOpenAI, extra)

		require.NoError(t, err)
		require.Equal(t, extra, normalized)
	})
}

func TestNormalizeGrokMediaEligibilityUpdateExtra(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Extra: map[string]any{GrokMediaEligibleExtraKey: false}}

	t.Run("omitted override preserves current value", func(t *testing.T) {
		input := &UpdateAccountInput{Extra: map[string]any{"quota_used": float64(1)}}
		normalized, err := normalizeGrokMediaEligibilityUpdateExtra(account, input, map[string]any{"quota_used": float64(1)})

		require.NoError(t, err)
		require.Equal(t, false, normalized[GrokMediaEligibleExtraKey])
	})

	t.Run("null removes current override", func(t *testing.T) {
		input := &UpdateAccountInput{Extra: map[string]any{GrokMediaEligibleExtraKey: nil}}
		normalized, err := normalizeGrokMediaEligibilityUpdateExtra(account, input, map[string]any{GrokMediaEligibleExtraKey: nil})

		require.NoError(t, err)
		require.NotContains(t, normalized, GrokMediaEligibleExtraKey)
		require.Contains(t, input.Extra, GrokMediaEligibleExtraKey)
	})

	t.Run("provided boolean replaces current override", func(t *testing.T) {
		input := &UpdateAccountInput{Extra: map[string]any{GrokMediaEligibleExtraKey: true}}
		normalized, err := normalizeGrokMediaEligibilityUpdateExtra(account, input, map[string]any{GrokMediaEligibleExtraKey: true})

		require.NoError(t, err)
		require.Equal(t, true, normalized[GrokMediaEligibleExtraKey])
	})

	t.Run("malformed override is rejected on update", func(t *testing.T) {
		input := &UpdateAccountInput{Extra: map[string]any{GrokMediaEligibleExtraKey: "false"}}
		_, err := normalizeGrokMediaEligibilityUpdateExtra(account, input, nil)

		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	})

	t.Run("non grok update is unchanged", func(t *testing.T) {
		input := &UpdateAccountInput{Extra: map[string]any{GrokMediaEligibleExtraKey: "provider-owned"}}
		normalized := map[string]any{GrokMediaEligibleExtraKey: "provider-owned"}
		got, err := normalizeGrokMediaEligibilityUpdateExtra(&Account{Platform: PlatformOpenAI}, input, normalized)

		require.NoError(t, err)
		require.Equal(t, normalized, got)
	})
}

// grokJWTWithTier builds an unsigned token whose payload carries the numeric
// prod_auth subscription tier; only the payload is decoded by the reader.
func grokJWTWithTier(tier int) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"sub":"u","tier":%d}`, tier)))
	return "h." + payload + ".s"
}

// A paid plan proven by the live access token must not need allowance numbers:
// production account 2611 (SuperGrok Heavy) carried a tier but no quota fields
// and was rejected as billing_inconclusive, which blocked every image request.
func TestGrokMediaEligibilityTrustsProvenPaidPlanWithoutQuotaFields(t *testing.T) {
	paidNoQuota := &Account{
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": grokJWTWithTier(5)},
		Extra: map[string]any{grokBillingExtraKey: &xai.BillingSummary{
			StatusCode: http.StatusOK,
		}},
	}
	eligible, reason := paidNoQuota.GrokMediaGenerationEligibility()
	require.True(t, eligible, "a proven paid tier is the positive evidence this gate wants")
	require.Equal(t, "eligible", reason)
	require.True(t, paidNoQuota.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGrokMediaGeneration))

	free := &Account{
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": grokJWTWithTier(0)},
		Extra: map[string]any{grokBillingExtraKey: &xai.BillingSummary{
			StatusCode: http.StatusOK,
		}},
	}
	eligible, reason = free.GrokMediaGenerationEligibility()
	require.False(t, eligible, "free tiers cannot generate media")
	require.Equal(t, "billing_free_tier", reason)

	// No plan evidence at all still fails closed on the quota fields, keeping
	// inconclusive accounts out of the media pool.
	inconclusive := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{grokBillingExtraKey: &xai.BillingSummary{
			StatusCode: http.StatusOK,
			Partial:    true,
		}},
	}
	eligible, reason = inconclusive.GrokMediaGenerationEligibility()
	require.False(t, eligible)
	require.Equal(t, "billing_inconclusive", reason)
}

// Scheduling reads a projected account: the cache keeps the Grok billing and
// quota snapshots but strips credentials, so the access-token JWT is gone by
// then. A paid plan must still be provable from the snapshot alone, or the
// candidate filter rejects the account that direct calls happily accept.
func TestGrokMediaEligibilityTrustsSnapshotTierWithoutCredentials(t *testing.T) {
	tokenLimit := int64(53_000_000)
	projected := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		// Credentials deliberately absent: filterSchedulerCredentials drops
		// access_token, so nothing here may depend on it.
		Extra: map[string]any{
			grokBillingExtraKey: &xai.BillingSummary{
				StatusCode:       http.StatusOK,
				SubscriptionTier: "SuperGrok Heavy",
			},
			grokQuotaSnapshotExtraKey: &xai.QuotaSnapshot{
				StatusCode: http.StatusOK,
				Tokens:     &xai.QuotaWindow{Limit: &tokenLimit},
			},
		},
	}
	eligible, reason := projected.GrokMediaGenerationEligibility()
	require.True(t, eligible, "a paid tier in the billing snapshot is provable without credentials")
	require.Equal(t, "eligible", reason)
	require.True(t, projected.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityGrokMediaGeneration))

	free := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			grokBillingExtraKey: &xai.BillingSummary{
				StatusCode:       http.StatusOK,
				SubscriptionTier: "Free",
			},
		},
	}
	eligible, reason = free.GrokMediaGenerationEligibility()
	require.False(t, eligible, "the upstream does not generate images for free accounts")
	require.Equal(t, "billing_free_tier", reason)
}
