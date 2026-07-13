package xai

import (
	"net/http"
	"net/url"
	"strings"
)

const (
	// CLI client identity expected by cli-chat-proxy.grok.com (Grok Build / free path).
	// Upstream rejects requests with missing version as:
	//   426 Your Grok CLI version (none) is outdated...
	CLIClientVersion      = "0.2.93"
	CLIClientIdentifier   = "grok-shell"
	CLIUserAgent          = "grok-shell/0.2.93 (linux; x86_64)"
	CLIAuthenticatorHdr   = "x-authenticateresponse"
	CLIAuthenticatorValue = "authenticate-response"
	CLIClientVersionHdr   = "x-grok-client-version"
	CLIClientIDHdr        = "x-grok-client-identifier"
	CLITokenAuthHdr       = "X-XAI-Token-Auth"
)

// IsCLIChatProxyBaseURL reports whether baseURL targets the free Grok Build CLI proxy.
// Only this host requires Grok CLI client headers; api.x.ai does not.
func IsCLIChatProxyBaseURL(baseURL string) bool {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	return host == "cli-chat-proxy.grok.com"
}

// ApplyCLIChatProxyHeaders sets Grok CLI identity headers when talking to
// cli-chat-proxy.grok.com. For api.x.ai (or any non-CLI host) this is a no-op,
// so paid/official Grok accounts keep their existing request profile.
func ApplyCLIChatProxyHeaders(req *http.Request, baseURL string) {
	if req == nil || !IsCLIChatProxyBaseURL(baseURL) {
		return
	}
	req.Header.Set(CLIClientVersionHdr, CLIClientVersion)
	req.Header.Set(CLIClientIDHdr, CLIClientIdentifier)
	req.Header.Set(CLITokenAuthHdr, CLITokenAuthHeader)
	req.Header.Set(CLIAuthenticatorHdr, CLIAuthenticatorValue)
	// Override generic gateway UA; CLI proxy validates version via these headers
	// and also accepts a grok-shell User-Agent.
	req.Header.Set("User-Agent", CLIUserAgent)
}

// CLIChatProxyUserAgent returns the CLI User-Agent when baseURL is the free
// CLI proxy; otherwise it returns fallback (or the default gateway UA).
func CLIChatProxyUserAgent(baseURL, fallback string) string {
	if IsCLIChatProxyBaseURL(baseURL) {
		return CLIUserAgent
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "sub2api-grok/1.0"
}
