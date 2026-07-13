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
	ApplyCLIChatProxyHeaders(cliReq, DefaultCLIBaseURL)
	require.Equal(t, CLIClientVersion, cliReq.Header.Get(CLIClientVersionHdr))
	require.Equal(t, CLIClientIdentifier, cliReq.Header.Get(CLIClientIDHdr))
	require.Equal(t, CLITokenAuthHeader, cliReq.Header.Get(CLITokenAuthHdr))
	require.Equal(t, CLIAuthenticatorValue, cliReq.Header.Get(CLIAuthenticatorHdr))
	require.Equal(t, CLIUserAgent, cliReq.Header.Get("User-Agent"))

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
