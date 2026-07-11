//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountTestService_KiroUsesKiroUpstreamInsteadOfAnthropic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          1,
		Name:        "kiro-test",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TESTSOCIAL",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{1: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"type":"authentication_error","message":"Invalid bearer token"}}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "gpt-4o", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)

	req := upstream.requests[0]
	require.Equal(t, "q.us-east-1.amazonaws.com", req.URL.Host)
	require.Equal(t, "/generateAssistantResponse", req.URL.Path)
	require.Equal(t, "Bearer kiro-access-token", req.Header.Get("Authorization"))
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Empty(t, req.Header.Get("X-Amz-Target"))
	require.Equal(t, "vibe", req.Header.Get("x-amzn-kiro-agent-mode"))
	require.Empty(t, req.Header.Get("anthropic-version"))
	require.NotContains(t, req.URL.Host, "api.anthropic.com")
}

func TestAccountTestService_Kiro429FallsBackToCodeWhispererEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          2,
		Name:        "kiro-fallback",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"api_region":   "us-west-2",
			"region":       "us-west-2",
			"profile_arn":  "arn:aws:codewhisperer:us-west-2:123456789012:profile/TESTFALLBACK",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{2: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusTooManyRequests, `{"message":"slow down"}`),
			newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"message":"Invalid bearer token"}}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 2)

	require.Equal(t, "q.us-west-2.amazonaws.com", upstream.requests[0].URL.Host)
	require.Empty(t, upstream.requests[0].Header.Get("X-Amz-Target"))
	require.Equal(t, "application/json", upstream.requests[0].Header.Get("Content-Type"))
	require.Equal(t, "codewhisperer.us-west-2.amazonaws.com", upstream.requests[1].URL.Host)
	require.Equal(t, "AmazonCodeWhispererStreamingService.GenerateAssistantResponse", upstream.requests[1].Header.Get("X-Amz-Target"))
	require.Equal(t, "application/json", upstream.requests[1].Header.Get("Content-Type"))
	require.Contains(t, err.Error(), "API returned 401")
}

func TestAccountTestService_KiroIDCWithoutProfileArnOmitsProfileArnAndUsesOIDCRegion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          5,
		Name:        "kiro-idc-default-region",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"auth_method":  "idc",
			"provider":     "AWS",
			"region":       "ap-northeast-2",
			"start_url":    "https://d-example.awsapps.com/start",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{5: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"message":"Invalid bearer token"}}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "q.ap-northeast-2.amazonaws.com", upstream.requests[0].URL.Host)
	body, readErr := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, readErr)
	require.NotContains(t, string(body), `"profileArn":`)
}

func TestAccountTestService_KiroQProbeDoesNotResolveMissingProfileArn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()
	originalSleep := kiroRetrySleep
	kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { kiroRetrySleep = originalSleep })

	account := &Account{
		ID:          8,
		Name:        "kiro-profile-resolve",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"auth_method":  "idc",
			"provider":     "AWS",
			"region":       "us-east-1",
			"start_url":    "https://d-example.awsapps.com/start",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{8: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"message":"Invalid bearer token"}}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)

	require.Equal(t, "q.us-east-1.amazonaws.com", upstream.requests[0].URL.Host)
	body, readErr := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, readErr)
	require.NotContains(t, string(body), `"profileArn":`)
	require.Empty(t, account.GetCredential("profile_arn"))
}

func TestAccountTestService_KiroInvalidModelErrorPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          6,
		Name:        "kiro-invalid-model",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-west-2:123456789012:profile/TESTINVALIDMODEL",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{6: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusBadRequest, `{"message":"Invalid model ID. Please select a different model to continue.","reason":"INVALID_MODEL_ID"}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-opus-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Equal(t, `API returned 400: {"message":"Invalid model ID. Please select a different model to continue.","reason":"INVALID_MODEL_ID"}`, err.Error())
}

func TestAccountTestService_KiroInvalidModelDoesNotRefreshProfileArnOrRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          7,
		Name:        "kiro-invalid-model-refresh",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/STALE",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{7: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusBadRequest, `{"message":"Invalid model ID. Please select a different model to continue.","reason":"INVALID_MODEL_ID"}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-opus-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Contains(t, err.Error(), "API returned 400")
	require.Len(t, upstream.requests, 1)

	firstBody, readErr := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, readErr)
	require.NotContains(t, string(firstBody), `"profileArn":`)
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/STALE", account.GetCredential("profile_arn"))
}

func TestAccountTestService_KiroPreferredEndpointIsIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          6,
		Name:        "kiro-preferred-endpoint",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "kiro-access-token",
			"api_region":         "us-west-2",
			"profile_arn":        "arn:aws:codewhisperer:us-west-2:123456789012:profile/PREFERRED",
			"preferred_endpoint": "codewhisperer",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{6: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"message":"Invalid bearer token"}}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "q.us-west-2.amazonaws.com", upstream.requests[0].URL.Host)
	require.Empty(t, upstream.requests[0].Header.Get("X-Amz-Target"))
}

func TestBuildKiroPayloadForAccount_KiroBuilderIDWithoutProfileArnOmitsProfileArn(t *testing.T) {
	account := &Account{
		ID:       3,
		Name:     "kiro-builder-id",
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method": "idc",
			"provider":    "BuilderId",
			"region":      "us-east-1",
			"client_id":   "builder-client-id",
		},
	}

	testPayload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	payloadBytes, err := json.Marshal(testPayload)
	require.NoError(t, err)

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(context.Background(), account, payloadBytes, "claude-sonnet-4-6", "kiro-access-token", "claude-sonnet-4-6", nil)
	require.NoError(t, err)
	kiroPayload := buildResult.Payload
	require.NotContains(t, string(kiroPayload), `"profileArn":`)
}

func TestBuildKiroPayloadForAccount_KiroBuilderIDWithCachedProfileArnOmitsForQMode(t *testing.T) {
	account := &Account{
		ID:       33,
		Name:     "kiro-builder-id-cached",
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method": "builder-id",
			"provider":    "BuilderId",
			"region":      "us-east-1",
			"client_id":   "builder-client-id",
			"profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/CACHED",
		},
	}

	testPayload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	payloadBytes, err := json.Marshal(testPayload)
	require.NoError(t, err)

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(context.Background(), account, payloadBytes, "claude-sonnet-4-6", "kiro-access-token", "claude-sonnet-4-6", nil)
	require.NoError(t, err)
	kiroPayload := buildResult.Payload
	require.NotContains(t, string(kiroPayload), `"profileArn"`)
}

func TestBuildKiroPayloadForAccount_KiroEnterpriseIDCOmitsMissingProfileArn(t *testing.T) {
	account := &Account{
		ID:       4,
		Name:     "kiro-enterprise-idc",
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method": "idc",
			"provider":    "AWS",
			"region":      "us-east-1",
			"client_id":   "enterprise-client-id",
			"start_url":   "https://d-example.awsapps.com/start",
		},
	}

	testPayload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	payloadBytes, err := json.Marshal(testPayload)
	require.NoError(t, err)

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(context.Background(), account, payloadBytes, "claude-sonnet-4-6", "kiro-access-token", "claude-sonnet-4-6", nil)
	require.NoError(t, err)
	kiroPayload := buildResult.Payload
	require.NotContains(t, string(kiroPayload), `"profileArn":`)
}
