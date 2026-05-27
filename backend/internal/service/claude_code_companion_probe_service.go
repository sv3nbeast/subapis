package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	gocache "github.com/patrickmn/go-cache"
	"github.com/tidwall/gjson"
)

const (
	claudeCodeCompanionBaseURL         = "https://api.anthropic.com"
	claudeCodeCompanionDefaultInterval = 300 * time.Second
	claudeCodeCompanionDefaultTimeout  = 5 * time.Second
)

type claudeCodeCompanionContextKey struct{}

type ClaudeCodeCompanionProbeService struct {
	httpUpstream HTTPUpstream
	cache        *gocache.Cache
}

type ClaudeCodeCompanionProbeInput struct {
	Account      *Account
	Body         []byte
	Token        string
	TokenType    string
	ProxyURL     string
	TLSProfile   *tlsfingerprint.Profile
	SessionID    string
	Config       config.GatewayClaudeCodeMimicryConfig
	RequestModel string
}

type claudeCodeCompanionEndpoint struct {
	Name        string
	Method      string
	URL         string
	UserAgent   string
	Beta        string
	Body        []byte
	ContentType string
	Auth        bool
}

func NewClaudeCodeCompanionProbeService(httpUpstream HTTPUpstream) *ClaudeCodeCompanionProbeService {
	return &ClaudeCodeCompanionProbeService{
		httpUpstream: httpUpstream,
		cache:        gocache.New(10*time.Minute, time.Minute),
	}
}

func WithClaudeCodeCompanionProbeTriggered(ctx context.Context) context.Context {
	return context.WithValue(ctx, claudeCodeCompanionContextKey{}, true)
}

func IsClaudeCodeCompanionProbeTriggered(ctx context.Context) bool {
	triggered, _ := ctx.Value(claudeCodeCompanionContextKey{}).(bool)
	return triggered
}

func (s *ClaudeCodeCompanionProbeService) MaybeTrigger(ctx context.Context, input ClaudeCodeCompanionProbeInput) {
	if s == nil || s.httpUpstream == nil || input.Account == nil {
		return
	}
	if !input.Config.Enabled || !input.Config.SyntheticCompanion.Enabled {
		return
	}
	if input.TokenType != "oauth" || strings.TrimSpace(input.Token) == "" {
		return
	}
	if !input.Account.IsAnthropicOAuthOrSetupToken() {
		return
	}

	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		sessionID = deriveClaudeCodeCompanionSessionID(input.Account, input.Body)
	}
	cacheKey := claudeCodeCompanionCacheKey(input.Account.ID, sessionID)
	interval := claudeCodeCompanionInterval(input.Config.SyntheticCompanion.MinIntervalSeconds)
	if s.cache != nil {
		if _, found := s.cache.Get(cacheKey); found {
			slog.Debug("claude_code_companion_probe_skipped",
				"account_id", input.Account.ID,
				"session_hash", hashForCompanionLog(sessionID),
				"reason", "throttled",
			)
			return
		}
		s.cache.Set(cacheKey, true, interval)
	}

	timeout := claudeCodeCompanionTimeout(input.Config.SyntheticCompanion.TimeoutSeconds)
	go s.run(context.WithoutCancel(ctx), input, sessionID, timeout)
}

func (s *ClaudeCodeCompanionProbeService) run(ctx context.Context, input ClaudeCodeCompanionProbeInput, sessionID string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoints := buildClaudeCodeCompanionEndpoints(input, sessionID)
	for _, endpoint := range endpoints {
		s.sendOne(ctx, input, endpoint, sessionID)
		if ctx.Err() != nil {
			return
		}
	}
}

func (s *ClaudeCodeCompanionProbeService) sendOne(ctx context.Context, input ClaudeCodeCompanionProbeInput, endpoint claudeCodeCompanionEndpoint, sessionID string) {
	var body io.Reader
	if len(endpoint.Body) > 0 {
		body = bytes.NewReader(endpoint.Body)
	}
	req, err := http.NewRequestWithContext(ctx, endpoint.Method, endpoint.URL, body)
	if err != nil {
		logClaudeCodeCompanionResult(input.Account, endpoint, sessionID, 0, 0, "build_request")
		return
	}
	if endpoint.Auth {
		setHeaderRaw(req.Header, "authorization", "Bearer "+input.Token)
	}
	if endpoint.UserAgent != "" {
		setHeaderRaw(req.Header, "User-Agent", endpoint.UserAgent)
	}
	if endpoint.Beta != "" {
		setHeaderRaw(req.Header, "anthropic-beta", endpoint.Beta)
	}
	if endpoint.ContentType != "" {
		setHeaderRaw(req.Header, "content-type", endpoint.ContentType)
	}
	setHeaderRaw(req.Header, "Accept", "application/json")
	setHeaderRaw(req.Header, "Accept-Encoding", "gzip, deflate, br")
	if strings.Contains(endpoint.URL, "/v1/") {
		setHeaderRaw(req.Header, "anthropic-version", "2023-06-01")
	}

	start := time.Now()
	resp, err := s.httpUpstream.DoWithTLS(req, input.ProxyURL, input.Account.ID, input.Account.Concurrency, input.TLSProfile)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		logClaudeCodeCompanionResult(input.Account, endpoint, sessionID, 0, durationMs, "request_error")
		return
	}
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
	}
	logClaudeCodeCompanionResult(input.Account, endpoint, sessionID, resp.StatusCode, durationMs, "")
}

func buildClaudeCodeCompanionEndpoints(input ClaudeCodeCompanionProbeInput, sessionID string) []claudeCodeCompanionEndpoint {
	endpoints := []claudeCodeCompanionEndpoint{
		{
			Name:      "claude_cli_bootstrap",
			Method:    http.MethodGet,
			URL:       claudeCodeCompanionBaseURL + "/api/claude_cli/bootstrap",
			UserAgent: "claude-code/" + claude.CLICurrentVersion,
			Beta:      claude.BetaOAuth,
			Auth:      true,
		},
		{
			Name:      "claude_code_penguin_mode",
			Method:    http.MethodGet,
			URL:       claudeCodeCompanionBaseURL + "/api/claude_code_penguin_mode",
			UserAgent: "axios/1.13.6",
			Beta:      claude.BetaOAuth,
			Auth:      true,
		},
		{
			Name:      "claude_code_grove",
			Method:    http.MethodGet,
			URL:       claudeCodeCompanionBaseURL + "/api/claude_code_grove",
			UserAgent: claude.DefaultHeaders["User-Agent"],
			Beta:      claude.BetaOAuth,
			Auth:      true,
		},
		{
			Name:      "oauth_account_settings",
			Method:    http.MethodGet,
			URL:       claudeCodeCompanionBaseURL + "/api/oauth/account/settings",
			UserAgent: claude.DefaultHeaders["User-Agent"],
			Beta:      claude.BetaOAuth,
			Auth:      true,
		},
		{
			Name:      "mcp_servers",
			Method:    http.MethodGet,
			URL:       claudeCodeCompanionBaseURL + "/v1/mcp_servers?limit=1000",
			UserAgent: "axios/1.13.6",
			Beta:      "mcp-servers-2025-12-04",
			Auth:      true,
		},
		{
			Name:      "mcp_registry_servers",
			Method:    http.MethodGet,
			URL:       claudeCodeCompanionBaseURL + "/mcp-registry/v0/servers?limit=50&offset=0",
			UserAgent: "axios/1.13.6",
			Auth:      false,
		},
	}
	if shouldSendClaudeCodeTitleProbe(input.Config.SyntheticCompanion.Mode) {
		endpoints = append(endpoints, buildClaudeCodeTitleProbeEndpoint(input, sessionID))
	}
	return endpoints
}

func buildClaudeCodeTitleProbeEndpoint(input ClaudeCodeCompanionProbeInput, sessionID string) claudeCodeCompanionEndpoint {
	titleBody := map[string]any{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 32000,
		"stream":     true,
		"system": []map[string]any{
			{
				"type": "text",
				"text": "Generate a concise, sentence-case title for this conversation.",
			},
		},
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": extractCompanionTitleSeed(input.Body)},
				},
			},
		},
	}
	if uid := strings.TrimSpace(gjson.GetBytes(input.Body, "metadata.user_id").String()); uid != "" {
		titleBody["metadata"] = map[string]any{"user_id": uid}
	}
	if sessionID != "" && titleBody["metadata"] == nil {
		titleBody["metadata"] = map[string]any{"user_id": FormatMetadataUserID(generateClientID(), input.Account.GetExtraString("account_uuid"), sessionID, claude.CLICurrentVersion)}
	}
	body, _ := json.Marshal(titleBody)
	return claudeCodeCompanionEndpoint{
		Name:        "haiku_title",
		Method:      http.MethodPost,
		URL:         claudeAPIURL,
		UserAgent:   claude.DefaultHeaders["User-Agent"],
		Beta:        claude.HaikuBetaHeader,
		Body:        body,
		ContentType: "application/json",
		Auth:        true,
	}
}

func shouldSendClaudeCodeTitleProbe(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case config.ClaudeCodeSyntheticCompanionModeAuxOnly:
		return false
	default:
		return true
	}
}

func claudeCodeCompanionInterval(seconds int) time.Duration {
	if seconds <= 0 {
		return claudeCodeCompanionDefaultInterval
	}
	return time.Duration(seconds) * time.Second
}

func claudeCodeCompanionTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return claudeCodeCompanionDefaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

func claudeCodeCompanionCacheKey(accountID int64, sessionID string) string {
	if sessionID == "" {
		sessionID = "account"
	}
	return strings.Join([]string{"claude_code_companion", claudeCodeCompanionFormatInt(accountID), sessionID}, ":")
}

func deriveClaudeCodeCompanionSessionID(account *Account, body []byte) string {
	if uid := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()); uid != "" {
		if parsed := ParseMetadataUserID(uid); parsed != nil && parsed.SessionID != "" {
			return parsed.SessionID
		}
	}
	if hash := hashBodyForSessionSeed(body); hash != "" && account != nil {
		return generateSessionUUID(claudeCodeCompanionFormatInt(account.ID) + "::" + hash)
	}
	if account != nil {
		return "account-" + claudeCodeCompanionFormatInt(account.ID)
	}
	return "account"
}

func extractCompanionTitleSeed(body []byte) string {
	text := strings.TrimSpace(extractFirstUserText(body))
	if text == "" {
		return "New conversation"
	}
	if len(text) > 800 {
		return text[:800]
	}
	return text
}

func logClaudeCodeCompanionResult(account *Account, endpoint claudeCodeCompanionEndpoint, sessionID string, status int, durationMs int64, errKind string) {
	accountID := int64(0)
	accountName := ""
	if account != nil {
		accountID = account.ID
		accountName = account.Name
	}
	slog.Info("claude_code_companion_probe",
		"account_id", accountID,
		"account_name", accountName,
		"endpoint", endpoint.Name,
		"method", endpoint.Method,
		"path", safeCompanionPath(endpoint.URL),
		"status", status,
		"duration_ms", durationMs,
		"error_kind", errKind,
		"session_hash", hashForCompanionLog(sessionID),
		"client_version", claude.CLICurrentVersion,
	)
}

func safeCompanionPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}

func hashForCompanionLog(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func claudeCodeCompanionFormatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}
