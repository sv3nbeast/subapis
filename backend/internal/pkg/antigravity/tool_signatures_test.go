package antigravity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractToolUseSignaturesFromClaudeResponse(t *testing.T) {
	body := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-opus-4-6",
		"content":[
			{"type":"text","text":"hello"},
			{"type":"tool_use","id":"tool_1","name":"Bash","input":{"command":"ls"},"signature":"sig_1"},
			{"type":"tool_use","id":"tool_2","name":"Read","input":{"file_path":"a.txt"}}
		],
		"usage":{"input_tokens":1,"output_tokens":2}
	}`)

	signatures, err := ExtractToolUseSignaturesFromClaudeResponse(body)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"tool_1": "sig_1"}, signatures)
}

func TestStreamingProcessor_ToolUseSignatures(t *testing.T) {
	processor := NewStreamingProcessor("claude-opus-4-6", nil)
	out := processor.ProcessLine(`data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Bash","id":"tool_1","args":{"command":"ls"}},"thoughtSignature":"sig_stream_1"}]},"finishReason":"STOP"}]}}`)
	require.NotEmpty(t, out)
	require.Equal(t, map[string]string{"tool_1": "sig_stream_1"}, processor.ToolUseSignatures())
}

func TestStreamingProcessor_ConvertsSplitMCPXMLTextToToolUse(t *testing.T) {
	processor := NewStreamingProcessor("claude-opus-4-6", map[string]string{
		"mcp__workspace__read_file": "mcp__workspace__read_file",
	})

	first := processor.ProcessLine(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"Before <mcp__workspace__read_file>{\"path\":"}]},"finishReason":""}]}}`)
	require.NotEmpty(t, first)
	require.NotContains(t, string(first), "<mcp__")
	require.NotContains(t, string(first), "tool_use")

	second := processor.ProcessLine(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"\"/tmp/demo.txt\"}</mcp__workspace__read_file> After"}]},"finishReason":"STOP"}]}}`)
	require.NotEmpty(t, second)
	out := string(append(first, second...))
	require.NotContains(t, out, "<mcp__")
	require.Contains(t, out, `"type":"tool_use"`)
	require.Contains(t, out, `"name":"mcp__workspace__read_file"`)
	require.Contains(t, out, `"partial_json":"{\"path\":\"/tmp/demo.txt\"}"`)
	require.Contains(t, out, `"stop_reason":"tool_use"`)
}

func TestStreamingProcessor_ConvertsSplitEscapedMCPXMLTextToToolUse(t *testing.T) {
	processor := NewStreamingProcessor("claude-opus-4-6", map[string]string{
		"mcp__workspace__read_file": "mcp__workspace__read_file",
	})

	first := processor.ProcessLine(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"Before &lt;mc"}]},"finishReason":""}]}}`)
	require.NotEmpty(t, first)
	require.NotContains(t, string(first), "&lt;mc")
	require.NotContains(t, string(first), "tool_use")

	second := processor.ProcessLine(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"p__workspace__read_file&gt;{\"path\":\"/tmp/demo.txt\"}&lt;/mcp__workspace__read_file&gt; After"}]},"finishReason":"STOP"}]}}`)
	require.NotEmpty(t, second)
	out := string(append(first, second...))
	require.NotContains(t, out, "&lt;mcp__")
	require.NotContains(t, out, "&lt;mc")
	require.Contains(t, out, `"text":"Before "`)
	require.Contains(t, out, `"type":"tool_use"`)
	require.Contains(t, out, `"name":"mcp__workspace__read_file"`)
	require.Contains(t, out, `"partial_json":"{\"path\":\"/tmp/demo.txt\"}"`)
	require.Contains(t, out, `"stop_reason":"tool_use"`)
}

func TestStreamingProcessor_ConvertsSignedSplitMCPXMLTextToSignedToolUse(t *testing.T) {
	processor := NewStreamingProcessor("claude-opus-4-6", map[string]string{
		"mcp__workspace__read_file": "mcp__workspace__read_file",
	})

	first := processor.ProcessLine(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"<mcp__workspace__read_file>{\"path\":","thoughtSignature":"sig_xml_tool"}]},"finishReason":""}]}}`)
	require.NotEmpty(t, first)
	require.NotContains(t, string(first), "<mcp__")

	second := processor.ProcessLine(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"\"/tmp/demo.txt\"}</mcp__workspace__read_file>"}]},"finishReason":"STOP"}]}}`)
	require.NotEmpty(t, second)
	out := string(append(first, second...))
	require.NotContains(t, out, "<mcp__")
	require.Contains(t, out, `"type":"tool_use"`)
	require.Contains(t, out, `"signature":"sig_xml_tool"`)
	signatures := processor.ToolUseSignatures()
	require.Len(t, signatures, 1)
	for toolID, signature := range signatures {
		require.Contains(t, toolID, "mcp__workspace__read_file")
		require.Equal(t, "sig_xml_tool", signature)
	}
}
