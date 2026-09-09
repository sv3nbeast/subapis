package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func agentAnthropicSSEFixture() string {
	return "data: " + strings.Join([]string{
		`{"type":"message_start","message":{"type":"message","content":[],"usage":{"input_tokens":12,"cache_read_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":0}}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"{\"kind\":"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"not part of artifact"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"private"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"\"document\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
		`{"type":"message_stop"}`,
	}, "\n\ndata: ") + "\n\n"
}

func TestWebAgentAnthropicSSETerminalAndPartialFailures(t *testing.T) {
	base := agentAnthropicSSEFixture()
	for _, tc := range []struct {
		name, data string
		ok         bool
	}{
		{"complete", base, true},
		{"crlf", strings.ReplaceAll(base, "\n", "\r\n"), true},
		{"ping", "data: {\"type\":\"ping\"}\n\n" + base, true},
		{"truncated", strings.ReplaceAll(base, "data: {\"type\":\"message_stop\"}\n\n", ""), false},
		{"incomplete", strings.ReplaceAll(base, "end_turn", "max_tokens"), false},
		{"refusal", strings.ReplaceAll(base, "end_turn", "refusal"), false},
		{"tool", strings.ReplaceAll(base, `"type":"thinking"`, `"type":"tool_use"`), false},
		{"bad-index", strings.ReplaceAll(base, `"index":2`, `"index":9`), false},
		{"error", strings.ReplaceAll(base, `{"type":"message_stop"}`, `{"type":"error","error":{"message":"failed"}}`), false},
		{"early-stop", "data: {\"type\":\"message_stop\"}\n\n", false},
		{"empty-stop", "event: message_stop\n\n", false},
		{"missing-delta", strings.ReplaceAll(base, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`+"\n\n", ""), false},
		{"budget", ":" + strings.Repeat("a", webAgentModelMaxResponseBytes) + "\n\n" + base, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out WebAgentModelOutput
			err := readWebAgentAnthropicStream(strings.NewReader(tc.data), &out)
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, `{"kind":"document"}`, out.Content)
				require.Equal(t, int64(7), out.Generation.Usage.CacheReadTokens)
				require.Equal(t, int64(8), out.Generation.Usage.OutputTokens)
			} else {
				require.Error(t, err)
				require.Empty(t, out.Content)
			}
		})
	}
}
