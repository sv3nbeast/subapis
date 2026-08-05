package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const anthropicCCHKnownVectorBody = `{"model":"model-a","messages":[{"role":"user","content":[{"type":"text","text":"x"}]}],"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.220.test; cc_entrypoint=sdk-cli; cch=00000;"},{"type":"text","text":"system-x"}],"tools":[],"metadata":{"user_id":"meta-x"},"max_tokens":1,"thinking":{"type":"adaptive","display":"omitted"},"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"output_config":{"effort":"high"},"stream":true}`

func TestSignAnthropicCCHKnownVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "base", body: anthropicCCHKnownVectorBody, want: "7ee87"},
		{name: "model ignored", body: strings.Replace(anthropicCCHKnownVectorBody, `"model":"model-a"`, `"model":"model-b"`, 1), want: "7ee87"},
		{name: "max tokens ignored", body: strings.Replace(anthropicCCHKnownVectorBody, `"max_tokens":1`, `"max_tokens":2`, 1), want: "7ee87"},
		{name: "message changes hash", body: strings.Replace(anthropicCCHKnownVectorBody, `"text":"x"`, `"text":"y"`, 1), want: "b9cc8"},
		{name: "metadata changes hash", body: strings.Replace(anthropicCCHKnownVectorBody, `"user_id":"meta-x"`, `"user_id":"meta-y"`, 1), want: "7a89d"},
		{name: "stream changes hash", body: strings.Replace(anthropicCCHKnownVectorBody, `"stream":true`, `"stream":false`, 1), want: "60400"},
		{name: "nested dispatch member ignored", body: strings.Replace(anthropicCCHKnownVectorBody, `"metadata":{"user_id":"meta-x"}`, `"metadata":{"user_id":"meta-x","max_tokens":2}`, 1), want: "7ee87"},
		{name: "fallbacks ignored", body: strings.Replace(anthropicCCHKnownVectorBody, `"stream":true}`, `"stream":true,"fallbacks":[{"model":"fallback-a"}]}`, 1), want: "7ee87"},
		{
			name: "field order remains significant",
			body: `{"stream":true,"output_config":{"effort":"high"},"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"thinking":{"type":"adaptive","display":"omitted"},"max_tokens":1,"metadata":{"user_id":"meta-x"},"tools":[],"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.220.test; cc_entrypoint=sdk-cli; cch=00000;"},{"type":"text","text":"system-x"}],"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}],"model":"model-a"}`,
			want: "e5b6c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			signed, err := signAnthropicCCH([]byte(tt.body))
			require.NoError(t, err)
			require.Equal(t, tt.want, anthropicCCHFromBody(t, signed))
		})
	}
}

func TestSignAnthropicCCHOnlyChangesSignatureDigits(t *testing.T) {
	t.Parallel()

	body := []byte(strings.Replace(anthropicCCHKnownVectorBody, `"text":"x"`, `"text":"literal cch=00000;"`, 1))
	signed, err := signAnthropicCCH(body)
	require.NoError(t, err)
	require.Equal(t, "literal cch=00000;", gjson.GetBytes(signed, "messages.0.content.0.text").String())

	offset, ok := anthropicCCHDigitsOffset(signed)
	require.True(t, ok)
	unsigned := bytes.Clone(signed)
	copy(unsigned[offset:offset+anthropicCCHLength], anthropicCCHZero)
	require.Equal(t, body, unsigned)
}

func TestNormalizeAnthropicCCHInputPreservesRawJSON(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		`{"model":"claude","keep":1}`:                             `{"model":"","keep":1}`,
		`{"max_tokens":1,"keep":2}`:                               `{"keep":2}`,
		`{"keep":1,"fallbacks":[{"model":"x"}],"tail":2}`:         `{"keep":1,"tail":2}`,
		`{"keep":1,"fallback_credit_token":"secret"}`:             `{"keep":1}`,
		`{"outer":{"model":"x","max_tokens":1,"keep":"y"}}`:       `{"outer":{"model":"","keep":"y"}}`,
		`{"text":"literal \"model\":\"x\" and \"max_tokens\":1"}`: `{"text":"literal \"model\":\"x\" and \"max_tokens\":1"}`,
	}
	for body, want := range tests {
		normalized, err := normalizeAnthropicCCHInput([]byte(body))
		require.NoError(t, err)
		require.Equal(t, want, string(normalized))
	}
}

func TestFinalizeAnthropicCCHAddsBillingAndPreservesSystem(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"claude-opus-5","system":"keep this system text","messages":[{"role":"user","content":"hello"}],"max_tokens":128,"stream":false}`)
	signed, err := finalizeAnthropicCCH(body, "claude-cli/2.1.222 (external, remote)")
	require.NoError(t, err)
	require.Equal(t, "keep this system text", gjson.GetBytes(signed, "system.1.text").String())
	billing := gjson.GetBytes(signed, "system.0.text").String()
	require.Contains(t, billing, "cc_version=2.1.222.")
	require.Contains(t, billing, "cc_entrypoint=remote;")
	require.Regexp(t, `cch=[0-9a-f]{5};`, billing)

	resigned, err := signAnthropicCCH(signed)
	require.NoError(t, err)
	require.Equal(t, signed, resigned, "final-body signing must be idempotent")
}

func TestShouldFinalizeAnthropicCCHScope(t *testing.T) {
	t.Parallel()

	oauth := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	setup := &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}
	apikey := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	mustURL := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return u
	}

	require.True(t, shouldFinalizeAnthropicCCH(oauth, "oauth", mustURL(claudeAPIURL)))
	require.True(t, shouldFinalizeAnthropicCCH(setup, "oauth", mustURL(claudeAPICountTokensURL)))
	require.False(t, shouldFinalizeAnthropicCCH(apikey, "apikey", mustURL(claudeAPIURL)))
	require.False(t, shouldFinalizeAnthropicCCH(oauth, "oauth", mustURL("https://relay.example/v1/messages?beta=true")))
	require.False(t, shouldFinalizeAnthropicCCH(oauth, "oauth", mustURL("https://api.anthropic.com.example/v1/messages")))
}

func TestEnsureClaudeOAuthCredentialBetas(t *testing.T) {
	t.Parallel()

	incoming := "interleaved-thinking-2025-05-14,context-management-2025-06-27"
	got := ensureClaudeOAuthCredentialBetas(incoming)
	require.Equal(t,
		"claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,extended-cache-ttl-2025-04-11",
		got,
	)
	require.Equal(t, got, ensureClaudeOAuthCredentialBetas(got))
}

func TestEnsureClaudeOAuthCountTokensCredentialBetas(t *testing.T) {
	incoming := "interleaved-thinking-2025-05-14,context-management-2025-06-27,token-counting-2024-11-01"
	got := ensureClaudeOAuthCountTokensCredentialBetas(incoming)

	require.True(t, containsBetaToken(got, claude.BetaClaudeCode))
	require.True(t, containsBetaToken(got, claude.BetaOAuth))
	require.True(t, containsBetaToken(got, claude.BetaTokenCounting))
	require.False(t, containsBetaToken(got, claude.BetaExtendedCacheTTL))
	require.Equal(t, got, ensureClaudeOAuthCountTokensCredentialBetas(got))
}

func TestBuildUpstreamRequestSignsNativeClassifierFinalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request, _ = http.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.222 (external, remote)")
	c.Request.Header.Set("X-App", "cli")
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07")

	ctx := SetClaudeCodeClient(context.Background(), true)
	ctx = SetClaudeCodeUserAgent(ctx, c.Request.Header.Get("User-Agent"))
	account := &Account{ID: 501, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	body := claudeCodeAgentClassifierBodyForTest()
	svc := &GatewayService{cfg: &config.Config{}}
	req, wireBody, err := svc.buildUpstreamRequest(ctx, c, account, body, "oauth-token", "oauth", "claude-opus-5", false, false)
	require.NoError(t, err)

	require.Equal(t, "https://api.anthropic.com/v1/messages?beta=true", req.URL.String())
	require.Equal(t, claude.PlainCLICanonicalUserAgent, req.Header.Get("User-Agent"))
	require.Equal(t, wireBody, readCCHUpstreamBodyForTest(t, req))
	require.NotEqual(t, anthropicCCHZero, anthropicCCHFromBody(t, wireBody))
	require.True(t, IsClaudeCodeAgentClassifierRequest(stripAnthropicBillingBlockForTest(t, wireBody)))
	require.Equal(t, claudeCodeAgentClassifierSystemPrefix+" Classify the state.", gjson.GetBytes(wireBody, "system.1.text").String())

	beta := getHeaderRaw(req.Header, "Anthropic-Beta")
	require.True(t, containsBetaToken(beta, claude.BetaClaudeCode))
	require.True(t, containsBetaToken(beta, claude.BetaOAuth))
	require.True(t, containsBetaToken(beta, claude.BetaExtendedCacheTTL))

	resigned, err := signAnthropicCCH(wireBody)
	require.NoError(t, err)
	require.Equal(t, wireBody, resigned, "CCH must cover the returned wire body")
}

func TestBuildUpstreamRequestSignsStreamingFinalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request, _ = http.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.222 (external, cli)")
	c.Request.Header.Set("X-App", "cli")
	c.Request.Header.Set("Anthropic-Beta", claude.DefaultBetaHeader)

	ctx := SetClaudeCodeClient(context.Background(), true)
	ctx = SetClaudeCodeUserAgent(ctx, c.Request.Header.Get("User-Agent"))
	account := &Account{ID: 502, Platform: PlatformAnthropic, Type: AccountTypeSetupToken, Status: StatusActive, Schedulable: true}
	body := []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hello"}],"system":[{"type":"text","text":"You are Claude Code."}],"metadata":{"user_id":"user_test_account__session_test"},"max_tokens":256,"stream":true}`)
	req, wireBody, err := (&GatewayService{cfg: &config.Config{}}).buildUpstreamRequest(
		ctx, c, account, body, "setup-token", "oauth", "claude-opus-5", true, false,
	)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(wireBody, "stream").Bool())
	require.NotEqual(t, anthropicCCHZero, anthropicCCHFromBody(t, wireBody))
	require.Equal(t, wireBody, readCCHUpstreamBodyForTest(t, req))

	resigned, err := signAnthropicCCH(wireBody)
	require.NoError(t, err)
	require.Equal(t, wireBody, resigned)
}

func TestBuildCountTokensRequestSignsAfterSanitization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request, _ = http.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.222 (external, remote)")
	c.Request.Header.Set("X-App", "cli")
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14,context-management-2025-06-27,token-counting-2024-11-01")

	ctx := SetClaudeCodeClient(context.Background(), true)
	ctx = SetClaudeCodeUserAgent(ctx, c.Request.Header.Get("User-Agent"))
	account := &Account{ID: 503, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	body := []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hello"}],"system":[{"type":"text","text":"Classifier system"}],"metadata":{"user_id":"user_test_account__session_test"},"max_tokens":256,"temperature":0,"stream":false}`)
	req, wireBody, err := (&GatewayService{cfg: &config.Config{}}).buildCountTokensRequest(
		ctx, c, account, body, "oauth-token", "oauth", "claude-opus-5", false,
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wireBody, "stream").Exists())
	require.False(t, gjson.GetBytes(wireBody, "temperature").Exists())
	require.NotEqual(t, anthropicCCHZero, anthropicCCHFromBody(t, wireBody))
	require.Equal(t, wireBody, readCCHUpstreamBodyForTest(t, req))

	beta := getHeaderRaw(req.Header, "Anthropic-Beta")
	require.True(t, containsBetaToken(beta, claude.BetaClaudeCode))
	require.True(t, containsBetaToken(beta, claude.BetaOAuth))
	require.True(t, containsBetaToken(beta, claude.BetaTokenCounting))
	require.False(t, containsBetaToken(beta, claude.BetaExtendedCacheTTL))

	resigned, err := signAnthropicCCH(wireBody)
	require.NoError(t, err)
	require.Equal(t, wireBody, resigned, "count_tokens CCH must cover the sanitized wire body")
}

func BenchmarkFinalizeAnthropicCCH256KB(b *testing.B) {
	message := strings.Repeat("x", 256*1024)
	body := []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":` +
		strconv.Quote(message) + `}],"system":[{"type":"text","text":"You are Claude Code."}],"max_tokens":1024,"stream":true}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for range b.N {
		if _, err := finalizeAnthropicCCH(body, claude.PlainCLICanonicalUserAgent); err != nil {
			b.Fatal(err)
		}
	}
}

func anthropicCCHFromBody(t *testing.T, body []byte) string {
	t.Helper()
	offset, ok := anthropicCCHDigitsOffset(body)
	require.True(t, ok, "billing CCH missing from body: %s", body)
	return string(body[offset : offset+anthropicCCHLength])
}

func readCCHUpstreamBodyForTest(t *testing.T, req *http.Request) []byte {
	t.Helper()
	require.NotNil(t, req.Body)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	return body
}

func stripAnthropicBillingBlockForTest(t *testing.T, body []byte) []byte {
	t.Helper()
	require.GreaterOrEqual(t, len(gjson.GetBytes(body, "system").Array()), 2)
	out, err := sjson.DeleteBytes(body, "system.0")
	require.NoError(t, err)
	return out
}
