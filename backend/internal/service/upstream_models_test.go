package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func upstreamModelSyncTestConfig() *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		},
	}
}

func TestBuildV1ModelsURL(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://api.anthropic.com/v1/models", buildV1ModelsURL("https://api.anthropic.com"))
	require.Equal(t, "https://api.anthropic.com/v1/models", buildV1ModelsURL("https://api.anthropic.com/v1"))
	require.Equal(t, "https://api.anthropic.com/v1/models", buildV1ModelsURL("https://api.anthropic.com/v1/models"))
	require.Equal(t, "https://gateway.example.com/antigravity/v1/models", buildV1ModelsURL("https://gateway.example.com/antigravity/"))
}

func TestBuildGeminiModelsURL(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://generativelanguage.googleapis.com/v1beta/models", buildGeminiModelsURL("https://generativelanguage.googleapis.com"))
	require.Equal(t, "https://generativelanguage.googleapis.com/v1beta/models", buildGeminiModelsURL("https://generativelanguage.googleapis.com/v1beta"))
	require.Equal(t, "https://generativelanguage.googleapis.com/v1beta/models", buildGeminiModelsURL("https://generativelanguage.googleapis.com/v1beta/models"))
}

func TestExtractUpstreamModelIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "openai and anthropic data array",
			body: `{"data":[{"id":"claude-sonnet-4-5"},{"id":"gpt-5"},{"id":"gpt-5"},{"id":""}]}`,
			want: []string{"claude-sonnet-4-5", "gpt-5"},
		},
		{
			name: "gemini models array strips prefix",
			body: `{"models":[{"name":"models/gemini-2.5-pro"},{"name":"gemini-2.5-flash"}]}`,
			want: []string{"gemini-2.5-flash", "gemini-2.5-pro"},
		},
		{
			name: "kiro list available models",
			body: `{"models":[{"modelId":"claude-sonnet-4.6","displayName":"Claude Sonnet 4.6"},{"modelId":"claude-opus-4.8"}]}`,
			want: []string{"claude-opus-4.8", "claude-sonnet-4.6"},
		},
		{
			name: "kiro modelName fallback",
			body: `{"models":[{"modelName":"claude-haiku-4.5"}]}`,
			want: []string{"claude-haiku-4.5"},
		},
		{
			name: "top level array",
			body: `[{"id":"z-model"},{"name":"models/a-model"}]`,
			want: []string{"a-model", "z-model"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := extractUpstreamModelIDs([]byte(tt.body))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBuildUpstreamModelsRequestsForAPIKeyAccounts(t *testing.T) {
	t.Parallel()

	svc := &AccountTestService{cfg: upstreamModelSyncTestConfig()}
	ctx := context.Background()

	anthropicReq, err := svc.buildAnthropicUpstreamModelsRequest(ctx, &Account{
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "anthropic-key",
			"base_url": "https://anthropic.example.com/v1",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://anthropic.example.com/v1/models", anthropicReq.URL.String())
	require.Equal(t, "anthropic-key", anthropicReq.Header.Get("x-api-key"))
	require.Equal(t, "2023-06-01", anthropicReq.Header.Get("anthropic-version"))

	openAIReq, err := svc.buildOpenAIUpstreamModelsRequest(ctx, &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "openai-key",
			"base_url": "https://openai.example.com",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://openai.example.com/v1/models", openAIReq.URL.String())
	require.Equal(t, "Bearer openai-key", openAIReq.Header.Get("Authorization"))

	geminiReq, err := svc.buildGeminiUpstreamModelsRequest(ctx, &Account{
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "gemini-key",
			"base_url": "https://generativelanguage.googleapis.com/v1beta",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://generativelanguage.googleapis.com/v1beta/models", geminiReq.URL.String())
	require.Equal(t, "gemini-key", geminiReq.Header.Get("x-goog-api-key"))

	antigravityReq, err := svc.buildAntigravityAPIKeyModelsRequest(ctx, &Account{
		Platform: PlatformAntigravity,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "antigravity-key",
			"base_url": "https://gateway.example.com/antigravity",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example.com/antigravity/v1/models", antigravityReq.URL.String())
	require.Equal(t, "antigravity-key", antigravityReq.Header.Get("x-api-key"))
}

func TestBuildKiroUpstreamModelsRequest(t *testing.T) {
	t.Parallel()

	svc := &AccountTestService{cfg: upstreamModelSyncTestConfig()}
	req, err := svc.buildKiroUpstreamModelsRequest(context.Background(), &Account{
		ID:       901,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"api_region":   "eu-central-1",
			"profile_arn":  "arn:aws:codewhisperer:eu-central-1:123456789012:profile/KIRO",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://codewhisperer.us-east-1.amazonaws.com/ListAvailableModels?maxResults=50&origin=AI_EDITOR&profileArn=arn%3Aaws%3Acodewhisperer%3Aeu-central-1%3A123456789012%3Aprofile%2FKIRO", req.URL.String())
	require.Equal(t, req.URL.Host, req.Host)
	require.Equal(t, "Bearer kiro-access-token", req.Header.Get("Authorization"))
	require.Equal(t, "application/json", req.Header.Get("Accept"))
	require.Empty(t, req.Header.Get("x-amzn-kiro-agent-mode"))
	require.Equal(t, "true", req.Header.Get("x-amzn-codewhisperer-optout"))
	require.Contains(t, req.Header.Get("User-Agent"), "api/codewhispererruntime#")
	require.Contains(t, req.Header.Get("User-Agent"), "KiroIDE-")
	require.Contains(t, req.Header.Get("X-Amz-User-Agent"), "KiroIDE-")
	require.Empty(t, req.Header.Get("Amz-Sdk-Invocation-Id"))
	require.Empty(t, req.Header.Get("Amz-Sdk-Request"))
}

func TestBuildKiroUpstreamModelsRequestUsesProfileArnRegionWhenAPIRegionMissing(t *testing.T) {
	t.Parallel()

	svc := &AccountTestService{cfg: upstreamModelSyncTestConfig()}
	req, err := svc.buildKiroUpstreamModelsRequest(context.Background(), &Account{
		ID:       904,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"region":       "ap-northeast-2",
			"profile_arn":  "arn:aws:codewhisperer:eu-west-1:123456789012:profile/KIRO",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "codewhisperer.us-east-1.amazonaws.com", req.URL.Host)
	require.Equal(t, "arn:aws:codewhisperer:eu-west-1:123456789012:profile/KIRO", req.URL.Query().Get("profileArn"))
}

func TestBuildKiroUpstreamModelsRequestUsesCodeWhispererRestBaseRegardlessOfAPIRegion(t *testing.T) {
	t.Parallel()

	svc := &AccountTestService{cfg: upstreamModelSyncTestConfig()}
	req, err := svc.buildKiroUpstreamModelsRequest(context.Background(), &Account{
		ID:       905,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"api_region":   "us-west-2",
			"profile_arn":  "arn:aws:codewhisperer:eu-west-1:123456789012:profile/KIRO",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "codewhisperer.us-east-1.amazonaws.com", req.URL.Host)
	require.Equal(t, "arn:aws:codewhisperer:eu-west-1:123456789012:profile/KIRO", req.URL.Query().Get("profileArn"))
}

func TestFetchUpstreamSupportedModelsParsesKiroListAvailableModels(t *testing.T) {
	t.Parallel()

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"models":[{"modelId":"claude-sonnet-4.6"},{"modelId":"claude-opus-4.8"},{"modelId":"claude-sonnet-4.6"}]}`)),
	}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          upstreamModelSyncTestConfig(),
	}

	models, err := svc.FetchUpstreamSupportedModels(context.Background(), &Account{
		ID:       902,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"api_region":   "us-east-1",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"claude-opus-4.8", "claude-sonnet-4.6"}, models)
	require.Equal(t, "https://codewhisperer.us-east-1.amazonaws.com/ListAvailableModels?maxResults=50&origin=AI_EDITOR", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer kiro-access-token", upstream.lastReq.Header.Get("Authorization"))
}

func TestFetchUpstreamSupportedModelsPaginatesKiroListAvailableModels(t *testing.T) {
	t.Parallel()

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"models":[{"modelId":"claude-sonnet-4.6"}],"nextToken":"page-two"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"models":[{"modelId":"claude-opus-4.8"}]}`)),
		},
	}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          upstreamModelSyncTestConfig(),
	}

	models, err := svc.FetchUpstreamSupportedModels(context.Background(), &Account{
		ID:       903,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"api_region":   "eu-central-1",
			"profile_arn":  "arn:aws:codewhisperer:eu-central-1:123456789012:profile/KIRO",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"claude-opus-4.8", "claude-sonnet-4.6"}, models)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "", upstream.requests[0].URL.Query().Get("nextToken"))
	require.Equal(t, "page-two", upstream.requests[1].URL.Query().Get("nextToken"))
	for _, req := range upstream.requests {
		require.Equal(t, "arn:aws:codewhisperer:eu-central-1:123456789012:profile/KIRO", req.URL.Query().Get("profileArn"))
		require.Equal(t, "AI_EDITOR", req.URL.Query().Get("origin"))
		require.Equal(t, "50", req.URL.Query().Get("maxResults"))
	}
}

func TestBuildAntigravityAPIKeyModelsRequestRejectsOfficialCloudCodeBase(t *testing.T) {
	t.Parallel()

	svc := &AccountTestService{cfg: upstreamModelSyncTestConfig()}
	_, err := svc.buildAntigravityAPIKeyModelsRequest(context.Background(), &Account{
		Platform: PlatformAntigravity,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "antigravity-key",
			"base_url": "https://cloudcode-pa.googleapis.com",
		},
	})
	require.Error(t, err)

	var syncErr *UpstreamModelSyncError
	require.True(t, errors.As(err, &syncErr))
	require.Equal(t, UpstreamModelSyncErrorUnsupported, syncErr.Kind)
	require.Contains(t, syncErr.SafeMessage(), "compatible gateway")
}

func TestBuildAnthropicUpstreamModelsRequestRejectsBedrock(t *testing.T) {
	t.Parallel()

	svc := &AccountTestService{cfg: upstreamModelSyncTestConfig()}
	_, err := svc.buildAnthropicUpstreamModelsRequest(context.Background(), &Account{
		Platform: PlatformAnthropic,
		Type:     AccountTypeBedrock,
	})
	require.Error(t, err)

	var syncErr *UpstreamModelSyncError
	require.True(t, errors.As(err, &syncErr))
	require.Equal(t, UpstreamModelSyncErrorUnsupported, syncErr.Kind)
}

func TestFetchUpstreamSupportedModelsParsesOpenAIResponse(t *testing.T) {
	t.Parallel()

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-5"},{"id":"gpt-5"},{"name":"o3"}]}`)),
	}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          upstreamModelSyncTestConfig(),
	}

	models, err := svc.FetchUpstreamSupportedModels(context.Background(), &Account{
		ID:       7,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "openai-key",
			"base_url": "https://openai.example.com/v1",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5", "o3"}, models)
	require.Equal(t, "https://openai.example.com/v1/models", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer openai-key", upstream.lastReq.Header.Get("Authorization"))
}

func TestFetchUpstreamSupportedModelsDoesNotExposeUpstreamBody(t *testing.T) {
	t.Parallel()

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"SECRET_TOKEN should not be exposed"}`)),
	}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          upstreamModelSyncTestConfig(),
	}

	_, err := svc.FetchUpstreamSupportedModels(context.Background(), &Account{
		ID:       8,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "openai-key",
			"base_url": "https://openai.example.com/v1",
		},
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SECRET_TOKEN")

	var syncErr *UpstreamModelSyncError
	require.True(t, errors.As(err, &syncErr))
	require.Equal(t, UpstreamModelSyncErrorUpstream, syncErr.Kind)
	require.NotContains(t, syncErr.SafeMessage(), "SECRET_TOKEN")
	require.Contains(t, syncErr.SafeMessage(), "HTTP 502")
}
