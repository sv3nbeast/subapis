//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
)

// swapMonitorHTTPClient 临时替换 monitorHTTPClient 为不带 SSRF 校验的普通 client，
// 让 httptest (127.0.0.1) 能连通。测试结束后恢复。
func swapMonitorHTTPClient(t *testing.T) {
	t.Helper()
	orig := monitorHTTPClient
	monitorHTTPClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() { monitorHTTPClient = orig })
}

// captureHandler 把每次收到的请求 body 和 headers 存起来，测试断言用。
type captureHandler struct {
	lastBody        map[string]any
	lastHeaders     http.Header
	respondText     string // 写到 Anthropic content[0].text 里（校验用）
	responseContent []map[string]any
	status          int
}

func (h *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.lastHeaders = r.Header.Clone()
	defer func() { _ = r.Body.Close() }()
	var parsed map[string]any
	_ = json.NewDecoder(r.Body).Decode(&parsed)
	h.lastBody = parsed

	if h.status == 0 {
		h.status = 200
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(h.status)
	content := h.responseContent
	if len(content) == 0 {
		content = []map[string]any{
			{"type": "text", "text": h.respondText},
		}
	}
	// 构造 Anthropic 格式的响应：content[0].text = h.respondText
	_ = json.NewEncoder(w).Encode(map[string]any{
		"content": content,
	})
}

func setupFakeAnthropic(t *testing.T, handler *captureHandler) string {
	t.Helper()
	swapMonitorHTTPClient(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRunCheckForModel_OffMode_PreservesDefaultBody(t *testing.T) {
	h := &captureHandler{respondText: "the answer is 42"}
	endpoint := setupFakeAnthropic(t, h)

	// 跑一次 off 模式（opts=nil），确认默认 body 行为未变
	_ = runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", nil)

	if h.lastBody["model"] != "claude-x" {
		t.Errorf("default body should contain model=claude-x, got %v", h.lastBody["model"])
	}
	if _, ok := h.lastBody["messages"]; !ok {
		t.Error("default body should contain messages")
	}
	if h.lastHeaders.Get("x-api-key") != "sk-fake" {
		t.Errorf("expected adapter's x-api-key header, got %q", h.lastHeaders.Get("x-api-key"))
	}
}

func TestRunCheckForModel_AnthropicDefaultRequestPassesClaudeCodeValidation(t *testing.T) {
	h := &captureHandler{respondText: allChallengeAnswers()}
	endpoint := setupFakeAnthropic(t, h)

	res := runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", nil)

	require.Equal(t, MonitorStatusOperational, res.Status, res.Message)
	require.Equal(t, claude.DefaultHeaders["User-Agent"], h.lastHeaders.Get("User-Agent"))
	require.Equal(t, claude.DefaultHeaders["X-App"], h.lastHeaders.Get("X-App"))
	require.Equal(t, claude.DefaultBetaHeader, h.lastHeaders.Get("anthropic-beta"))
	require.Equal(t, monitorAnthropicAPIVersion, h.lastHeaders.Get("anthropic-version"))

	system, ok := h.lastBody["system"].([]any)
	require.True(t, ok)
	require.Len(t, system, 1)
	systemBlock, ok := system[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, claudeCodeSystemPrompt, systemBlock["text"])
	require.Equal(t, true, h.lastBody["stream"])

	metadata, ok := h.lastBody["metadata"].(map[string]any)
	require.True(t, ok)
	userID, ok := metadata["user_id"].(string)
	require.True(t, ok)
	require.NotNil(t, ParseMetadataUserID(userID))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header = h.lastHeaders.Clone()
	require.True(t, NewClaudeCodeValidator().Validate(req, h.lastBody))
}

func TestRunCheckForModel_AnthropicNormalizesMonitorModelAlias(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "opus 4.6 thinking alias", model: "claude-opus-4-6-thinking", want: "claude-opus-4-6"},
		{name: "opus 4.6 dotted alias", model: "claude-opus-4.6", want: "claude-opus-4-6"},
		{name: "opus 4.6 dotted thinking alias", model: "claude-opus-4.6-thinking", want: "claude-opus-4-6"},
		{name: "thinking alias", model: "claude-opus-4-7-thinking", want: "claude-opus-4-7"},
		{name: "dotted alias", model: "claude-opus-4.7", want: "claude-opus-4-7"},
		{name: "dotted thinking alias", model: "claude-opus-4.7-thinking", want: "claude-opus-4-7"},
		{name: "official id passthrough", model: "claude-opus-4-7", want: "claude-opus-4-7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &captureHandler{respondText: allChallengeAnswers()}
			endpoint := setupFakeAnthropic(t, h)

			res := runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", tt.model, nil)

			if h.lastBody["model"] != tt.want {
				t.Fatalf("monitor body model = %v, want %s", h.lastBody["model"], tt.want)
			}
			if res.Model != tt.model {
				t.Fatalf("result model = %q, want original model %q", res.Model, tt.model)
			}
			if res.Status != MonitorStatusOperational {
				t.Fatalf("expected operational result, got status=%s message=%q", res.Status, res.Message)
			}
		})
	}
}

func TestRunCheckForModel_MergeMode_UserFieldsWinButDenyListProtects(t *testing.T) {
	h := &captureHandler{respondText: "the answer is 42"}
	endpoint := setupFakeAnthropic(t, h)

	opts := &CheckOptions{
		BodyOverrideMode: MonitorBodyOverrideModeMerge,
		BodyOverride: map[string]any{
			"system":     "You are Claude Code...",
			"max_tokens": float64(999),   // 应该覆盖默认 50
			"model":      "hacked-model", // 应该被黑名单挡住，保留原 model
			"messages":   []any{},        // 同上，被挡
			"stream":     false,          // 同上，被挡，避免内部网关拒绝同步 /v1/messages
		},
		ExtraHeaders: map[string]string{
			"User-Agent":     "claude-cli/1.0",
			"Content-Length": "999", // 黑名单
			"x-custom":       "ok",
		},
	}
	_ = runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", opts)

	if h.lastBody["system"] != "You are Claude Code..." {
		t.Errorf("merge mode should inject system, got %v", h.lastBody["system"])
	}
	// max_tokens 覆盖生效
	if mt, ok := h.lastBody["max_tokens"].(float64); !ok || mt != 999 {
		t.Errorf("merge mode should override max_tokens to 999, got %v", h.lastBody["max_tokens"])
	}
	// model 在黑名单 — 应该保留默认值
	if h.lastBody["model"] != "claude-x" {
		t.Errorf("model should be protected by deny list, got %v", h.lastBody["model"])
	}
	// messages 在黑名单 — 应该保留默认值（非空）
	msgs, _ := h.lastBody["messages"].([]any)
	if len(msgs) == 0 {
		t.Error("messages should be protected by deny list (kept default, non-empty)")
	}
	if h.lastBody["stream"] != true {
		t.Errorf("stream should be protected by deny list (kept true), got %v", h.lastBody["stream"])
	}
	// Anthropic User-Agent 由统一上游 UA 控制，模板不能覆盖。
	if h.lastHeaders.Get("User-Agent") != claude.DefaultHeaders["User-Agent"] {
		t.Errorf("extra User-Agent should not override Anthropic upstream UA, got %q", h.lastHeaders.Get("User-Agent"))
	}
	if h.lastHeaders.Get("x-custom") != "ok" {
		t.Errorf("extra custom header should be present, got %q", h.lastHeaders.Get("x-custom"))
	}
	// Content-Length 黑名单：会被 net/http 自动重算，但不应由用户的 "999" 决定。
	// 我们无法直接断言丢弃（http.Client 总会填上），只断言请求成功即可。
}

func TestRunCheckForModel_ReplaceMode_FullBodyUsedAndChallengeSkipped(t *testing.T) {
	// replace 模式下我们的 body 完全自定义，challenge 数学题不会出现在请求里，
	// 上游也不会回正确答案 — 但只要 2xx + 响应文本非空，就算 operational
	h := &captureHandler{respondText: "any non-empty text"}
	endpoint := setupFakeAnthropic(t, h)

	userBody := map[string]any{
		"model":      "user-forced-model",
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
		"max_tokens": float64(10),
		"system":     "You are someone else",
	}
	opts := &CheckOptions{
		BodyOverrideMode: MonitorBodyOverrideModeReplace,
		BodyOverride:     userBody,
	}
	res := runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", opts)

	// 请求 body = 用户提供的原样
	if h.lastBody["model"] != "user-forced-model" {
		t.Errorf("replace mode should use user's model, got %v", h.lastBody["model"])
	}
	if h.lastBody["system"] != "You are someone else" {
		t.Errorf("replace mode should use user's system, got %v", h.lastBody["system"])
	}
	// challenge 虽然没命中，但由于 replace 模式跳过 challenge 校验 + 响应非空 → operational
	if res.Status != MonitorStatusOperational {
		t.Errorf("replace mode with 2xx + non-empty text should be operational, got status=%s message=%q",
			res.Status, res.Message)
	}
}

func TestRunCheckForModel_ReplaceMode_EmptyResponseIsFailed(t *testing.T) {
	h := &captureHandler{respondText: ""} // 上游 200 但 content[0].text 为空
	endpoint := setupFakeAnthropic(t, h)

	opts := &CheckOptions{
		BodyOverrideMode: MonitorBodyOverrideModeReplace,
		BodyOverride:     map[string]any{"model": "x", "messages": []any{}},
	}
	res := runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", opts)

	if res.Status != MonitorStatusFailed {
		t.Errorf("replace mode with empty text should be failed, got status=%s", res.Status)
	}
	if !strings.Contains(res.Message, "replace-mode") {
		t.Errorf("failure message should hint replace-mode, got %q", res.Message)
	}
}

func TestRunCheckForModel_AnthropicSkipsThinkingBlocksWhenExtractingText(t *testing.T) {
	h := &captureHandler{
		responseContent: []map[string]any{
			{"type": "thinking", "thinking": "private chain of thought"},
			{"type": "text", "text": allChallengeAnswers()},
		},
	}
	endpoint := setupFakeAnthropic(t, h)

	res := runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", nil)

	if res.Status != MonitorStatusOperational {
		t.Fatalf("thinking block before text should still extract text and pass challenge, got status=%s message=%q",
			res.Status, res.Message)
	}
}

func TestExtractAnthropicMonitorText_PrefersTextBlock(t *testing.T) {
	body := []byte(`{"content":[{"type":"thinking","thinking":"42"},{"type":"text","text":"answer 7"}]}`)

	if got := extractAnthropicMonitorText(body); got != "answer 7" {
		t.Fatalf("expected text block, got %q", got)
	}
}

func TestRunCheckForModel_AnthropicStreamingResponsePassesChallenge(t *testing.T) {
	swapMonitorHTTPClient(t)
	var lastBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&lastBody))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + allChallengeAnswers() + `"}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	res := runCheckForModel(context.Background(), MonitorProviderAnthropic, srv.URL, "sk-fake", "claude-x", nil)

	require.Equal(t, MonitorStatusOperational, res.Status, res.Message)
	require.Equal(t, true, lastBody["stream"])
}

func TestExtractAnthropicMonitorText_ReadsSSETextDeltas(t *testing.T) {
	body := []byte(strings.Join([]string{
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"answer "}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"42"}}`,
		``,
	}, "\n"))

	require.Equal(t, "answer 42", extractAnthropicMonitorText(body))
}

func allChallengeAnswers() string {
	var b strings.Builder
	for i := 0; i <= monitorChallengeMax*2; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.Itoa(i))
	}
	return b.String()
}
