package service

import "testing"

func TestResponsesStreamPayloadHasMeaningfulOutput(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		eventType string
		want      bool
	}{
		{name: "created preamble", payload: `{"type":"response.created"}`, eventType: "response.created"},
		{name: "in progress preamble", payload: `{"type":"response.in_progress"}`, eventType: "response.in_progress"},
		{name: "message item metadata", payload: `{"type":"response.output_item.added","item":{"type":"message"}}`, eventType: "response.output_item.added"},
		{name: "empty text delta", payload: `{"type":"response.output_text.delta","delta":""}`, eventType: "response.output_text.delta"},
		{name: "text delta", payload: `{"type":"response.output_text.delta","delta":"hello"}`, eventType: "response.output_text.delta", want: true},
		{name: "whitespace token", payload: `{"type":"response.output_text.delta","delta":" "}`, eventType: "response.output_text.delta", want: true},
		{name: "reasoning delta", payload: `{"type":"response.reasoning_text.delta","delta":"think"}`, eventType: "response.reasoning_text.delta", want: true},
		{name: "tool arguments", payload: `{"type":"response.function_call_arguments.delta","delta":"{\"path\":"}`, eventType: "response.function_call_arguments.delta", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := responsesStreamPayloadHasMeaningfulOutput([]byte(tt.payload), tt.eventType); got != tt.want {
				t.Fatalf("responsesStreamPayloadHasMeaningfulOutput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChatCompletionsPayloadHasMeaningfulOutput(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{name: "role only", payload: `{"choices":[{"delta":{"role":"assistant"}}]}`},
		{name: "usage only", payload: `{"choices":[],"usage":{"prompt_tokens":1}}`},
		{name: "empty content", payload: `{"choices":[{"delta":{"content":""}}]}`},
		{name: "content", payload: `{"choices":[{"delta":{"content":"hello"}}]}`, want: true},
		{name: "reasoning", payload: `{"choices":[{"delta":{"reasoning_content":"think"}}]}`, want: true},
		{name: "tool call", payload: `{"choices":[{"delta":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"exec"}}]}}]}`, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chatCompletionsPayloadHasMeaningfulOutput([]byte(tt.payload)); got != tt.want {
				t.Fatalf("chatCompletionsPayloadHasMeaningfulOutput() = %v, want %v", got, tt.want)
			}
		})
	}
}
