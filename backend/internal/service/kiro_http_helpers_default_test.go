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
		"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
		[]byte(`{"ok":true}`),
		"access-token",
		"account-key",
		buildKiroMachineID(account),
		"",
		account,
	)
	require.NoError(t, err)
	require.Equal(t, "q.us-east-1.amazonaws.com", req.URL.Host)
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
