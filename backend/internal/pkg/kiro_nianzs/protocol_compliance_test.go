package kiro

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
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
	require.Equal(t, int64(4), events[0].data.Get("message.usage.input_tokens").Int())
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

	frames := strings.Split(strings.TrimSuffix(out.String(), "\n\n"), "\n\n")
	require.GreaterOrEqual(t, len(frames), 7)
	require.Contains(t, frames[0], `data: {"type":"message_start","message":{"model":"claude-opus-4-8","id":"msg_01`)
	require.Contains(t, frames[1], `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}`)
	require.Equal(t, "event: ping\ndata: {\"type\": \"ping\"}", frames[2])
	require.Contains(t, frames[3], `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"h"}`)
	require.Contains(t, frames[4], `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ello"}`)
	for _, frameIndex := range []int{0, 1, 3, 4, 5, len(frames) - 1} {
		payload := strings.TrimPrefix(strings.SplitN(frames[frameIndex], "\ndata: ", 2)[1], "")
		require.LessOrEqual(t, len(payload)-len(strings.TrimRight(payload[:len(payload)-1], " "))-1, 15)
		require.True(t, gjson.Valid(payload))
	}
}

func TestClaudeMessageIDMatchesNativeUUIDv7Base58Shape(t *testing.T) {
	capturedUUID := uuid.MustParse("019ffb6d-5765-7225-8609-85724765f589")
	require.Equal(t, "1CdzsPyXJq5Uutscj7ZULG", encodeClaudeMessageID(capturedUUID))

	id := newClaudeMessageID()
	require.Regexp(t, `^msg_01[1-9A-HJ-NP-Za-km-z]{22}$`, id)

	decoded := big.NewInt(0)
	base := big.NewInt(58)
	for _, char := range strings.TrimPrefix(id, "msg_01") {
		index := strings.IndexRune("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz", char)
		require.NotEqual(t, -1, index)
		decoded.Mul(decoded, base)
		decoded.Add(decoded, big.NewInt(int64(index)))
	}
	raw := decoded.FillBytes(make([]byte, 16))
	require.Equal(t, byte(7), raw[6]>>4)
	require.Equal(t, byte(2), raw[8]>>6)
}

func TestAnthropicProtocolComplianceClaudeCodePureTextFingerprintShape(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "N43QRR"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 10,
				"outputTokens":        8,
				"totalTokens":         18,
			},
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(
		context.Background(), stream, &out, "claude-opus-5", 10,
		KiroRequestContext{
			EmitProtocolPing:      true,
			ReportUsageIterations: true,
			RequireTerminalEvent:  true,
		},
	)
	require.NoError(t, err)
	events := parseAnthropicSSEProtocolEvents(t, out.String())
	var textParts []string
	for _, event := range events {
		if event.name == "content_block_delta" && event.data.Get("delta.type").String() == "text_delta" {
			textParts = append(textParts, event.data.Get("delta.text").String())
		}
	}
	require.Equal(t, []string{"N", "43QRR"}, textParts)
	messageDelta := events[len(events)-2].data
	require.Equal(t, int64(7), messageDelta.Get("usage.output_tokens").Int())
	require.Equal(t, int64(7), messageDelta.Get("usage.iterations.0.output_tokens").Int())
}

func TestAnthropicProtocolComplianceClaudeCodeSimulatedUsageFraming(t *testing.T) {
	tests := []struct {
		name             string
		simulated        Usage
		adaptiveThinking bool
		explicitEffort   bool
		wantInput        int64
		wantCache        int64
	}{
		{
			name:      "bare message",
			simulated: Usage{InputTokens: 12},
			wantInput: 11,
		},
		{
			name: "cached Claude Code prefix",
			simulated: Usage{
				InputTokens:                82,
				CacheCreationInputTokens:   34246,
				CacheCreation5mInputTokens: 34246,
			},
			wantInput: 79,
			wantCache: 34246,
		},
		{
			name: "implicit effort adaptive stream removes framing tokens",
			simulated: Usage{
				InputTokens:                133,
				CacheCreationInputTokens:   34246,
				CacheCreation5mInputTokens: 34246,
			},
			adaptiveThinking: true,
			wantInput:        130,
			wantCache:        34246,
		},
		{
			name:             "uncached implicit effort adaptive stream remains authoritative",
			simulated:        Usage{InputTokens: 133},
			adaptiveThinking: true,
			wantInput:        133,
		},
		{
			name: "explicit effort adaptive stream keeps thinking-aware estimate",
			simulated: Usage{
				InputTokens:                498,
				CacheCreationInputTokens:   34246,
				CacheCreation5mInputTokens: 34246,
			},
			adaptiveThinking: true,
			explicitEffort:   true,
			wantInput:        498,
			wantCache:        34246,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := bytes.NewBuffer(nil)
			_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
				"assistantResponseEvent": map[string]any{"content": "2"},
			}))
			_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
				"messageStopEvent": map[string]any{"stopReason": "end_turn"},
			}))

			requestCtx := KiroRequestContext{
				EmitProtocolPing:                  true,
				ReportUsageIterations:             true,
				RequireTerminalEvent:              true,
				CacheEmulationUsage:               &tt.simulated,
				SuppressAdaptiveThinkingText:      tt.adaptiveThinking,
				AdaptiveThinkingHasExplicitEffort: tt.explicitEffort,
			}
			var out bytes.Buffer
			_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-5", 999, requestCtx)
			require.NoError(t, err)
			events := parseAnthropicSSEProtocolEvents(t, out.String())
			start := events[0].data
			delta := events[len(events)-2].data
			require.Equal(t, tt.wantInput, start.Get("message.usage.input_tokens").Int())
			require.Equal(t, tt.wantInput, delta.Get("usage.input_tokens").Int())
			require.Equal(t, tt.wantInput, delta.Get("usage.iterations.0.input_tokens").Int())
			require.Equal(t, tt.wantCache, start.Get("message.usage.cache_creation_input_tokens").Int())
			require.Equal(t, tt.wantCache, delta.Get("usage.cache_creation_input_tokens").Int())
		})
	}
}

func TestAnthropicProtocolComplianceDelayedMessageStartNormalizesUsageOnce(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(
		context.Background(), stream, &out, "claude-opus-5", 999,
		KiroRequestContext{
			EmitProtocolPing:             true,
			ReportUsageIterations:        true,
			RequireTerminalEvent:         true,
			SuppressAdaptiveThinkingText: true,
			CacheEmulationUsage: &Usage{
				InputTokens:                133,
				CacheCreationInputTokens:   34246,
				CacheCreation5mInputTokens: 34246,
			},
		},
	)
	require.NoError(t, err)
	events := parseAnthropicSSEProtocolEvents(t, out.String())
	require.Equal(t, []string{"message_start", "message_delta", "message_stop"}, []string{
		events[0].name, events[1].name, events[2].name,
	})
	require.Equal(t, int64(130), events[0].data.Get("message.usage.input_tokens").Int())
	require.Equal(t, int64(130), events[1].data.Get("usage.input_tokens").Int())
	require.Equal(t, int64(130), events[1].data.Get("usage.iterations.0.input_tokens").Int())
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

func TestBuildKiroPayloadTracksExplicitAdaptiveEffort(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Anthropic-Beta", "claude-code-20250219")
	for _, tt := range []struct {
		name string
		body string
		want bool
	}{
		{
			name: "implicit effort",
			body: `{"model":"claude-opus-5","max_tokens":128,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name: "explicit medium effort",
			body: `{"model":"claude-opus-5","max_tokens":128,"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"},"messages":[{"role":"user","content":"hello"}]}`,
			want: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := BuildKiroPayloadWithContext([]byte(tt.body), "claude-opus-5", "", "AI_EDITOR", headers)
			require.NoError(t, err)
			require.True(t, result.Context.SuppressAdaptiveThinkingText)
			require.Equal(t, tt.want, result.Context.AdaptiveThinkingHasExplicitEffort)
		})
	}
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

func TestAnthropicProtocolComplianceNativeWireDoesNotHTMLEscapeText(t *testing.T) {
	payload, err := marshalAnthropicStreamEvent("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type": "text_delta",
			"text": "<17f5e708a52422d2>&",
		},
	}, true)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"text":"<17f5e708a52422d2>&"`)
	require.NotContains(t, string(payload), `\u003c`)
	require.NotContains(t, string(payload), `\u003e`)
	require.NotContains(t, string(payload), `\u0026`)

	legacyPayload, err := marshalAnthropicStreamEvent("content_block_delta", map[string]any{
		"text": "<marker>&",
	}, false)
	require.NoError(t, err)
	require.Contains(t, string(legacyPayload), `\u003cmarker\u003e\u0026`)
}

func TestAnthropicProtocolComplianceNativeNonStreamingWireShape(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "2"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{
			"uncachedInputTokens": 82, "outputTokens": 3,
		}},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-opus-5", KiroRequestContext{
		EmitProtocolPing:             true,
		SuppressAdaptiveThinkingText: true,
		CacheEmulationUsage: &Usage{
			InputTokens:          82,
			CacheReadInputTokens: 34246,
		},
	})
	require.NoError(t, err)
	wire := string(result.ResponseBody)
	require.Equal(t,
		`{"id":"`+gjson.GetBytes(result.ResponseBody, "id").String()+`","type":"message","role":"assistant","content":[{"text":"2","type":"text"}],"model":"claude-opus-5","stop_reason":"end_turn","usage":{"input_tokens":79,"output_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":34246}}`,
		wire,
	)
	require.NotContains(t, wire, `"stop_details"`)
	require.NotContains(t, wire, `"stop_sequence"`)
	require.NotContains(t, wire, `"cache_creation"`)
	require.NotContains(t, wire, `"service_tier"`)
	require.NotContains(t, wire, `"inference_geo"`)
}
