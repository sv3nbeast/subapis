//go:build unit

package xai

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseBillingCredits(t *testing.T) {
	t.Parallel()

	body := []byte(`{"config":{"creditUsagePercent":49.0,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-07-09T18:40:47.778876+00:00","end":"2026-07-16T18:40:47.778876+00:00"},"onDemandCap":{"val":2500},"onDemandUsed":{"val":"125.5"},"isUnifiedBillingUser":true,"prepaidBalance":{"val":30},"topUpMethod":"TOP_UP_METHOD_SAVED_PAYMENT_METHOD","billingPeriodStart":"2026-07-09T18:40:47.778876+00:00","billingPeriodEnd":"2026-07-16T18:40:47.778876+00:00"}}`)

	snapshot, err := ParseBillingCredits(body, http.StatusOK)
	require.NoError(t, err)
	require.Equal(t, 49.0, snapshot.CreditUsagePercent)
	require.Equal(t, 51.0, snapshot.CreditRemainingPercent)
	require.Equal(t, "USAGE_PERIOD_TYPE_WEEKLY", snapshot.CurrentPeriodType)
	require.Equal(t, "2026-07-16T18:40:47.778876Z", snapshot.CurrentPeriodEnd)
	require.Equal(t, 2500.0, snapshot.OnDemandCap)
	require.Equal(t, 125.5, snapshot.OnDemandUsed)
	require.Equal(t, 2374.5, snapshot.OnDemandRemaining)
	require.Equal(t, 30.0, snapshot.PrepaidBalance)
	require.True(t, snapshot.UnifiedBillingUser)
	require.Equal(t, http.StatusOK, snapshot.StatusCode)
	require.NotEmpty(t, snapshot.UpdatedAt)
}

func TestParseBillingCreditsTreatsOmittedProtoZeroValuesAsZero(t *testing.T) {
	t.Parallel()

	body := []byte(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-07-09T18:40:47Z","end":"2026-07-16T18:40:47Z"},"isUnifiedBillingUser":true}}`)

	snapshot, err := ParseBillingCredits(body, http.StatusOK)
	require.NoError(t, err)
	require.Zero(t, snapshot.CreditUsagePercent)
	require.Equal(t, 100.0, snapshot.CreditRemainingPercent)
	require.Zero(t, snapshot.OnDemandCap)
	require.Zero(t, snapshot.OnDemandUsed)
	require.Zero(t, snapshot.PrepaidBalance)
}

func TestParseBillingCreditsRejectsInvalidPeriod(t *testing.T) {
	t.Parallel()

	_, err := ParseBillingCredits([]byte(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-07-16T18:40:47Z","end":"2026-07-09T18:40:47Z"}}}`), http.StatusOK)
	require.Error(t, err)
}

func TestParseSettingsSubscriptionTier(t *testing.T) {
	t.Parallel()
	require.Equal(t, "SuperGrok", ParseSettingsSubscriptionTier([]byte(`{"subscription_tier_display":" SuperGrok "}`)))
}
