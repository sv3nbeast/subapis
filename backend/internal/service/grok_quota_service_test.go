//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type grokQuotaAccountRepo struct {
	AccountRepository
	accounts              map[int64]*Account
	updates               map[int64]map[string]any
	tempUnschedCalls      int
	lastTempUnschedID     int64
	lastTempUnschedUntil  time.Time
	lastTempUnschedReason string
}

func (r *grokQuotaAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.accounts[id], nil
}

func (r *grokQuotaAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	r.updates[id] = updates
	if account := r.accounts[id]; account != nil {
		if account.Extra == nil {
			account.Extra = make(map[string]any)
		}
		for key, value := range updates {
			account.Extra[key] = value
		}
	}
	return nil
}

func (r *grokQuotaAccountRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls++
	r.lastTempUnschedID = id
	r.lastTempUnschedUntil = until
	r.lastTempUnschedReason = reason
	return nil
}

type grokQuotaProxyRepo struct {
	ProxyRepository
	proxies map[int64]*Proxy
	calls   int
}

func (r *grokQuotaProxyRepo) GetByID(_ context.Context, id int64) (*Proxy, error) {
	r.calls++
	return r.proxies[id], nil
}

type grokQuotaHTTPUpstream struct {
	lastReq      *http.Request
	lastProxyURL string
	requests     []*http.Request
	resp         *http.Response
	responses    []*http.Response
	err          error
}

func (u *grokQuotaHTTPUpstream) Do(req *http.Request, proxyURL string, _ int64, _ int) (*http.Response, error) {
	u.lastReq = req
	u.lastProxyURL = proxyURL
	u.requests = append(u.requests, req)
	if u.err != nil {
		return nil, u.err
	}
	if len(u.responses) > 0 {
		resp := u.responses[0]
		u.responses = u.responses[1:]
		return resp, nil
	}
	return u.resp, nil
}

func (u *grokQuotaHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func grokBillingTestBody(usedPercent float64) string {
	return fmt.Sprintf(`{"config":{"creditUsagePercent":%g,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-07-09T18:40:47.778876+00:00","end":"2026-07-16T18:40:47.778876+00:00"},"onDemandCap":{"val":2500},"onDemandUsed":{"val":125},"isUnifiedBillingUser":true,"prepaidBalance":{"val":30}}}`, usedPercent)
}

func TestGrokQuotaServiceProbeUsageStoresOfficialBillingSnapshot(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          42,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &grokQuotaAccountRepo{
		accounts: map[int64]*Account{42: account},
	}
	upstream := &grokQuotaHTTPUpstream{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(grokBillingTestBody(49))),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"subscription_tier_display":"SuperGrok"}`)),
		},
	}}
	svc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), upstream)

	result, err := svc.ProbeUsage(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "billing", result.Source)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.NotNil(t, result.Billing)
	require.Equal(t, 49.0, result.Billing.CreditUsagePercent)
	require.Equal(t, 51.0, result.Billing.CreditRemainingPercent)
	require.Equal(t, 2500.0, result.Billing.OnDemandCap)
	require.Equal(t, 2375.0, result.Billing.OnDemandRemaining)
	require.Equal(t, "SuperGrok", result.Billing.SubscriptionTier)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, xai.BillingCreditsURL, upstream.requests[0].URL.String())
	require.Equal(t, xai.SettingsURL, upstream.requests[1].URL.String())
	require.Equal(t, http.MethodGet, upstream.requests[0].Method)
	require.Equal(t, "Bearer access-token", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, xai.CLITokenAuthHeader, upstream.requests[0].Header.Get("X-XAI-Token-Auth"))
	require.NotNil(t, repo.updates[42][grokBillingSnapshotExtraKey])
}

func TestGrokQuotaServiceProbeUsageDoesNotSendInferenceRequest(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          47,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"model_mapping": map[string]any{
				"grok":          "grok-composer",
				"grok-composer": "grok-composer-2.5-fast",
			},
		},
	}
	repo := &grokQuotaAccountRepo{
		accounts: map[int64]*Account{47: account},
	}
	upstream := &grokQuotaHTTPUpstream{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(grokBillingTestBody(0)))},
		{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))},
	}}
	svc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), upstream)

	result, err := svc.ProbeUsage(context.Background(), 47)
	require.NoError(t, err)
	require.NotNil(t, result.Billing)
	require.Len(t, upstream.requests, 2)
	for _, req := range upstream.requests {
		require.Equal(t, http.MethodGet, req.Method)
		require.NotContains(t, req.URL.String(), "/responses")
	}
}

func TestGrokQuotaServiceProbeUsageReportsBillingUpstreamError(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          48,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &grokQuotaAccountRepo{
		accounts: map[int64]*Account{48: account},
	}
	upstream := &grokQuotaHTTPUpstream{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"code":"invalid-argument","error":"Model not found"}`)),
	}}
	svc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), upstream)

	_, err := svc.ProbeUsage(context.Background(), 48)
	require.Error(t, err)
	require.Equal(t, "GROK_QUOTA_PROBE_UPSTREAM_ERROR", infraerrors.Reason(err))
	require.Contains(t, infraerrors.Message(err), "billing endpoint returned 400")
}

func TestGrokQuotaServiceProbeUsageLoadsProxyWhenAccountEdgeMissing(t *testing.T) {
	t.Parallel()

	proxyID := int64(7)
	account := &Account{
		ID:          46,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		ProxyID:     &proxyID,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &grokQuotaAccountRepo{
		accounts: map[int64]*Account{46: account},
	}
	proxyRepo := &grokQuotaProxyRepo{
		proxies: map[int64]*Proxy{
			proxyID: {
				ID:       proxyID,
				Protocol: "http",
				Host:     "proxy.test",
				Port:     3128,
			},
		},
	}
	upstream := &grokQuotaHTTPUpstream{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(grokBillingTestBody(20)))},
		{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))},
	}}
	svc := NewGrokQuotaService(repo, proxyRepo, NewGrokTokenProvider(repo, nil), upstream)

	_, err := svc.ProbeUsage(context.Background(), 46)
	require.NoError(t, err)
	require.Equal(t, 1, proxyRepo.calls)
	require.Equal(t, "http://proxy.test:3128", upstream.lastProxyURL)
}

func TestGrokQuotaServiceProbeUsageAcceptsOmittedZeroValues(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          45,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &grokQuotaAccountRepo{
		accounts: map[int64]*Account{45: account},
	}
	upstream := &grokQuotaHTTPUpstream{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-07-09T18:40:47Z","end":"2026-07-16T18:40:47Z"},"isUnifiedBillingUser":true}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))},
	}}
	svc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), upstream)

	result, err := svc.ProbeUsage(context.Background(), 45)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.NotNil(t, result.Billing)
	require.Zero(t, result.Billing.CreditUsagePercent)
	require.Equal(t, 100.0, result.Billing.CreditRemainingPercent)

	stored, ok := repo.updates[45][grokBillingSnapshotExtraKey].(*xai.BillingSnapshot)
	require.True(t, ok)
	require.Zero(t, stored.CreditUsagePercent)
	require.Equal(t, http.StatusOK, stored.StatusCode)
}

func TestGrokQuotaServiceProbeUsageReturnsBillingRateLimitError(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:       43,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &grokQuotaAccountRepo{
		accounts: map[int64]*Account{43: account},
	}
	upstream := &grokQuotaHTTPUpstream{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"45"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}
	svc := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), upstream)

	result, err := svc.ProbeUsage(context.Background(), 43)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, "GROK_QUOTA_PROBE_UPSTREAM_ERROR", infraerrors.Reason(err))
}

func TestGrokQuotaServiceResetQuotaUnsupported(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:       44,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
	}
	repo := &grokQuotaAccountRepo{
		accounts: map[int64]*Account{44: account},
	}
	svc := NewGrokQuotaService(repo, nil, nil, nil)

	_, err := svc.ResetQuota(context.Background(), 44)
	require.Error(t, err)
	require.Equal(t, http.StatusNotImplemented, infraerrors.Code(err))
	require.Equal(t, "GROK_QUOTA_RESET_UNSUPPORTED", infraerrors.Reason(err))
}

func TestAccountUsageServiceRefreshesOfficialGrokBillingAndCachesSnapshot(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          49,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
		Extra: map[string]any{},
	}
	repo := &grokQuotaAccountRepo{accounts: map[int64]*Account{49: account}}
	upstream := &grokQuotaHTTPUpstream{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(grokBillingTestBody(49)))},
		{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"subscription_tier_display":"SuperGrok"}`))},
	}}
	quotaService := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), upstream)
	usageService := NewAccountUsageService(repo, nil, nil, nil, nil, NewGrokQuotaFetcher(), nil, NewUsageCache(), nil, nil).
		SetGrokQuotaService(quotaService)

	usage, err := usageService.GetUsage(context.Background(), 49)
	require.NoError(t, err)
	require.NotNil(t, usage.GrokBilling)
	require.Equal(t, 49.0, usage.GrokBilling.CreditUsagePercent)
	require.Equal(t, 51.0, usage.GrokBilling.CreditRemainingPercent)
	require.Equal(t, "SuperGrok", usage.SubscriptionTier)
	require.Equal(t, "observed", usage.GrokBillingState)
	require.Len(t, upstream.requests, 2)

	usage, err = usageService.GetUsage(context.Background(), 49)
	require.NoError(t, err)
	require.NotNil(t, usage.GrokBilling)
	require.Len(t, upstream.requests, 2, "fresh persisted billing snapshot should avoid another upstream query")
}

func TestAccountUsageServiceDoesNotQueryOAuthBillingForGrokAPIKey(t *testing.T) {
	t.Parallel()

	account := &Account{
		ID:          50,
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"api_key": "test-key"},
		Extra:       map[string]any{},
	}
	repo := &grokQuotaAccountRepo{accounts: map[int64]*Account{50: account}}
	upstream := &grokQuotaHTTPUpstream{}
	quotaService := NewGrokQuotaService(repo, nil, NewGrokTokenProvider(repo, nil), upstream)
	usageService := NewAccountUsageService(repo, nil, nil, nil, nil, NewGrokQuotaFetcher(), nil, NewUsageCache(), nil, nil).
		SetGrokQuotaService(quotaService)

	usage, err := usageService.GetUsage(context.Background(), 50)
	require.NoError(t, err)
	require.Equal(t, "billing_unknown", usage.ErrorCode)
	require.Empty(t, upstream.requests)
}

func TestShouldAutoPauseGrokAccountByQuota(t *testing.T) {
	t.Parallel()

	zero := int64(0)
	limit := int64(10)
	resetFuture := time.Now().Add(time.Minute).Unix()
	retryAfter := 30
	tests := []struct {
		name     string
		snapshot xai.QuotaSnapshot
		want     bool
	}{
		{
			name: "remaining requests exhausted",
			snapshot: xai.QuotaSnapshot{
				Requests:  &xai.QuotaWindow{Limit: &limit, Remaining: &zero, ResetUnix: &resetFuture},
				UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			},
			want: true,
		},
		{
			name: "retry after active",
			snapshot: xai.QuotaSnapshot{
				RetryAfterSeconds: &retryAfter,
				UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
			},
			want: true,
		},
		{
			name: "retry after expired",
			snapshot: xai.QuotaSnapshot{
				RetryAfterSeconds: &retryAfter,
				UpdatedAt:         time.Now().Add(-time.Duration(retryAfter+1) * time.Second).UTC().Format(time.RFC3339),
			},
			want: false,
		},
		{
			name: "stale snapshot ignored",
			snapshot: xai.QuotaSnapshot{
				Requests:  &xai.QuotaWindow{Limit: &limit, Remaining: &zero, ResetUnix: &resetFuture},
				UpdatedAt: time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			account := &Account{
				Platform: PlatformGrok,
				Type:     AccountTypeOAuth,
				Extra: map[string]any{
					grokQuotaSnapshotExtraKey: tt.snapshot,
				},
			}
			got, _ := shouldAutoPauseGrokAccountByQuota(account)
			require.Equal(t, tt.want, got)
		})
	}
}
