package service

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewKiroJSONRequestSetsExplicitHostLikeKiroGo(t *testing.T) {
	account := &Account{
		ID:       901,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
	}

	req, err := newKiroJSONRequest(
		context.Background(),
		"https://runtime.eu-central-1.kiro.dev/",
		[]byte(`{"ok":true}`),
		"access-token",
		"account-key",
		buildKiroMachineID(account),
		"",
		account,
	)
	require.NoError(t, err)
	require.Equal(t, "runtime.eu-central-1.kiro.dev", req.URL.Host)
	require.Equal(t, req.URL.Host, req.Host)
}

func TestNewKiroJSONRequestAddsAPIKeyTokenType(t *testing.T) {
	account := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"kiroApiKey": "ksk_test_key",
		},
	}

	req, err := newKiroJSONRequest(
		context.Background(),
		"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
		[]byte(`{"ok":true}`),
		"ksk_test_key",
		"account-key",
		buildKiroMachineID(account),
		"",
		account,
	)

	require.NoError(t, err)
	require.Equal(t, []string{"API_KEY"}, req.Header["TokenType"])
	require.Equal(t, "Bearer ksk_test_key", req.Header.Get("Authorization"))
	require.Equal(t, kiroAWSJSONContentType, req.Header.Get("Content-Type"))
	require.Equal(t, kiroEventStreamContentType, req.Header.Get("Accept"))
	require.Equal(t, kiroGenerateAssistantResponseTarget, req.Header.Get("X-Amz-Target"))
	require.Contains(t, req.Header.Get("User-Agent"), "app/AmazonQ-For-CLI")
	require.Contains(t, req.Header.Get("X-Amz-User-Agent"), "app/AmazonQ-For-CLI")
	require.Equal(t, "false", req.Header.Get("x-amzn-codewhisperer-optout"))
	require.Empty(t, req.Header.Get("x-amzn-kiro-profile-arn"))

	oauthAccount := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "refresh-token",
		},
	}
	oauthReq, err := newKiroJSONRequest(
		context.Background(),
		"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
		[]byte(`{"ok":true}`),
		"access-token",
		"account-key",
		buildKiroMachineID(oauthAccount),
		"",
		oauthAccount,
	)

	require.NoError(t, err)
	require.Empty(t, oauthReq.Header["TokenType"])
	require.Equal(t, kiroJSONContentType, oauthReq.Header.Get("Content-Type"))
}

func TestNewKiroJSONRequestUsesAPIKeyHeadersOnlyForAttachedCLIKey(t *testing.T) {
	account := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method":  "external_idp",
			"provider":     "Internal",
			"kiro_api_key": "ksk_generation_key",
		},
	}

	cliReq, err := newKiroJSONRequest(
		context.Background(),
		"https://runtime.eu-central-1.kiro.dev/",
		[]byte(`{"ok":true}`),
		"ksk_generation_key",
		"account-key",
		buildKiroMachineID(account),
		kiroGenerateAssistantResponseTarget,
		account,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"API_KEY"}, cliReq.Header["TokenType"])
	require.Empty(t, cliReq.Header.Get("redirect-for-internal"))

	oauthReq, err := newKiroAvailableModelsRequest(context.Background(), account, "oauth-access-token", "")
	require.NoError(t, err)
	require.Equal(t, "EXTERNAL_IDP", oauthReq.Header.Get("TokenType"))
	require.Equal(t, "true", oauthReq.Header.Get("redirect-for-internal"))
}

func TestKiroOAuthAuxiliaryHeadersIgnoreAttachedCLIKey(t *testing.T) {
	account := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method":  "external_idp",
			"provider":     "Internal",
			"kiro_api_key": "ksk_generation_key",
		},
	}

	usageReq, err := http.NewRequest(http.MethodGet, "https://runtime.eu-central-1.kiro.dev/getUsageLimits", nil)
	require.NoError(t, err)
	(&AccountUsageService{}).applyKiroRuntimeHeaders(usageReq, account, "oauth-access-token")
	require.Equal(t, "EXTERNAL_IDP", usageReq.Header.Get("TokenType"))

	profileReq, err := http.NewRequest(http.MethodPost, "https://q.eu-central-1.amazonaws.com/", nil)
	require.NoError(t, err)
	applyKiroListAvailableProfilesHeaders(profileReq, account, "oauth-access-token", 1, 1)
	require.Equal(t, "EXTERNAL_IDP", profileReq.Header.Get("TokenType"))
}

func TestBuildKiroClientRequestIDUsesUpstreamID(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("x-amzn-requestid", "aws-request-id")

	require.Equal(t, "aws-request-id", buildKiroClientRequestID(resp))
}

func TestBuildKiroClientRequestIDGeneratesClaudeCompatibleFallback(t *testing.T) {
	requestID := buildKiroClientRequestID(&http.Response{Header: http.Header{}})

	require.Regexp(t, regexp.MustCompile(`^req_01[0-9A-Za-z]{25}$`), requestID)
}
