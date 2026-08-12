package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type anthropicSSEProtocolEvent struct {
	name string
	data gjson.Result
}

func parseAnthropicSSEProtocolEvents(t *testing.T, wire string) []anthropicSSEProtocolEvent {
	t.Helper()
	events := make([]anthropicSSEProtocolEvent, 0)
	for _, frame := range strings.Split(wire, "\n\n") {
		if strings.TrimSpace(frame) == "" {
			continue
		}
		var name, payload string
		for _, line := range strings.Split(frame, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				payload = strings.TrimPrefix(line, "data: ")
			}
		}
		require.NotEmpty(t, name, "SSE frame is missing event name: %q", frame)
		require.True(t, gjson.Valid(payload), "SSE frame has invalid JSON: %q", frame)
		data := gjson.Parse(payload)
		require.Equal(t, name, data.Get("type").String(), "SSE event name and JSON type must agree")
		events = append(events, anthropicSSEProtocolEvent{name: name, data: data})
	}
	return events
}

func requireAnthropicSSEProtocolLifecycle(t *testing.T, wire string) {
	t.Helper()
	events := parseAnthropicSSEProtocolEvents(t, wire)
	require.NotEmpty(t, events)
	require.Equal(t, "message_start", events[0].name)
	require.Equal(t, "assistant", events[0].data.Get("message.role").String())
	require.True(t, strings.HasPrefix(events[0].data.Get("message.id").String(), "msg_"))
	require.Equal(t, int64(0), events[0].data.Get("message.content.#").Int())
	require.Equal(t, gjson.Null, events[0].data.Get("message.stop_reason").Type)

	nextIndex := int64(0)
	openIndex := int64(-1)
	openType := ""
	lastDeltaType := ""
	messageDeltaCount := 0
	messageStopCount := 0
	for i, event := range events[1:] {
		switch event.name {
		case "content_block_start":
			require.Equal(t, int64(-1), openIndex, "content blocks must not overlap")
			require.Equal(t, nextIndex, event.data.Get("index").Int(), "content block indices must be contiguous")
			openIndex = nextIndex
			nextIndex++
			openType = event.data.Get("content_block.type").String()
			require.Contains(t, []string{"text", "thinking", "tool_use", "server_tool_use", "web_search_tool_result", "code_execution_tool_result"}, openType)
			lastDeltaType = ""
		case "content_block_delta":
			require.NotEqual(t, int64(-1), openIndex, "delta requires an open content block")
			require.Equal(t, openIndex, event.data.Get("index").Int())
			deltaType := event.data.Get("delta.type").String()
			switch openType {
			case "text":
				require.Equal(t, "text_delta", deltaType)
			case "thinking":
				require.Contains(t, []string{"thinking_delta", "signature_delta"}, deltaType)
				if deltaType == "signature_delta" {
					require.NotEmpty(t, event.data.Get("delta.signature").String())
				}
			case "tool_use", "server_tool_use":
				require.Equal(t, "input_json_delta", deltaType)
			default:
				t.Fatalf("content block type %q must not emit delta %q", openType, deltaType)
			}
			lastDeltaType = deltaType
		case "content_block_stop":
			require.NotEqual(t, int64(-1), openIndex, "stop requires an open content block")
			require.Equal(t, openIndex, event.data.Get("index").Int())
			if openType == "thinking" {
				require.Equal(t, "signature_delta", lastDeltaType, "thinking signature must be the final delta before block stop")
			}
			openIndex = -1
			openType = ""
			lastDeltaType = ""
		case "message_delta":
			require.Equal(t, int64(-1), openIndex, "message_delta cannot close an open content block")
			messageDeltaCount++
			require.Equal(t, 0, messageStopCount)
			require.NotEmpty(t, normalizeAnthropicStopReason(event.data.Get("delta.stop_reason").String()))
			require.True(t, event.data.Get("usage.output_tokens").Exists())
		case "message_stop":
			require.Equal(t, int64(-1), openIndex, "message_stop cannot close an open content block")
			messageStopCount++
			require.Equal(t, len(events)-2, i, "message_stop must be the final event")
		default:
			t.Fatalf("unexpected Anthropic SSE event %q", event.name)
		}
	}
	require.Equal(t, 1, messageDeltaCount)
	require.Equal(t, 1, messageStopCount)
}

func TestAnthropicProtocolComplianceThinkingTextStream(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "inspect protocol"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "final answer"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-8", 11, KiroRequestContext{
		ThinkingEnabled:      true,
		RequireTerminalEvent: true,
	})
	require.NoError(t, err)
	requireAnthropicSSEProtocolLifecycle(t, out.String())
}

func TestAnthropicProtocolComplianceWebSearchServerBlocks(t *testing.T) {
	snippet := "Protocol result"
	var wire strings.Builder
	writeProtocolEventForTest(t, &wire, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_protocol", "type": "message", "role": "assistant", "content": []any{},
			"model": "claude-opus-5", "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 0},
		},
	})
	for _, event := range GenerateSearchIndicatorEvents("protocol query", "srvtoolu_protocol", &WebSearchResults{
		Results: []WebSearchResult{{Title: "Protocol", URL: "https://example.com/protocol", Snippet: &snippet}},
	}, 0) {
		_, _ = wire.Write(event)
	}
	writeProtocolEventForTest(t, &wire, "message_delta", map[string]any{
		"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 5, "server_tool_use": map[string]any{"web_search_requests": 1}},
	})
	writeProtocolEventForTest(t, &wire, "message_stop", map[string]any{"type": "message_stop"})

	requireAnthropicSSEProtocolLifecycle(t, wire.String())
	events := parseAnthropicSSEProtocolEvents(t, wire.String())
	require.Equal(t, "srvtoolu_protocol", events[1].data.Get("content_block.id").String())
	require.Equal(t, "srvtoolu_protocol", events[4].data.Get("content_block.tool_use_id").String())
	require.NotEmpty(t, events[4].data.Get("content_block.content.0.encrypted_content").String())
}

func TestAnthropicProtocolCompliancePreservesCurrentStopReasons(t *testing.T) {
	for _, stopReason := range []string{"pause_turn", "refusal", "model_context_window_exceeded"} {
		t.Run(stopReason, func(t *testing.T) {
			stream := bytes.NewBuffer(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
				"assistantResponseEvent": map[string]any{"content": "partial", "stopReason": stopReason},
			}))
			var out bytes.Buffer
			result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-5", 3, KiroRequestContext{
				RequireTerminalEvent: true,
			})
			require.NoError(t, err)
			require.Equal(t, stopReason, result.StopReason)
			requireAnthropicSSEProtocolLifecycle(t, out.String())
		})
	}
}

func writeProtocolEventForTest(t *testing.T, dst *strings.Builder, name string, data map[string]any) {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	_, _ = fmt.Fprintf(dst, "event: %s\ndata: %s\n\n", name, payload)
}
