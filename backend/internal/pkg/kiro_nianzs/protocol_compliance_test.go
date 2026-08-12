package kiro

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
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
	require.Regexp(t, `^msg_01[0-9A-Za-z]{22}$`, events[0].data.Get("message.id").String())
	require.Equal(t, int64(0), events[0].data.Get("message.content.#").Int())
	require.Equal(t, gjson.Null, events[0].data.Get("message.stop_reason").Type)

	nextIndex := int64(0)
	openIndex := int64(-1)
	openType := ""
	lastDeltaType := ""
	openCitedText := false
	openTextSeen := false
	messageDeltaCount := 0
	messageStopCount := 0
	serverToolIDs := make(map[string]struct{})
	for i, event := range events[1:] {
		switch event.name {
		case "ping":
			// Anthropic permits keepalive pings between any two protocol
			// events. They neither open nor close a content block.
			require.Equal(t, "ping", event.data.Get("type").String())
		case "content_block_start":
			require.Equal(t, int64(-1), openIndex, "content blocks must not overlap")
			require.Equal(t, nextIndex, event.data.Get("index").Int(), "content block indices must be contiguous")
			openIndex = nextIndex
			nextIndex++
			openType = event.data.Get("content_block.type").String()
			openCitedText = false
			openTextSeen = false
			require.Contains(t, []string{"text", "thinking", "redacted_thinking", "tool_use", "server_tool_use", "web_search_tool_result", "code_execution_tool_result"}, openType)
			switch openType {
			case "text":
				if event.data.Get("content_block.citations").Exists() {
					require.True(t, event.data.Get("content_block.citations").IsArray())
					require.Equal(t, int64(0), event.data.Get("content_block.citations.#").Int())
					openCitedText = true
				}
			case "thinking":
				require.Empty(t, event.data.Get("content_block.thinking").String())
			case "redacted_thinking":
				requireBase64OpaqueValue(t, event.data.Get("content_block.data").String())
			case "server_tool_use":
				toolID := event.data.Get("content_block.id").String()
				require.True(t, strings.HasPrefix(toolID, "srvtoolu_"), "server tool ID must use Anthropic namespace")
				require.Equal(t, "web_search", event.data.Get("content_block.name").String())
				serverToolIDs[toolID] = struct{}{}
			case "tool_use":
				require.Equal(t, "direct", event.data.Get("content_block.caller.type").String())
			case "web_search_tool_result":
				require.Equal(t, "direct", event.data.Get("content_block.caller.type").String())
				toolID := event.data.Get("content_block.tool_use_id").String()
				_, paired := serverToolIDs[toolID]
				require.True(t, paired, "web-search result must reference a preceding server tool use")
				requireWebSearchToolResultContent(t, event.data.Get("content_block.content"))
			}
			lastDeltaType = ""
		case "content_block_delta":
			require.NotEqual(t, int64(-1), openIndex, "delta requires an open content block")
			require.Equal(t, openIndex, event.data.Get("index").Int())
			deltaType := event.data.Get("delta.type").String()
			switch openType {
			case "text":
				require.Contains(t, []string{"text_delta", "citations_delta"}, deltaType)
				if deltaType == "citations_delta" {
					require.True(t, openCitedText, "citation delta requires citations: [] on content_block_start")
					require.False(t, openTextSeen, "citation delta must precede the cited text")
					citation := event.data.Get("delta.citation")
					require.Equal(t, "web_search_result_location", citation.Get("type").String())
					require.NotEmpty(t, citation.Get("url").String())
					require.NotEmpty(t, citation.Get("title").String())
					require.NotEmpty(t, citation.Get("cited_text").String())
					requireBase64OpaqueValue(t, citation.Get("encrypted_index").String())
				} else {
					openTextSeen = true
				}
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
			if openCitedText {
				require.Equal(t, "text_delta", lastDeltaType, "cited text block must end with text")
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

func requireWebSearchToolResultContent(t *testing.T, content gjson.Result) {
	t.Helper()
	if content.IsArray() {
		for _, result := range content.Array() {
			require.Equal(t, "web_search_result", result.Get("type").String())
			require.NotEmpty(t, result.Get("title").String())
			require.NotEmpty(t, result.Get("url").String())
			requireBase64OpaqueValue(t, result.Get("encrypted_content").String())
		}
		return
	}
	require.Equal(t, "web_search_tool_result_error", content.Get("type").String())
	require.Contains(t, []string{
		WebSearchErrorInvalidToolInput,
		WebSearchErrorUnavailable,
		WebSearchErrorMaxUsesExceeded,
		WebSearchErrorTooManyRequests,
		WebSearchErrorQueryTooLong,
		WebSearchErrorRequestTooLarge,
	}, content.Get("error_code").String())
}

func requireBase64OpaqueValue(t *testing.T, value string) {
	t.Helper()
	require.NotEmpty(t, value)
	_, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		_, err = base64.RawStdEncoding.DecodeString(value)
	}
	require.NoError(t, err, "opaque protocol field must be valid base64")
}

func TestAnthropicProtocolComplianceThinkingTextStream(t *testing.T) {
	providerSignature := providerThinkingSignatureFixture(t, true)
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "inspect protocol", "signature": providerSignature},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "final answer"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-8", 11, KiroRequestContext{
		ThinkingEnabled:                  true,
		RequireProviderThinkingSignature: true,
		RequireTerminalEvent:             true,
	})
	require.NoError(t, err)
	requireAnthropicSSEProtocolLifecycle(t, out.String())
	require.NotContains(t, out.String(), "event: ping")
	require.NotContains(t, out.String(), `"context_management"`)
}

func TestAnthropicProtocolComplianceClaudeCodeResponseHints(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Anthropic-Beta", "claude-code-20250219, context-management-2025-06-27")
	buildResult, err := BuildKiroPayloadWithContext(
		[]byte(`{"model":"claude-opus-4-8","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`),
		"claude-opus-4-8", "", "AI_EDITOR", headers,
	)
	require.NoError(t, err)
	require.True(t, buildResult.Context.EmitProtocolPing)
	require.True(t, buildResult.Context.ReportUsageIterations)
	require.True(t, buildResult.Context.ReportContextManagement)

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "hello"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))

	requestCtx := buildResult.Context
	requestCtx.RequireTerminalEvent = true
	var out bytes.Buffer
	_, err = StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-8", 4, requestCtx)
	require.NoError(t, err)
	requireAnthropicSSEProtocolLifecycle(t, out.String())

	events := parseAnthropicSSEProtocolEvents(t, out.String())
	require.GreaterOrEqual(t, len(events), 6)
	require.Equal(t, "message_start", events[0].name)
	require.Equal(t, int64(1), events[0].data.Get("message.usage.output_tokens").Int())
	require.Equal(t, "content_block_start", events[1].name)
	require.Equal(t, "ping", events[2].name)
	pingCount := 0
	for _, event := range events {
		if event.name == "ping" {
			pingCount++
		}
	}
	require.Equal(t, 1, pingCount)
	messageDelta := events[len(events)-2]
	require.Equal(t, "message_delta", messageDelta.name)
	require.True(t, messageDelta.data.Get("context_management.applied_edits").IsArray())
	require.Equal(t, int64(0), messageDelta.data.Get("context_management.applied_edits.#").Int())
	require.False(t, messageDelta.data.Get("usage.cache_creation").Exists())
	require.True(t, messageDelta.data.Get("usage.iterations").IsArray())
	require.Equal(t, int64(1), messageDelta.data.Get("usage.iterations.#").Int())
	require.Equal(t, "message", messageDelta.data.Get("usage.iterations.0.type").String())
	require.True(t, messageDelta.data.Get("usage.iterations.0.cache_creation").Exists())
	require.Equal(t, messageDelta.data.Get("usage.input_tokens").Int(), messageDelta.data.Get("usage.iterations.0.input_tokens").Int())
	require.Equal(t, messageDelta.data.Get("usage.output_tokens").Int(), messageDelta.data.Get("usage.iterations.0.output_tokens").Int())
}

func TestBuildKiroPayloadProtocolResponseHintsRequireExactBetaTokens(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Anthropic-Beta", "claude-code-20250219-rev2,context-management-2025-06-27-rev2")
	buildResult, err := BuildKiroPayloadWithContext(
		[]byte(`{"model":"claude-opus-4-8","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`),
		"claude-opus-4-8", "", "AI_EDITOR", headers,
	)
	require.NoError(t, err)
	require.False(t, buildResult.Context.EmitProtocolPing)
	require.False(t, buildResult.Context.ReportUsageIterations)
	require.False(t, buildResult.Context.ReportContextManagement)
}

func TestAnthropicProtocolComplianceWebSearchServerBlocks(t *testing.T) {
	snippet := "Protocol result"
	var wire strings.Builder
	writeProtocolEventForTest(t, &wire, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_010123456789ABCDEFGHIJKL", "type": "message", "role": "assistant", "content": []any{},
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
	require.Equal(t, "server_tool_use", events[1].data.Get("content_block.type").String())
	require.Equal(t, "srvtoolu_protocol", events[1].data.Get("content_block.id").String())
	require.Equal(t, "srvtoolu_protocol", events[4].data.Get("content_block.tool_use_id").String())
	require.Equal(t, "direct", events[4].data.Get("content_block.caller.type").String())
	require.NotEmpty(t, events[4].data.Get("content_block.content.0.encrypted_content").String())
}

func TestAnthropicProtocolComplianceFinalizedWebSearchStreamWithCitations(t *testing.T) {
	snippet := "The official Go documentation describes goroutines and channels."
	results := &WebSearchResults{Results: []WebSearchResult{{
		Title: "Effective Go", URL: "https://go.dev/doc/effective_go#concurrency", Snippet: &snippet,
	}}}
	searches := []SearchIndicator{{ToolUseID: "srvtoolu_protocol_full", Query: "Go concurrency", Results: results}}

	upstream := bytes.NewBuffer(nil)
	_, _ = upstream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "See https://go.dev/doc/effective_go#concurrency"},
	}))
	_, _ = upstream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))
	var translated bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), upstream, &translated, "claude-opus-5", 11, KiroRequestContext{
		RequireTerminalEvent: true,
	})
	require.NoError(t, err)

	var wire strings.Builder
	writeProtocolEventForTest(t, &wire, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_01NOPQRSTUVWXYZabcdefghi", "type": "message", "role": "assistant", "content": []any{},
			"model": "claude-opus-5", "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 11, "output_tokens": 0},
		},
	})
	for _, event := range GenerateSearchIndicatorEvents("Go concurrency", "srvtoolu_protocol_full", results, 0) {
		_, _ = wire.Write(event)
	}
	translatedFrames := splitSSEFrames([][]byte{translated.Bytes()})
	for _, event := range FinalizeWebSearchSSEChunks(translatedFrames, 2, 1, searches) {
		_, _ = wire.Write(event)
	}

	requireAnthropicSSEProtocolLifecycle(t, wire.String())
	events := parseAnthropicSSEProtocolEvents(t, wire.String())
	citationDeltas := 0
	for _, event := range events {
		if event.data.Get("delta.type").String() == "citations_delta" {
			citationDeltas++
		}
	}
	require.Equal(t, 1, citationDeltas)
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
