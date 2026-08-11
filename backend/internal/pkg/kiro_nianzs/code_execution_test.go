package kiro

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const legacyCodeExecutionRequestForTest = `{
  "model":"claude-opus-5",
  "stream":true,
  "messages":[{"role":"user","content":"run Python"}],
  "tools":[{"type":"code_execution_20250522","name":"code_execution"}]
}`

func TestReplaceLegacyCodeExecutionToolBuildsKiroCallableSchema(t *testing.T) {
	require.True(t, IsOnlyLegacyCodeExecutionTool([]byte(legacyCodeExecutionRequestForTest)))
	updated, err := ReplaceLegacyCodeExecutionTool([]byte(legacyCodeExecutionRequestForTest))
	require.NoError(t, err)
	require.Equal(t, "code_execution", gjson.GetBytes(updated, "tools.0.name").String())
	require.False(t, gjson.GetBytes(updated, "tools.0.type").Exists())
	require.Equal(t, "string", gjson.GetBytes(updated, "tools.0.input_schema.properties.code.type").String())
	require.Equal(t, "code", gjson.GetBytes(updated, "tools.0.input_schema.required.0").String())
	require.False(t, gjson.GetBytes(updated, "tools.0.input_schema.additionalProperties").Bool())
}

func TestAnalyzeCodeExecutionBufferedStreamExtractsCompleteCall(t *testing.T) {
	chunks := [][]byte{
		[]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"),
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_test\",\"name\":\"code_execution\",\"input\":{}}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"code\\\":\\\"print('HELLO_CHECK')\\\"}\"}}\n\n"),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"),
	}
	call, ok := AnalyzeCodeExecutionBufferedStream(chunks)
	require.True(t, ok)
	require.Equal(t, "toolu_test", call.ToolUseID)
	require.Equal(t, "print('HELLO_CHECK')", call.Code)
	require.Equal(t, 1, call.Index)
}

func TestInjectCodeExecutionResultClaudeClosesUpstreamToolCycle(t *testing.T) {
	replaced, err := ReplaceLegacyCodeExecutionTool([]byte(legacyCodeExecutionRequestForTest))
	require.NoError(t, err)
	updated, err := InjectCodeExecutionResultClaude(replaced, CodeExecutionCall{
		ToolUseID: "toolu_test",
		Code:      "print('HELLO_CHECK')",
	}, CodeExecutionResult{Stdout: "HELLO_CHECK\n"})
	require.NoError(t, err)
	require.Equal(t, "assistant", gjson.GetBytes(updated, "messages.1.role").String())
	require.Equal(t, "tool_use", gjson.GetBytes(updated, "messages.1.content.0.type").String())
	require.Equal(t, "toolu_test", gjson.GetBytes(updated, "messages.2.content.0.tool_use_id").String())
	require.Contains(t, gjson.GetBytes(updated, "messages.2.content.0.content").String(), "HELLO_CHECK")
}

func TestGenerateLegacyCodeExecutionEventsUsesServerToolProtocol(t *testing.T) {
	result := CodeExecutionResult{Stdout: "HELLO_CHECK\n", Stderr: "", ReturnCode: 0}
	events := append(
		GenerateCodeExecutionToolUseEvents("print('HELLO_CHECK')", "srvtoolu_test", 2),
		GenerateCodeExecutionResultEvents("srvtoolu_test", result, 3)...,
	)
	wire := string(bytesJoin(events))
	require.Contains(t, wire, `"type":"server_tool_use"`)
	require.Contains(t, wire, `"name":"code_execution"`)
	require.Contains(t, wire, `"type":"code_execution_tool_result"`)
	require.Contains(t, wire, `"type":"code_execution_result"`)
	require.Contains(t, wire, `"stdout":"HELLO_CHECK\n"`)
	require.NotContains(t, wire, `"type":"tool_use"`)
}

func TestInjectCodeExecutionIndicatorsInResponseAddsUsage(t *testing.T) {
	response := []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`)
	updated, err := InjectCodeExecutionIndicatorsInResponse(response, []CodeExecutionIndicator{{
		ServerToolUseID: "srvtoolu_test",
		Code:            "print('HELLO_CHECK')",
		Result:          CodeExecutionResult{Stdout: "HELLO_CHECK\n"},
	}})
	require.NoError(t, err)
	require.Equal(t, "server_tool_use", gjson.GetBytes(updated, "content.0.type").String())
	require.Equal(t, "code_execution_tool_result", gjson.GetBytes(updated, "content.1.type").String())
	require.Equal(t, "done", gjson.GetBytes(updated, "content.2.text").String())
	require.Equal(t, int64(1), gjson.GetBytes(updated, "usage.server_tool_use.code_execution_requests").Int())
}

func TestExtractCodeExecutionTurnPreservesIntermediateContentOrder(t *testing.T) {
	intermediate := []byte(`{"content":[{"type":"text","text":"before"},{"type":"tool_use","id":"toolu_test","name":"code_execution","input":{"code":"print(1)"}},{"type":"text","text":"after"}]}`)
	call, before, after, ok := ExtractCodeExecutionTurnFromResponse(intermediate)
	require.True(t, ok)
	require.Equal(t, "print(1)", call.Code)

	response := []byte(`{"content":[{"type":"text","text":"done"}],"usage":{}}`)
	updated, err := InjectCodeExecutionIndicatorsInResponse(response, []CodeExecutionIndicator{{
		ServerToolUseID: "srvtoolu_test",
		Code:            call.Code,
		Result:          CodeExecutionResult{Stdout: "1\n"},
		Before:          before,
		After:           after,
	}})
	require.NoError(t, err)
	require.Equal(t, "before", gjson.GetBytes(updated, "content.0.text").String())
	require.Equal(t, "server_tool_use", gjson.GetBytes(updated, "content.1.type").String())
	require.Equal(t, "code_execution_tool_result", gjson.GetBytes(updated, "content.2.type").String())
	require.Equal(t, "after", gjson.GetBytes(updated, "content.3.text").String())
	require.Equal(t, "done", gjson.GetBytes(updated, "content.4.text").String())
}

func TestAdjustSSEChunkWithCodeExecutionUsagePreservesExistingUsage(t *testing.T) {
	chunk := []byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n")
	adjusted, ok := AdjustSSEChunkWithCodeExecutionUsage(chunk, 0, 2)
	require.True(t, ok)
	payload := strings.TrimSpace(strings.TrimPrefix(strings.Split(string(adjusted), "\n")[1], "data: "))
	require.Equal(t, int64(2), gjson.Get(payload, "usage.output_tokens").Int())
	require.Equal(t, int64(2), gjson.Get(payload, "usage.server_tool_use.code_execution_requests").Int())
}

func bytesJoin(chunks [][]byte) []byte {
	var builder strings.Builder
	for _, chunk := range chunks {
		_, _ = builder.Write(chunk)
	}
	return []byte(builder.String())
}

func TestLegacyCodeExecutionResultContentIsJSONSerializable(t *testing.T) {
	_, err := json.Marshal(legacyCodeExecutionResultContent(CodeExecutionResult{Stdout: "ok"}))
	require.NoError(t, err)
}
