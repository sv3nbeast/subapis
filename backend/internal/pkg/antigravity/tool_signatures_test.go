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
