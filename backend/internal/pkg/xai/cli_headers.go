package xai

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const (
	// CLI client identity expected by cli-chat-proxy.grok.com (Grok Build / free path).
	// Upstream rejects requests with missing version as:
	//   426 Your Grok CLI version (none) is outdated...
	CLIClientVersion      = "0.2.99"
	CLIClientIdentifier   = "grok-shell"
	CLIUserAgent          = "grok-shell/0.2.99 (linux; x86_64)"
	CLIAuthenticatorHdr   = "x-authenticateresponse"
	CLIAuthenticatorValue = "authenticate-response"
	CLIClientVersionHdr   = "x-grok-client-version"
	CLIClientIDHdr        = "x-grok-client-identifier"
	CLITokenAuthHdr       = "X-XAI-Token-Auth"
)

// CLIRequestMetadata carries the request identity understood by the Grok Build
// CLI proxy. ConversationID should already be tenant-isolated by the caller.
type CLIRequestMetadata struct {
	AccountID      int64
	UserID         string
	Model          string
	ConversationID string
}

type cliClientIdentity struct {
	agentID   string
	sessionID string
}

var cliClientIdentities sync.Map // map[int64]cliClientIdentity

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
func ApplyCLIChatProxyHeaders(req *http.Request, baseURL string, metadata ...CLIRequestMetadata) {
	if req == nil || !IsCLIChatProxyBaseURL(baseURL) {
		return
	}
	var meta CLIRequestMetadata
	if len(metadata) > 0 {
		meta = metadata[0]
	}
	identity := loadCLIClientIdentity(meta.AccountID)
	requestID := randomCLIHex(16)
	conversationID := strings.TrimSpace(meta.ConversationID)
	if conversationID == "" {
		conversationID = randomCLIHex(16)
	}
	req.Header.Set(CLIClientVersionHdr, CLIClientVersion)
	req.Header.Set(CLIClientIDHdr, CLIClientIdentifier)
	req.Header.Set(CLITokenAuthHdr, CLITokenAuthHeader)
	req.Header.Set(CLIAuthenticatorHdr, CLIAuthenticatorValue)
	req.Header.Set("x-grok-client-surface", "tui")
	req.Header.Set("x-grok-client-name", CLIClientIdentifier)
	if identity.agentID != "" {
		req.Header.Set("x-grok-agent-id", identity.agentID)
	}
	if identity.sessionID != "" {
		req.Header.Set("x-grok-session-id", identity.sessionID)
		req.Header.Set("x-grok-session-id-legacy", identity.sessionID)
	}
	if conversationID != "" {
		req.Header.Set("x-grok-conv-id", conversationID)
		req.Header.Set("x-grok-conversation-id", conversationID)
	}
	if requestID != "" {
		req.Header.Set("x-grok-req-id", requestID)
		req.Header.Set("x-grok-request-id", requestID)
	}
	if userID := strings.TrimSpace(meta.UserID); userID != "" {
		req.Header.Set("x-userid", userID)
	}
	if model := strings.TrimSpace(meta.Model); model != "" {
		req.Header.Set("x-grok-model-override", model)
	}
	if traceID, spanID := randomCLIHex(16), randomCLIHex(8); traceID != "" && spanID != "" {
		req.Header.Set("traceparent", "00-"+traceID+"-"+spanID+"-01")
	}
	// Override generic gateway UA; CLI proxy validates version via these headers
	// and also accepts a grok-shell User-Agent.
	req.Header.Set("User-Agent", CLIUserAgent)
}

func loadCLIClientIdentity(accountID int64) cliClientIdentity {
	if accountID > 0 {
		if value, ok := cliClientIdentities.Load(accountID); ok {
			if identity, valid := value.(cliClientIdentity); valid {
				return identity
			}
		}
	}
	identity := cliClientIdentity{agentID: randomCLIHex(16), sessionID: randomCLIUUID()}
	if accountID <= 0 {
		return identity
	}
	actual, _ := cliClientIdentities.LoadOrStore(accountID, identity)
	stored, _ := actual.(cliClientIdentity)
	return stored
}

func randomCLIHex(bytesLength int) string {
	if bytesLength <= 0 {
		return ""
	}
	value := make([]byte, bytesLength)
	if _, err := rand.Read(value); err != nil {
		return ""
	}
	return hex.EncodeToString(value)
}

func randomCLIUUID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return ""
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(value)
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:]
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
