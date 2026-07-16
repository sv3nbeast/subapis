//go:build unit

package xai

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCLIChatProxyBaseURL(t *testing.T) {
	require.True(t, IsCLIChatProxyBaseURL(DefaultCLIBaseURL))
	require.True(t, IsCLIChatProxyBaseURL("https://cli-chat-proxy.grok.com/v1/"))
	require.True(t, IsCLIChatProxyBaseURL("https://CLI-CHAT-PROXY.GROK.COM/v1"))
	require.False(t, IsCLIChatProxyBaseURL(DefaultBaseURL))
	require.False(t, IsCLIChatProxyBaseURL("https://api.x.ai/v1"))
	require.False(t, IsCLIChatProxyBaseURL(""))
	require.False(t, IsCLIChatProxyBaseURL("https://example.com"))
	require.False(t, IsCLIChatProxyBaseURL("https://cli-chat-proxy.grok.com.example.com/v1"))
	require.False(t, IsCLIChatProxyBaseURL("://cli-chat-proxy.grok.com/v1"))
}

func TestApplyCLIChatProxyHeadersOnlyForCLIHost(t *testing.T) {
	cliReq, err := http.NewRequest(http.MethodPost, DefaultCLIBaseURL+"/responses", nil)
	require.NoError(t, err)
	cliReq.Header.Set("User-Agent", "sub2api-grok/1.0")
	ApplyCLIChatProxyHeaders(cliReq, DefaultCLIBaseURL, CLIRequestMetadata{
		AccountID: 7, UserID: "user-7", Model: "grok-4.5", ConversationID: "conversation-7",
	})
	require.Equal(t, CLIClientVersion, cliReq.Header.Get(CLIClientVersionHdr))
	require.Equal(t, CLIClientIdentifier, cliReq.Header.Get(CLIClientIDHdr))
	require.Equal(t, CLITokenAuthHeader, cliReq.Header.Get(CLITokenAuthHdr))
	require.Equal(t, CLIAuthenticatorValue, cliReq.Header.Get(CLIAuthenticatorHdr))
	require.Equal(t, CLIUserAgent, cliReq.Header.Get("User-Agent"))
	require.Equal(t, "tui", cliReq.Header.Get("x-grok-client-surface"))
	require.Len(t, cliReq.Header.Get("x-grok-agent-id"), 32)
	require.Len(t, cliReq.Header.Get("x-grok-session-id"), 36)
	require.Equal(t, "conversation-7", cliReq.Header.Get("x-grok-conv-id"))
	require.Equal(t, cliReq.Header.Get("x-grok-req-id"), cliReq.Header.Get("x-grok-request-id"))
	require.Equal(t, "user-7", cliReq.Header.Get("x-userid"))
	require.Equal(t, "grok-4.5", cliReq.Header.Get("x-grok-model-override"))
	require.Len(t, cliReq.Header.Get("traceparent"), 55)

	secondReq, err := http.NewRequest(http.MethodPost, DefaultCLIBaseURL+"/responses", nil)
	require.NoError(t, err)
	ApplyCLIChatProxyHeaders(secondReq, DefaultCLIBaseURL, CLIRequestMetadata{AccountID: 7})
	require.Equal(t, cliReq.Header.Get("x-grok-agent-id"), secondReq.Header.Get("x-grok-agent-id"))
	require.Equal(t, cliReq.Header.Get("x-grok-session-id"), secondReq.Header.Get("x-grok-session-id"))
	require.NotEqual(t, cliReq.Header.Get("x-grok-req-id"), secondReq.Header.Get("x-grok-req-id"))

	apiReq, err := http.NewRequest(http.MethodPost, DefaultBaseURL+"/responses", nil)
	require.NoError(t, err)
	apiReq.Header.Set("User-Agent", "sub2api-grok/1.0")
	ApplyCLIChatProxyHeaders(apiReq, DefaultBaseURL)
	require.Empty(t, apiReq.Header.Get(CLIClientVersionHdr))
	require.Empty(t, apiReq.Header.Get(CLIClientIDHdr))
	require.Empty(t, apiReq.Header.Get(CLITokenAuthHdr))
	require.Equal(t, "sub2api-grok/1.0", apiReq.Header.Get("User-Agent"))
}

func TestCLIChatProxyUserAgent(t *testing.T) {
	require.Equal(t, CLIUserAgent, CLIChatProxyUserAgent(DefaultCLIBaseURL, "sub2api-grok/1.0"))
	require.Equal(t, "sub2api-grok/1.0", CLIChatProxyUserAgent(DefaultBaseURL, "sub2api-grok/1.0"))
	require.Equal(t, "custom", CLIChatProxyUserAgent(DefaultBaseURL, "custom"))
	require.Equal(t, "sub2api-grok/1.0", CLIChatProxyUserAgent(DefaultBaseURL, ""))
}
