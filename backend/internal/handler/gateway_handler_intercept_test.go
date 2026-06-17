package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDetectInterceptType_MaxTokensOneHaikuRequiresClaudeCodeClient(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)

	notClaudeCode := detectInterceptType(body, "claude-haiku-4-5", 1, false, false)
	require.Equal(t, InterceptTypeNone, notClaudeCode)

	isClaudeCode := detectInterceptType(body, "claude-haiku-4-5", 1, false, true)
	require.Equal(t, InterceptTypeMaxTokensOneHaiku, isClaudeCode)
}

// TestDetectInterceptType_ConnectionProbeAnyModel 验证任意模型的 Claude Code
// 探测请求（max_tokens=1, !stream）都会被拦截，覆盖 Claude Code Desktop
// 使用非 haiku 模型进行 Test Connection 的场景。
func TestDetectInterceptType_ConnectionProbeAnyModel(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	// Claude Code Desktop 使用 opus 模型探测 → 应被拦截
	opusProbe := detectInterceptType(body, "claude-opus-4-8", 1, false, true)
	require.Equal(t, InterceptTypeMaxTokensOneHaiku, opusProbe)

	// Claude Code Desktop 使用 sonnet 模型探测 → 应被拦截
	sonnetProbe := detectInterceptType(body, "claude-sonnet-4-5", 1, false, true)
	require.Equal(t, InterceptTypeMaxTokensOneHaiku, sonnetProbe)

	// 非 Claude Code 客户端的 max_tokens=1 请求 → 不拦截
	notClaudeCode := detectInterceptType(body, "claude-opus-4-8", 1, false, false)
	require.Equal(t, InterceptTypeNone, notClaudeCode)

	// 流式请求即使 max_tokens=1 也不拦截
	streamReq := detectInterceptType(body, "claude-opus-4-8", 1, true, true)
	require.Equal(t, InterceptTypeNone, streamReq)

	// max_tokens > 1 的请求不算探测请求
	largeReq := detectInterceptType(body, "claude-opus-4-8", 100, false, true)
	require.Equal(t, InterceptTypeNone, largeReq)
}

func TestIsClaudeCodeConnectionProbeRequest(t *testing.T) {
	require.True(t, isClaudeCodeConnectionProbeRequest(1, false))
	require.False(t, isClaudeCodeConnectionProbeRequest(1, true))
	require.False(t, isClaudeCodeConnectionProbeRequest(2, false))
}

func TestIsAnthropicMessagesSyncRequest(t *testing.T) {
	require.True(t, isAnthropicMessagesSyncRequest(false))
	require.False(t, isAnthropicMessagesSyncRequest(true))
}

// TestConnectionProbeBypassesSyncCheck 验证 Claude Code 连通性探测请求
// （max_tokens=1, stream=false）能正确被识别且应该绕过同步请求检查。
// 这是 Claude Code Desktop "Test Connection" 功能能正常工作的关键。
func TestConnectionProbeBypassesSyncCheck(t *testing.T) {
	// 探测请求特征：max_tokens=1, stream=false
	require.True(t, isClaudeCodeConnectionProbeRequest(1, false))
	require.True(t, isAnthropicMessagesSyncRequest(false))

	// 即使同步请求会被拦截，探测请求应通过 context 标识被允许通过。
	// 实际逻辑在 gateway_handler.go Messages() 中：
	// if isAnthropicMessagesSyncRequest(reqStream) && !isConnectionProbe { reject }
}

func TestDetectInterceptType_SuggestionModeUnaffected(t *testing.T) {
	body := []byte(`{
		"messages":[{
			"role":"user",
			"content":[{"type":"text","text":"[SUGGESTION MODE:foo]"}]
		}],
		"system":[]
	}`)

	got := detectInterceptType(body, "claude-sonnet-4-5", 256, false, false)
	require.Equal(t, InterceptTypeSuggestionMode, got)
}

func TestSendMockInterceptResponse_MaxTokensOneHaiku(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)

	sendMockInterceptResponse(ctx, "claude-haiku-4-5", InterceptTypeMaxTokensOneHaiku)

	require.Equal(t, http.StatusOK, rec.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Equal(t, "max_tokens", response["stop_reason"])

	id, ok := response["id"].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(id, "msg_bdrk_"))

	content, ok := response["content"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, content)

	firstBlock, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "#", firstBlock["text"])

	usage, ok := response["usage"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(1), usage["output_tokens"])
}
