package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestGrokQuotaHeadroomFactorUsesFreshBillingAndRateLimitWindows(t *testing.T) {
	now := time.Now().UTC()
	requestLimit, requestRemaining := int64(100), int64(60)
	tokenLimit, tokenRemaining := int64(1000), int64(300)
	account := &Account{
		Platform: PlatformGrok,
		Extra: map[string]any{
			grokBillingSnapshotExtraKey: &xai.BillingSnapshot{
				CreditUsagePercent: 20, CreditRemainingPercent: 80, UpdatedAt: now.Format(time.RFC3339),
			},
			grokQuotaSnapshotExtraKey: &xai.QuotaSnapshot{
				Requests:  &xai.QuotaWindow{Limit: &requestLimit, Remaining: &requestRemaining},
				Tokens:    &xai.QuotaWindow{Limit: &tokenLimit, Remaining: &tokenRemaining},
				UpdatedAt: now.Format(time.RFC3339),
			},
		},
	}

	require.InDelta(t, 0.3, openAIQuotaHeadroomFactor(account, now), 0.001)
}

func TestGrokQuotaHeadroomFactorTreatsStaleOrMissingSnapshotAsNeutral(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{Platform: PlatformGrok, Extra: map[string]any{
		grokBillingSnapshotExtraKey: &xai.BillingSnapshot{
			CreditUsagePercent: 99, CreditRemainingPercent: 1,
			UpdatedAt: now.Add(-9 * time.Hour).Format(time.RFC3339),
		},
	}}
	require.Equal(t, openAIQuotaHeadroomNeutralFactor, openAIQuotaHeadroomFactor(account, now))
	require.Equal(t, openAIQuotaHeadroomNeutralFactor, openAIQuotaHeadroomFactor(&Account{Platform: PlatformGrok}, now))
}
