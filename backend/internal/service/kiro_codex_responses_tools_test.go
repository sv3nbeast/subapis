package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeKiroCodexResponsesToolsExtractsAdditionalToolsAndNamespaces(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"inspect the repository"}]},
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"custom","name":"exec","description":"Run JavaScript orchestration","format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}},
				{"type":"namespace","name":"codex_app","tools":[
					{"type":"function","name":"read_thread_terminal","description":"Read terminal output","inputSchema":{"type":"object","properties":{},"additionalProperties":false}}
				]}
			]}
		],
		"stream":true
	}`)

	normalized, metadata, err := normalizeKiroCodexResponsesTools(body)
	require.NoError(t, err)
	require.Equal(t, 2, metadata.DeclaredToolCount)
	require.Equal(t, 2, metadata.ForwardedToolCount)
	require.Contains(t, metadata.CustomToolNames, "exec")
	require.Equal(t, int64(1), gjson.GetBytes(normalized, "input.#").Int())
	require.Equal(t, "inspect the repository", gjson.GetBytes(normalized, "input.0.content.0.text").String())
	require.Equal(t, int64(2), gjson.GetBytes(normalized, "tools.#").Int())
	require.Equal(t, "exec", gjson.GetBytes(normalized, "tools.0.name").String())
	require.Equal(t, "string", gjson.GetBytes(normalized, "tools.0.parameters.properties.input.type").String())
	require.Equal(t, "input", gjson.GetBytes(normalized, "tools.0.parameters.required.0").String())
	require.Contains(t, gjson.GetBytes(normalized, "tools.0.description").String(), "freeform input")
	require.Equal(t, "codex_app__read_thread_terminal", gjson.GetBytes(normalized, "tools.1.name").String())
	require.Equal(t, "object", gjson.GetBytes(normalized, "tools.1.parameters.type").String())

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(normalized, &parsed))
}

func TestNormalizeKiroCodexResponsesToolsLeavesToollessRequestByteStable(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","stream":true}`)
	normalized, metadata, err := normalizeKiroCodexResponsesTools(body)
	require.NoError(t, err)
	require.Equal(t, body, normalized)
	require.Zero(t, metadata.DeclaredToolCount)
	require.Zero(t, metadata.ForwardedToolCount)
}

func TestNormalizeKiroCodexResponsesToolsConvertsHistoryWithoutDeclarations(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"previous_response_id":"resp_exec",
		"input":[
			{"type":"custom_tool_call","call_id":"call_exec","name":"exec","input":"text(\"hello\")"},
			{"type":"custom_tool_call_output","call_id":"call_exec","output":"hello"}
		]
	}`)

	normalized, metadata, err := normalizeKiroCodexResponsesTools(body)
	require.NoError(t, err)
	require.Zero(t, metadata.DeclaredToolCount)
	require.Zero(t, metadata.ForwardedToolCount)
	require.Equal(t, "function_call", gjson.GetBytes(normalized, "input.0.type").String())
	require.JSONEq(t, `{"input":"text(\"hello\")"}`, gjson.GetBytes(normalized, "input.0.arguments").String())
	require.False(t, gjson.GetBytes(normalized, "input.0.input").Exists())
	require.Equal(t, "function_call_output", gjson.GetBytes(normalized, "input.1.type").String())
}

func TestNormalizeKiroCodexResponsesToolsPromotesCodexLiteToolChoiceNoneToAuto(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"tool_choice":"none",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"continue"}]},
			{"type":"additional_tools","tools":[{"type":"custom","name":"exec","description":"Run JavaScript"}]}
		]
	}`)

	normalized, metadata, err := normalizeKiroCodexResponsesTools(body)
	require.NoError(t, err)
	require.Equal(t, 1, metadata.DeclaredToolCount)
	require.Equal(t, "auto", gjson.GetBytes(normalized, "tool_choice").String())
	require.Equal(t, int64(1), gjson.GetBytes(normalized, "tools.#").Int())
}

func TestNormalizeKiroCodexResponsesToolsPreservesExplicitNoneOutsideCodexLite(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"tool_choice":"none",
		"tools":[{"type":"function","name":"read","parameters":{"type":"object","properties":{}}}],
		"input":[{"role":"user","content":[{"type":"input_text","text":"answer directly"}]}]
	}`)

	normalized, metadata, err := normalizeKiroCodexResponsesTools(body)
	require.NoError(t, err)
	require.Equal(t, 1, metadata.DeclaredToolCount)
	require.Equal(t, "none", gjson.GetBytes(normalized, "tool_choice").String())
}
