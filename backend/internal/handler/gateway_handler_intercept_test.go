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

// TestDetectInterceptType_ConnectionProbeIsClientAgnostic 验证连通性探测请求
// （max_tokens=1, !stream）对任意客户端、任意模型都会被拦截 mock，
// 覆盖 claude-cli、Claude Code Desktop、Anthropic SDK 等各类 Test Connection 场景。
func TestDetectInterceptType_ConnectionProbeIsClientAgnostic(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	// Claude Code 客户端探测 → 拦截
	cli := detectInterceptType(body, "claude-haiku-4-5", 1, false, true)
	require.Equal(t, InterceptTypeMaxTokensOneHaiku, cli)

	// 非 Claude Code 客户端（如 Claude Code Desktop）探测 → 也拦截
	desktop := detectInterceptType(body, "claude-haiku-4-5", 1, false, false)
	require.Equal(t, InterceptTypeMaxTokensOneHaiku, desktop)

	// 任意模型探测都拦截（opus / sonnet / haiku）
	opus := detectInterceptType(body, "claude-opus-4-8", 1, false, false)
	require.Equal(t, InterceptTypeMaxTokensOneHaiku, opus)
	sonnet := detectInterceptType(body, "claude-sonnet-4-5", 1, false, false)
	require.Equal(t, InterceptTypeMaxTokensOneHaiku, sonnet)

	// 流式请求即使 max_tokens=1 也不拦截（让真实流式探测通过）
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
