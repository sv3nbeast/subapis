package service

import (
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

const (
	OpenAIUpstreamEndpointResponses       = "/v1/responses"
	OpenAIUpstreamEndpointChatCompletions = "/v1/chat/completions"
)

// responsesStreamPayloadHasMeaningfulOutput excludes Responses lifecycle
// events and empty deltas so TTFT measures the first model output, not the
// initial response.created/response.in_progress preamble.
func responsesStreamPayloadHasMeaningfulOutput(payload []byte, eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.output_text.delta",
		"response.reasoning_summary_text.delta",
		"response.reasoning_text.delta",
		"response.function_call_arguments.delta",
		"response.custom_tool_call_input.delta":
		delta := gjson.GetBytes(payload, "delta")
		return delta.Exists() && delta.String() != ""
	default:
		return false
	}
}

// chatCompletionsPayloadHasMeaningfulOutput ignores role-only and usage-only
// chunks. Text, reasoning, or a tool call marks the first usable model output.
func chatCompletionsPayloadHasMeaningfulOutput(payload []byte) bool {
	var chunk apicompat.ChatCompletionsChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return false
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			return true
		}
		if choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
			return true
		}
		if len(choice.Delta.ToolCalls) > 0 {
			return true
		}
	}
	return false
}
