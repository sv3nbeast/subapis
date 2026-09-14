package kiro

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

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
		// Bedrock 会插入 bdrk_ 段，官方 ID 没有，必须抹掉
		{"toolu_bdrk_011Czx95Lx414PZQkAicFfBC", "toolu_011Czx95Lx414PZQkAicFfBC"},
		{"srvtoolu_bdrk_0199", "srvtoolu_0199"},
		{"toolu_bdrk_", "toolu_bdrk_"}, // 无主体时不产生裸前缀
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
	require.Contains(t, wire, "event: ping", "real Claude streams carry a ping after the first content_block_start")

	// 官方以一个空 partial_json 起手，再分多帧增量下发输入；拼接后必须等于完整输入。
	deltas := make([]string, 0, 4)
	for _, event := range parseAnthropicSSEEventsForTest(t, wire) {
		if event.Get("delta.type").String() == "input_json_delta" {
			deltas = append(deltas, event.Get("delta.partial_json").String())
		}
	}
	require.Greater(t, len(deltas), 2, "tool input must stream incrementally, not as one frame")
	require.Equal(t, "", deltas[0], "the first input_json_delta must be an empty partial_json")
	require.Equal(t, `{"city":"beijing"}`, strings.Join(deltas, ""))

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

// concatToolInputJSONForTest 拼接 SSE 中所有 input_json_delta 的 partial_json。
// 工具输入按 Anthropic 官方形态分片下发，单帧不再是完整 JSON，断言需要看拼接结果。
func concatToolInputJSONForTest(t *testing.T, sse string) string {
	t.Helper()
	var sb strings.Builder
	for _, event := range parseAnthropicSSEEventsForTest(t, sse) {
		if event.Get("delta.type").String() != "input_json_delta" {
			continue
		}
		sb.WriteString(event.Get("delta.partial_json").String())
	}
	return sb.String()
}

// 分片必须可无损拼接回原始 JSON，且不切坏多字节字符。
func TestSplitToolInputJSONDeltasRoundTrips(t *testing.T) {
	inputs := []string{
		`{"city":"北京","unit":"c"}`,
		`{"query":"golang"}`,
		`{}`,
		`{"path":"/tmp/a.txt","content":"hello world, this is a longer value"}`,
		`{"escaped":"a\"b\\c","nested":{"k":[1,2,3]}}`,
	}
	for _, in := range inputs {
		chunks := splitToolInputJSONDeltas(in)
		require.Equal(t, in, strings.Join(chunks, ""), "chunks must rejoin losslessly: %s", in)
		for _, c := range chunks {
			require.True(t, utf8.ValidString(c), "chunk %q must stay valid UTF-8", c)
		}
		if len([]rune(in)) > 8 {
			require.Greater(t, len(chunks), 1, "a non-trivial input should stream in multiple deltas: %s", in)
		}
	}
	require.Empty(t, splitToolInputJSONDeltas(""))
}

// Kiro 协议没有 toolChoice 字段，tool_choice=any/tool 只能靠提示逼近官方的协议级保证。
// system 段提示对问候类输入常被忽略，因此强制要求必须同时出现在当前用户消息末尾。
func TestForcedToolChoiceAddsUserHint(t *testing.T) {
	cases := []struct {
		name       string
		toolChoice string
		wantInUser string
	}{
		{"any", `{"type":"any"}`, "calling one of the available tools"},
		{"named tool", `{"type":"tool","name":"get_weather"}`, "calling the 'get_weather' tool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"model":"claude-opus-5","max_tokens":400,
				"messages":[{"role":"user","content":"你好"}],
				"tools":[{"name":"get_weather","description":"获取天气","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],
				"tool_choice":` + tc.toolChoice + `}`
			res, err := BuildKiroPayloadWithOptions(
				[]byte(body), "claude-opus-5", "arn:test", http.Header{},
				KiroPayloadOptions{RequireNativeToolProgress: true},
			)
			require.NoError(t, err)

			payload := gjson.ParseBytes(res.Payload)
			userContent := payload.Get("conversationState.currentMessage.userInputMessage.content").String()
			require.Contains(t, userContent, "你好", "the original user text must be preserved")
			require.Contains(t, userContent, "[CRITICAL]", "forced tool choice must be restated on the user turn")
			require.Contains(t, userContent, tc.wantInUser)
			require.True(t, res.Context.NativeToolCallRequired, "forced tool choice must arm the native tool-call guard")
		})
	}

	// tool_choice=auto 不得追加强制提示。
	body := `{"model":"claude-opus-5","max_tokens":400,
		"messages":[{"role":"user","content":"你好"}],
		"tools":[{"name":"get_weather","description":"获取天气","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],
		"tool_choice":{"type":"auto"}}`
	res, err := BuildKiroPayloadWithOptions(
		[]byte(body), "claude-opus-5", "arn:test", http.Header{},
		KiroPayloadOptions{RequireNativeToolProgress: true},
	)
	require.NoError(t, err)
	userContent := gjson.ParseBytes(res.Payload).Get("conversationState.currentMessage.userInputMessage.content").String()
	require.NotContains(t, userContent, "[CRITICAL]")
}
