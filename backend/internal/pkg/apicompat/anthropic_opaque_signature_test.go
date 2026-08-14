package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicToResponsesPreservesOpaqueSignatureBytes(t *testing.T) {
	const signature = "  opaque-signature-with-padding \n"
	req := &AnthropicRequest{
		Model: "grok-4.5",
		Messages: []AnthropicMessage{{
			Role: "assistant",
			Content: mustJSONForOpaqueTest(t, []AnthropicContentBlock{
				{Type: "thinking", Thinking: "internal", Signature: signature},
			}),
		}},
	}

	converted, err := AnthropicToResponses(req)
	require.NoError(t, err)
	var input []ResponsesInputItem
	require.NoError(t, json.Unmarshal(converted.Input, &input))
	require.Len(t, input, 1)
	require.Equal(t, signature, input[0].EncryptedContent)
}

func mustJSONForOpaqueTest(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}
