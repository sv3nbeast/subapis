package kiro

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Kiro 返回的 tool use ID 形如 tooluse_xxx，而 Anthropic 官方是 toolu_xxx。
// 写给客户端的响应必须用官方前缀，否则一眼就能看出不是原生 Claude 响应。
func TestNormalizeAnthropicToolUseID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tooluse_AbC123", "toolu_AbC123"},
		{"toolu_AbC123", "toolu_AbC123"}, // 已是官方前缀，保持不变
		{"tooluse_", "tooluse_"},         // 空 ID 主体，不产生裸前缀
		{"", ""},
		{"srvtoolu_web", "srvtoolu_web"}, // 服务端工具 ID 不受影响
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, normalizeAnthropicToolUseID(tc.in), "input %q", tc.in)
	}
}

// 流式：自定义工具的 tool_use 必须用 toolu_ 前缀，且不带 caller 字段
// （caller 是 programmatic tool calling 专有，普通自定义工具没有）。
func TestStreamToolUseBlockMatchesAnthropicShape(t *testing.T) {
	stream := &bytes.Buffer{}
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "tooluse_DlOk3DfqZGyBp4E9MN2tLp", "name": "get_weather",
			"input": `{"city":"beijing"}`, "stop": true,
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(
		context.Background(), stream, &out, "claude-sonnet-4-6", 7,
		KiroRequestContext{RequireTerminalEvent: true},
	)
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)

	wire := out.String()
	require.NotContains(t, wire, "tooluse_", "the Kiro tool-use ID prefix must not reach the client")
	require.NotContains(t, wire, `"caller"`, "a plain custom tool_use must not carry a caller field")

	var sawToolUse bool
	for _, event := range parseAnthropicSSEEventsForTest(t, wire) {
		block := event.Get("content_block")
		if block.Get("type").String() != "tool_use" {
			continue
		}
		sawToolUse = true
		id := block.Get("id").String()
		require.True(t, strings.HasPrefix(id, "toolu_"), "tool_use id %q must use the toolu_ prefix", id)
		require.False(t, block.Get("caller").Exists(), "tool_use must not carry caller")
		require.Equal(t, "get_weather", block.Get("name").String())
	}
	require.True(t, sawToolUse, "expected a tool_use content block")
}

// 非流式：同样的形态要求。
func TestNonStreamingToolUseBlockMatchesAnthropicShape(t *testing.T) {
	stream := &bytes.Buffer{}
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "tooluse_DlOk3DfqZGyBp4E9MN2tLp", "name": "get_weather",
			"input": `{"city":"beijing"}`, "stop": true,
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(
		stream, "claude-sonnet-4-6", KiroRequestContext{RequireTerminalEvent: true},
	)
	require.NoError(t, err)

	body := string(result.ResponseBody)
	require.NotContains(t, body, "tooluse_", "the Kiro tool-use ID prefix must not reach the client")
	require.NotContains(t, body, `"caller"`, "a plain custom tool_use must not carry a caller field")

	block := gjson.Parse(body).Get("content.0")
	require.Equal(t, "tool_use", block.Get("type").String())
	require.True(t, strings.HasPrefix(block.Get("id").String(), "toolu_"))
	require.False(t, block.Get("caller").Exists())
}
