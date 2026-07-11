//go:build unit

package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func grokInt64PtrForTest(v int64) *int64 { return &v }
func grokIntPtrForTest(v int) *int       { return &v }

func TestGrokQuotaFetcherBuildUsageInfoUnknownUntilFirstSnapshot(t *testing.T) {
	t.Parallel()

	usage := NewGrokQuotaFetcher().BuildUsageInfo(&Account{Platform: PlatformGrok, Type: AccountTypeOAuth})
	require.Equal(t, "passive", usage.Source)
	require.Equal(t, "billing_unknown", usage.ErrorCode)
	require.Equal(t, "unknown", usage.GrokBillingState)
	require.Contains(t, usage.Error, "billing usage has not been queried")
}

func TestGrokQuotaFetcherBuildUsageInfoFromSnapshot(t *testing.T) {
	t.Parallel()

	updatedAt := "2030-01-01T00:00:00Z"
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			grokBillingSnapshotExtraKey: &xai.BillingSnapshot{
				CreditUsagePercent:     49,
				CreditRemainingPercent: 51,
				CurrentPeriodType:      "USAGE_PERIOD_TYPE_WEEKLY",
				CurrentPeriodStart:     "2029-12-25T00:00:00Z",
				CurrentPeriodEnd:       updatedAt,
				OnDemandCap:            2500,
				OnDemandUsed:           125,
				OnDemandRemaining:      2375,
				PrepaidBalance:         30,
				UnifiedBillingUser:     true,
				SubscriptionTier:       "SuperGrok",
				UpdatedAt:              updatedAt,
			},
			grokQuotaSnapshotExtraKey: &xai.QuotaSnapshot{
				Requests: &xai.QuotaWindow{
					Limit:     grokInt64PtrForTest(100),
					Remaining: grokInt64PtrForTest(12),
					ResetAt:   updatedAt,
				},
				Tokens: &xai.QuotaWindow{
					Limit:     grokInt64PtrForTest(1000),
					Remaining: grokInt64PtrForTest(900),
				},
				RetryAfterSeconds: grokIntPtrForTest(30),
				SubscriptionTier:  "supergrok",
				EntitlementStatus: "active",
				StatusCode:        http.StatusTooManyRequests,
				LastProbeAt:       updatedAt,
				LastHeadersSeenAt: updatedAt,
				UpdatedAt:         updatedAt,
			},
		},
	}

	usage := NewGrokQuotaFetcher().BuildUsageInfo(account)
	require.Equal(t, "passive", usage.Source)
	require.Equal(t, "rate_limited", usage.ErrorCode)
	require.Equal(t, "observed", usage.GrokQuotaSnapshotState)
	require.Equal(t, "SuperGrok", usage.SubscriptionTier)
	require.Equal(t, "observed", usage.GrokBillingState)
	require.NotNil(t, usage.GrokBilling)
	require.Equal(t, 49.0, usage.GrokBilling.CreditUsagePercent)
	require.Equal(t, 51.0, usage.GrokBilling.CreditRemainingPercent)
	require.Equal(t, "active", usage.GrokEntitlementStatus)
	require.Equal(t, int64(100), *usage.GrokRequestQuota.Limit)
	require.Equal(t, int64(12), *usage.GrokRequestQuota.Remaining)
	require.Equal(t, 30, *usage.GrokRetryAfterSeconds)
	require.NotNil(t, usage.UpdatedAt)
	require.Equal(t, updatedAt, usage.GrokLastQuotaProbeAt)
	require.Equal(t, updatedAt, usage.GrokLastHeadersSeenAt)
	require.Equal(t, http.StatusTooManyRequests, usage.GrokLastStatusCode)
	require.True(t, usage.UpdatedAt.Equal(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)))
}

func TestGrokQuotaFetcherBuildUsageInfoFromNoHeadersProbe(t *testing.T) {
	t.Parallel()

	probedAt := "2030-01-01T00:00:00Z"
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			grokQuotaSnapshotExtraKey: xai.QuotaSnapshot{
				StatusCode:        http.StatusOK,
				HeadersObserved:   false,
				ObservationSource: "active_probe",
				LastProbeAt:       probedAt,
				UpdatedAt:         probedAt,
			},
		},
	}

	usage := NewGrokQuotaFetcher().BuildUsageInfo(account)
	require.Equal(t, "billing_unknown", usage.ErrorCode)
	require.Equal(t, "unknown", usage.GrokBillingState)
	require.Equal(t, "no_headers", usage.GrokQuotaSnapshotState)
	require.Contains(t, usage.Error, "billing usage has not been queried")
	require.Equal(t, probedAt, usage.GrokLastQuotaProbeAt)
	require.Empty(t, usage.GrokLastHeadersSeenAt)
	require.Equal(t, http.StatusOK, usage.GrokLastStatusCode)
	require.Nil(t, usage.GrokRequestQuota)
	require.Nil(t, usage.GrokTokenQuota)
}

func TestGrokQuotaFetcherClassifiesForbiddenAndReauth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statusCode  int
		wantReauth  bool
		wantForbid  bool
		wantCode    string
		wantEntitle string
	}{
		{name: "reauth", statusCode: http.StatusUnauthorized, wantReauth: true, wantCode: "unauthenticated"},
		{name: "forbidden", statusCode: http.StatusForbidden, wantForbid: true, wantCode: "forbidden", wantEntitle: "forbidden"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			account := &Account{
				Platform: PlatformGrok,
				Type:     AccountTypeOAuth,
				Extra: map[string]any{
					grokQuotaSnapshotExtraKey: xai.QuotaSnapshot{
						StatusCode:      tt.statusCode,
						HeadersObserved: true,
						UpdatedAt:       time.Now().UTC().Format(time.RFC3339),
					},
				},
			}
			usage := NewGrokQuotaFetcher().BuildUsageInfo(account)
			require.Equal(t, tt.wantReauth, usage.NeedsReauth)
			require.Equal(t, tt.wantForbid, usage.IsForbidden)
			require.Equal(t, tt.wantCode, usage.ErrorCode)
			require.Equal(t, tt.wantEntitle, usage.GrokEntitlementStatus)
		})
	}
}

func TestGrokQuotaFetcherTreatsBillingExhausted403AsRateLimited(t *testing.T) {
	t.Parallel()

	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			grokBillingSnapshotExtraKey: &xai.BillingSnapshot{
				CreditUsagePercent:     100,
				CreditRemainingPercent: 0,
				CurrentPeriodEnd:       "2030-01-01T00:00:00Z",
				UpdatedAt:              "2029-12-25T00:00:00Z",
			},
			grokQuotaSnapshotExtraKey: &xai.QuotaSnapshot{
				StatusCode:      http.StatusForbidden,
				HeadersObserved: true,
				UpdatedAt:       "2029-12-25T00:00:00Z",
			},
		},
	}

	usage := NewGrokQuotaFetcher().BuildUsageInfo(account)

	require.False(t, usage.IsForbidden)
	require.Empty(t, usage.ForbiddenType)
	require.Equal(t, "rate_limited", usage.ErrorCode)
}
