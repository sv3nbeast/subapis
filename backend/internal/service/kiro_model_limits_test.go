package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type concurrentKiroModelLimitsUpstream struct {
	calls atomic.Int64
}

type failingKiroModelLimitsUpstream struct {
	calls atomic.Int64
}

func (u *concurrentKiroModelLimitsUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *concurrentKiroModelLimitsUpstream) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls.Add(1)
	time.Sleep(10 * time.Millisecond)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"models":[{"modelId":"claude-sonnet-4.6","tokenLimits":{"maxInputTokens":1000000}}]}`)),
	}, nil
}

func (u *failingKiroModelLimitsUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *failingKiroModelLimitsUpstream) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls.Add(1)
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(`{"message":"slow down"}`)),
	}, nil
}

func TestResolveKiroModelContextWindowUsesRegionalPaginatedMetadataAndCache(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"models":[{"modelId":"claude-opus-4.8","tokenLimits":{"maxInputTokens":1000000}}],
				"nextToken":"page-two"
			}`)),
		},
		{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"models":[{"modelId":"claude-sonnet-4.6","tokenLimits":{"maxInputTokens":1000000}}]
			}`)),
		},
	}}
	svc := &GatewayService{
		httpUpstream:         upstream,
		kiroModelLimitsCache: newKiroModelLimitsCache(),
	}
	account := &Account{
		ID:          1701,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_region":  "eu-central-1",
			"profile_arn": "arn:aws:codewhisperer:eu-central-1:123456789012:profile/KIRO",
		},
	}

	window, source := svc.resolveKiroModelContextWindow(context.Background(), account, "access-token", "claude-sonnet-4-6")
	require.Equal(t, 1_000_000, window)
	require.Equal(t, kiroContextWindowSourceDynamic, source)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://q.eu-central-1.amazonaws.com/ListAvailableModels", upstream.requests[0].URL.Scheme+"://"+upstream.requests[0].URL.Host+upstream.requests[0].URL.Path)
	require.Empty(t, upstream.requests[0].URL.Query().Get("nextToken"))
	require.Equal(t, "page-two", upstream.requests[1].URL.Query().Get("nextToken"))

	// A second lookup in the same region must not issue another upstream call.
	window, source = svc.resolveKiroModelContextWindow(context.Background(), account, "rotated-token", "claude-opus-4.8")
	require.Equal(t, 1_000_000, window)
	require.Equal(t, kiroContextWindowSourceDynamic, source)
	require.Len(t, upstream.requests, 2)
}

func TestResolveKiroModelContextWindowSingleflightPreventsConcurrentMetadataBurst(t *testing.T) {
	upstream := &concurrentKiroModelLimitsUpstream{}
	svc := &GatewayService{
		httpUpstream:         upstream,
		kiroModelLimitsCache: newKiroModelLimitsCache(),
	}
	account := &Account{
		ID:          1701,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 20,
		Credentials: map[string]any{"api_region": "eu-central-1"},
	}

	const workers = 20
	var wg sync.WaitGroup
	type resolution struct {
		window int
		source string
	}
	results := make(chan resolution, workers)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			window, source := svc.resolveKiroModelContextWindow(context.Background(), account, "access-token", "claude-sonnet-4-6")
			results <- resolution{window: window, source: source}
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		require.Equal(t, 1_000_000, result.window)
		require.Equal(t, kiroContextWindowSourceDynamic, result.source)
	}
	require.Equal(t, int64(1), upstream.calls.Load())
}

func TestResolveKiroModelContextWindowTemporarilyCachesMetadataFailure(t *testing.T) {
	upstream := &failingKiroModelLimitsUpstream{}
	svc := &GatewayService{
		httpUpstream:         upstream,
		kiroModelLimitsCache: newKiroModelLimitsCache(),
	}
	account := &Account{
		ID:          1701,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"api_region": "eu-central-1"},
	}

	for i := 0; i < 2; i++ {
		window, source := svc.resolveKiroModelContextWindow(context.Background(), account, "access-token", "claude-sonnet-4-6")
		require.Zero(t, window)
		require.Empty(t, source)
	}
	require.Equal(t, int64(1), upstream.calls.Load(), "metadata 429 must not add one failed lookup to every inference request")
}

func TestApplyKiroContextWindowResolutionRestoresUnboundedPayloadEstimate(t *testing.T) {
	requestCtx := kiropkg.KiroRequestContext{
		PayloadInputTokenEstimate: 740_000,
		InputTokenBudget:          200_000,
		ContextWindowTokens:       200_000,
		ContextWindowSource:       "static_fallback",
	}

	applyKiroContextWindowResolution(&requestCtx, 1_000_000, kiroContextWindowSourceDynamic)

	require.Equal(t, 1_000_000, requestCtx.ContextWindowTokens)
	require.Equal(t, 740_000, requestCtx.InputTokenBudget)
	require.Equal(t, 740_000, requestCtx.PayloadInputTokenEstimate)
	require.Equal(t, kiroContextWindowSourceDynamic, requestCtx.ContextWindowSource)
}

func TestApplyKiroContextWindowResolutionStillCapsPayloadToRealLimit(t *testing.T) {
	requestCtx := kiropkg.KiroRequestContext{
		PayloadInputTokenEstimate: 1_200_000,
		InputTokenBudget:          200_000,
	}

	applyKiroContextWindowResolution(&requestCtx, 1_000_000, kiroContextWindowSourceDynamic)

	require.Equal(t, 1_000_000, requestCtx.InputTokenBudget)
}

func TestExecuteKiroUpstreamAppliesDynamicContextWindowToRequestContext(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"models":[{"modelId":"claude-sonnet-4.5","tokenLimits":{"maxInputTokens":256000}}]
			}`)),
		},
		newKiroEventStreamResponse(http.StatusOK, nil),
	}}
	svc := &GatewayService{
		httpUpstream:         upstream,
		kiroCooldownStore:    &kiroStreamFailoverCooldownStore{},
		kiroModelLimitsCache: newKiroModelLimitsCache(),
		tlsFPProfileService:  &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          1701,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_region": "eu-central-1",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-5","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`)

	resp, requestCtx, err := svc.executeKiroUpstream(context.Background(), account, body, "claude-sonnet-4-5", "claude-sonnet-4-5", "access-token", nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	_ = resp.Body.Close()
	require.Equal(t, 256_000, requestCtx.ContextWindowTokens)
	require.Equal(t, kiroContextWindowSourceDynamic, requestCtx.ContextWindowSource)
	require.Positive(t, requestCtx.PayloadInputTokenEstimate)
	require.Equal(t, requestCtx.PayloadInputTokenEstimate, requestCtx.InputTokenBudget)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "/ListAvailableModels", upstream.requests[0].URL.Path)
	require.Equal(t, "/generateAssistantResponse", upstream.requests[1].URL.Path)
}

func TestExtractKiroModelTokenLimitPageIgnoresModelsWithoutExactLimit(t *testing.T) {
	limits, nextToken, err := extractKiroModelTokenLimitPage([]byte(`{
		"models":[
			{"modelId":"claude-sonnet-4.6","tokenLimits":{"maxInputTokens":1000000}},
			{"modelId":"claude-sonnet-4.5","tokenLimits":{}},
			{"modelId":"claude-opus-4-8","tokenLimits":{"maxInputTokens":1000000}}
		],
		"nextToken":"next"
	}`))

	require.NoError(t, err)
	require.Equal(t, "next", nextToken)
	require.Equal(t, 1_000_000, limits["claude-sonnet-4.6"])
	require.Equal(t, 1_000_000, limits["claude-opus-4.8"])
	_, exists := limits["claude-sonnet-4.5"]
	require.False(t, exists)
}
