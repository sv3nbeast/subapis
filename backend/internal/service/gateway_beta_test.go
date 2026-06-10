package service

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMergeAnthropicBeta(t *testing.T) {
	got := mergeAnthropicBeta(
		[]string{"oauth-2025-04-20", "interleaved-thinking-2025-05-14"},
		"foo, oauth-2025-04-20,bar, foo",
	)
	require.Equal(t, "oauth-2025-04-20,interleaved-thinking-2025-05-14,foo,bar", got)
}

func TestMergeAnthropicBeta_EmptyIncoming(t *testing.T) {
	got := mergeAnthropicBeta(
		[]string{"oauth-2025-04-20", "interleaved-thinking-2025-05-14"},
		"",
	)
	require.Equal(t, "oauth-2025-04-20,interleaved-thinking-2025-05-14", got)
}

func TestStripBetaTokens(t *testing.T) {
	tests := []struct {
		name   string
		header string
		tokens []string
		want   string
	}{
		{
			name:   "single token in middle",
			header: "oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14",
			tokens: []string{"context-1m-2025-08-07"},
			want:   "oauth-2025-04-20,interleaved-thinking-2025-05-14",
		},
		{
			name:   "single token at start",
			header: "context-1m-2025-08-07,oauth-2025-04-20,interleaved-thinking-2025-05-14",
			tokens: []string{"context-1m-2025-08-07"},
			want:   "oauth-2025-04-20,interleaved-thinking-2025-05-14",
		},
		{
			name:   "single token at end",
			header: "oauth-2025-04-20,interleaved-thinking-2025-05-14,context-1m-2025-08-07",
			tokens: []string{"context-1m-2025-08-07"},
			want:   "oauth-2025-04-20,interleaved-thinking-2025-05-14",
		},
		{
			name:   "token not present",
			header: "oauth-2025-04-20,interleaved-thinking-2025-05-14",
			tokens: []string{"context-1m-2025-08-07"},
			want:   "oauth-2025-04-20,interleaved-thinking-2025-05-14",
		},
		{
			name:   "empty header",
			header: "",
			tokens: []string{"context-1m-2025-08-07"},
			want:   "",
		},
		{
			name:   "with spaces",
			header: "oauth-2025-04-20, context-1m-2025-08-07 , interleaved-thinking-2025-05-14",
			tokens: []string{"context-1m-2025-08-07"},
			want:   "oauth-2025-04-20,interleaved-thinking-2025-05-14",
		},
		{
			name:   "only token",
			header: "context-1m-2025-08-07",
			tokens: []string{"context-1m-2025-08-07"},
			want:   "",
		},
		{
			name:   "nil tokens",
			header: "oauth-2025-04-20,interleaved-thinking-2025-05-14",
			tokens: nil,
			want:   "oauth-2025-04-20,interleaved-thinking-2025-05-14",
		},
		{
			name:   "multiple tokens removed",
			header: "oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,fast-mode-2026-02-01",
			tokens: []string{"context-1m-2025-08-07", "fast-mode-2026-02-01"},
			want:   "oauth-2025-04-20,interleaved-thinking-2025-05-14",
		},
		{
			name:   "DroppedBetas is empty (filtering moved to configurable beta policy)",
			header: "oauth-2025-04-20,context-1m-2025-08-07,fast-mode-2026-02-01,interleaved-thinking-2025-05-14",
			tokens: claude.DroppedBetas,
			want:   "oauth-2025-04-20,context-1m-2025-08-07,fast-mode-2026-02-01,interleaved-thinking-2025-05-14",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripBetaTokens(tt.header, tt.tokens)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestMergeAnthropicBetaDropping_Context1M(t *testing.T) {
	required := []string{"oauth-2025-04-20", "interleaved-thinking-2025-05-14"}
	incoming := "context-1m-2025-08-07,foo-beta,oauth-2025-04-20"
	drop := map[string]struct{}{"context-1m-2025-08-07": {}}

	got := mergeAnthropicBetaDropping(required, incoming, drop)
	require.Equal(t, "oauth-2025-04-20,interleaved-thinking-2025-05-14,foo-beta", got)
	require.NotContains(t, got, "context-1m-2025-08-07")
}

func TestMergeAnthropicBetaDropping_DroppedBetas(t *testing.T) {
	required := []string{"oauth-2025-04-20", "interleaved-thinking-2025-05-14"}
	incoming := "context-1m-2025-08-07,fast-mode-2026-02-01,foo-beta,oauth-2025-04-20"
	// DroppedBetas is now empty — filtering moved to configurable beta policy.
	// Without a policy filter set, nothing gets dropped from the static set.
	drop := droppedBetaSet()

	got := mergeAnthropicBetaDropping(required, incoming, drop)
	require.Equal(t, "oauth-2025-04-20,interleaved-thinking-2025-05-14,context-1m-2025-08-07,fast-mode-2026-02-01,foo-beta", got)
	require.Contains(t, got, "context-1m-2025-08-07")
	require.Contains(t, got, "fast-mode-2026-02-01")
}

func TestFullClaudeCodeMimicryBetas_DoesNotDefaultRedactThinking(t *testing.T) {
	required := claude.FullClaudeCodeMimicryBetas()

	require.NotContains(t, required, claude.BetaRedactThinking)
	require.Contains(t, required, claude.BetaClaudeCode)
	require.Contains(t, required, claude.BetaOAuth)
	require.Contains(t, required, claude.BetaInterleavedThinking)
	require.Contains(t, required, claude.BetaContextManagement)
	require.Contains(t, required, claude.BetaPromptCachingScope)
	require.Contains(t, required, claude.BetaMidConversationSystem)
	require.Contains(t, required, claude.BetaAdvancedToolUse)
	require.Contains(t, required, claude.BetaEffort)
	require.Contains(t, required, claude.BetaExtendedCacheTTL)
}

func TestMergeAnthropicBetaDropping_PreservesIncomingRedactThinking(t *testing.T) {
	required := claude.FullClaudeCodeMimicryBetas()
	incoming := claude.BetaRedactThinking

	got := mergeAnthropicBetaDropping(required, incoming, droppedBetaSet())

	require.Contains(t, got, claude.BetaRedactThinking)
}

func TestDefaultClaudeCodeBetaHeadersMatchCapturedCLI2165(t *testing.T) {
	require.Equal(t,
		"claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,advanced-tool-use-2025-11-20,effort-2025-11-24,extended-cache-ttl-2025-04-11",
		claude.DefaultBetaHeader,
	)
	require.Equal(t,
		"oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24,structured-outputs-2025-12-15",
		claude.HaikuBetaHeader,
	)
	require.Contains(t, claude.DefaultBetaHeader, claude.BetaExtendedCacheTTL)
	require.NotContains(t, claude.APIKeyBetaHeader, claude.BetaOAuth)
}

func TestApplyClaudeCodeMimicHeaders_OfficialCLI2165Fingerprint(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages?beta=true", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "curl/8")
	req.Header.Set("X-Stainless-Package-Version", "old")
	req.Header.Set("Accept", "text/event-stream")

	applyClaudeCodeMimicHeaders(req, true)

	require.Equal(t, "claude-cli/2.1.165 (external, sdk-cli)", getHeaderRaw(req.Header, "User-Agent"))
	require.Equal(t, "0.94.0", getHeaderRaw(req.Header, "X-Stainless-Package-Version"))
	require.Equal(t, "MacOS", getHeaderRaw(req.Header, "X-Stainless-OS"))
	require.Equal(t, "arm64", getHeaderRaw(req.Header, "X-Stainless-Arch"))
	require.Equal(t, "node", getHeaderRaw(req.Header, "X-Stainless-Runtime"))
	require.Equal(t, "v24.3.0", getHeaderRaw(req.Header, "X-Stainless-Runtime-Version"))
	require.Equal(t, "600", getHeaderRaw(req.Header, "X-Stainless-Timeout"))
	require.Equal(t, "cli", getHeaderRaw(req.Header, "X-App"))
	require.Equal(t, "true", getHeaderRaw(req.Header, "Anthropic-Dangerous-Direct-Browser-Access"))
	require.Equal(t, "application/json", getHeaderRaw(req.Header, "Accept"))
	require.Equal(t, "stream", getHeaderRaw(req.Header, "x-stainless-helper-method"))
	require.NotEmpty(t, getHeaderRaw(req.Header, "x-client-request-id"))
}

func TestSyncClaudeCodeSessionHeaderFromBody_AlwaysMatchesMetadataSession(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages?beta=true", nil)
	require.NoError(t, err)

	body := []byte(`{
		"metadata": {
			"user_id": "{\"device_id\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"account_uuid\":\"550e8400-e29b-41d4-a716-446655440000\",\"session_id\":\"123e4567-e89b-12d3-a456-426614174000\"}"
		}
	}`)

	syncClaudeCodeSessionHeaderFromBody(req, body)

	require.Equal(t, "123e4567-e89b-12d3-a456-426614174000", getHeaderRaw(req.Header, "X-Claude-Code-Session-Id"))
}

func TestBuildUpstreamRequest_MimicClaudeCodeSignsCCHWithoutGlobalSetting(t *testing.T) {
	req, err := (&GatewayService{}).buildUpstreamRequest(
		context.Background(),
		nil,
		&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		[]byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.165.abc; cc_entrypoint=sdk-cli; cch=00000;"}],"messages":[{"role":"user","content":"hello"}]}`),
		"oauth-token",
		"oauth",
		"claude-sonnet-4-6",
		true,
		true,
	)
	require.NoError(t, err)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)

	require.NotContains(t, string(body), "cch=00000")
	require.Regexp(t, `cch=[0-9a-f]{5};`, string(body))
}

func TestBuildUpstreamRequest_MimicClaudeCodeNormalizesForcedToolChoice(t *testing.T) {
	req, err := (&GatewayService{}).buildUpstreamRequest(
		context.Background(),
		nil,
		&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		[]byte(`{"model":"claude-sonnet-4-6","messages":[],"tool_choice":{"type":"any"},"thinking":{"type":"adaptive","budget_tokens":32000},"output_config":{"effort":"max"},"tools":[{"name":"Bash","input_schema":{},"cache_control":{"type":"ephemeral","ttl":"5m"}}],"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`),
		"oauth-token",
		"oauth",
		"claude-sonnet-4-6",
		true,
		true,
	)
	require.NoError(t, err)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)

	require.False(t, gjson.GetBytes(body, "thinking").Exists())
	require.False(t, gjson.GetBytes(body, "output_config").Exists())
	require.Equal(t, "any", gjson.GetBytes(body, "tool_choice.type").String())
	require.False(t, gjson.GetBytes(body, "system.0.cache_control.ttl").Exists())
	require.Equal(t, "5m", gjson.GetBytes(body, "tools.0.cache_control.ttl").String())
}

func TestBuildUpstreamRequest_RealClaudeCodeDoesNotNormalizeForcedToolChoice(t *testing.T) {
	req, err := (&GatewayService{}).buildUpstreamRequest(
		context.Background(),
		nil,
		&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		[]byte(`{"model":"claude-sonnet-4-6","messages":[],"tool_choice":{"type":"any"},"thinking":{"type":"adaptive","budget_tokens":32000},"output_config":{"effort":"max"},"tools":[{"name":"Bash","input_schema":{},"cache_control":{"type":"ephemeral","ttl":"5m"}}],"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`),
		"oauth-token",
		"oauth",
		"claude-sonnet-4-6",
		true,
		false,
	)
	require.NoError(t, err)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)

	require.Equal(t, "adaptive", gjson.GetBytes(body, "thinking.type").String())
	require.Equal(t, "max", gjson.GetBytes(body, "output_config.effort").String())
	require.Equal(t, "1h", gjson.GetBytes(body, "system.0.cache_control.ttl").String())
}

func TestDroppedBetaSet(t *testing.T) {
	// Base set contains DroppedBetas (now empty — filtering moved to configurable beta policy)
	base := droppedBetaSet()
	require.Len(t, base, len(claude.DroppedBetas))

	// With extra tokens
	extended := droppedBetaSet(claude.BetaClaudeCode)
	require.Contains(t, extended, claude.BetaClaudeCode)
	require.Len(t, extended, len(claude.DroppedBetas)+1)
}

func TestBuildBetaTokenSet(t *testing.T) {
	got := buildBetaTokenSet([]string{"foo", "", "bar", "foo"})
	require.Len(t, got, 2)
	require.Contains(t, got, "foo")
	require.Contains(t, got, "bar")
	require.NotContains(t, got, "")

	empty := buildBetaTokenSet(nil)
	require.Empty(t, empty)
}

func TestContainsBetaToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		token  string
		want   bool
	}{
		{"present in middle", "oauth-2025-04-20,fast-mode-2026-02-01,interleaved-thinking-2025-05-14", "fast-mode-2026-02-01", true},
		{"present at start", "fast-mode-2026-02-01,oauth-2025-04-20", "fast-mode-2026-02-01", true},
		{"present at end", "oauth-2025-04-20,fast-mode-2026-02-01", "fast-mode-2026-02-01", true},
		{"only token", "fast-mode-2026-02-01", "fast-mode-2026-02-01", true},
		{"not present", "oauth-2025-04-20,interleaved-thinking-2025-05-14", "fast-mode-2026-02-01", false},
		{"with spaces", "oauth-2025-04-20, fast-mode-2026-02-01 , interleaved-thinking-2025-05-14", "fast-mode-2026-02-01", true},
		{"empty header", "", "fast-mode-2026-02-01", false},
		{"empty token", "fast-mode-2026-02-01", "", false},
		{"partial match", "fast-mode-2026-02-01-extra", "fast-mode-2026-02-01", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsBetaToken(tt.header, tt.token)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestStripBetaTokensWithSet_EmptyDropSet(t *testing.T) {
	header := "oauth-2025-04-20,interleaved-thinking-2025-05-14"
	got := stripBetaTokensWithSet(header, map[string]struct{}{})
	require.Equal(t, header, got)
}

func TestIsCountTokensUnsupported404(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{
			name:       "exact endpoint not found",
			statusCode: 404,
			body:       `{"error":{"message":"Not found: /v1/messages/count_tokens","type":"not_found_error"}}`,
			want:       true,
		},
		{
			name:       "contains count_tokens and not found",
			statusCode: 404,
			body:       `{"error":{"message":"count_tokens route not found","type":"not_found_error"}}`,
			want:       true,
		},
		{
			name:       "generic 404",
			statusCode: 404,
			body:       `{"error":{"message":"resource not found","type":"not_found_error"}}`,
			want:       false,
		},
		{
			name:       "404 with empty error message",
			statusCode: 404,
			body:       `{"error":{"message":"","type":"not_found_error"}}`,
			want:       false,
		},
		{
			name:       "non-404 status",
			statusCode: 400,
			body:       `{"error":{"message":"Not found: /v1/messages/count_tokens","type":"invalid_request_error"}}`,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCountTokensUnsupported404(tt.statusCode, []byte(tt.body))
			require.Equal(t, tt.want, got)
		})
	}
}
