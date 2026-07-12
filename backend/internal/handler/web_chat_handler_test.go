package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestWebChatHandleUpstreamSSEEventWritesDelta(t *testing.T) {
	h := &WebChatHandler{}
	var out bytes.Buffer
	var builder strings.Builder

	done, err := h.handleUpstreamSSEEvent(&out, []string{
		`data: {"choices":[{"delta":{"content":"hello"}}]}`,
	}, &builder, &service.WebChatUsage{})

	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, "hello", builder.String())
	require.Contains(t, out.String(), "event: delta")
	require.Contains(t, out.String(), `"text":"hello"`)
}

func TestWebChatHandleUpstreamSSEEventDone(t *testing.T) {
	h := &WebChatHandler{}
	var out bytes.Buffer
	var builder strings.Builder

	done, err := h.handleUpstreamSSEEvent(&out, []string{"data: [DONE]"}, &builder, &service.WebChatUsage{})

	require.NoError(t, err)
	require.True(t, done)
	require.Empty(t, builder.String())
	require.Empty(t, out.String())
}

func TestWebChatHandleUpstreamSSEEventError(t *testing.T) {
	h := &WebChatHandler{}
	var out bytes.Buffer
	var builder strings.Builder

	done, err := h.handleUpstreamSSEEvent(&out, []string{
		`data: {"error":{"message":"upstream unavailable"}}`,
	}, &builder, &service.WebChatUsage{})

	require.ErrorContains(t, err, "upstream unavailable")
	require.False(t, done)
	require.Empty(t, builder.String())
	require.Empty(t, out.String())
}

func TestWebChatBuildUpstreamPayloadUsesMessagesForClaudeLikePlatforms(t *testing.T) {
	h := &WebChatHandler{}
	targetURL, payload, useAnthropicMessages, err := h.buildUpstreamPayload(&service.WebChatSession{Platform: service.PlatformAnthropic, Model: "claude-opus-4-6", MaxOutputTokens: 8192}, []service.OpenAIChatMessage{
		{Role: "user", Content: "hello"},
	})

	require.NoError(t, err)
	require.True(t, useAnthropicMessages)
	require.Contains(t, targetURL, "/v1/messages")

	var req map[string]any
	require.NoError(t, json.Unmarshal(payload, &req))
	require.Equal(t, "claude-opus-4-6", req["model"])
	require.Equal(t, true, req["stream"])
	require.Contains(t, req, "max_tokens")
	require.Contains(t, req, "messages")
	require.Equal(t, "ephemeral", gjson.GetBytes(payload, "messages.0.content.0.cache_control.type").String())
	require.Equal(t, "5m", gjson.GetBytes(payload, "messages.0.content.0.cache_control.ttl").String())
}

func TestWebChatBuildUpstreamPayloadUsesChatCompletionsForOpenAI(t *testing.T) {
	h := &WebChatHandler{}
	temperature := 0.4
	targetURL, payload, useAnthropicMessages, err := h.buildUpstreamPayload(&service.WebChatSession{Platform: service.PlatformOpenAI, Model: "gpt-5.5", SystemPrompt: "be concise", Temperature: &temperature, MaxOutputTokens: 1234}, []service.OpenAIChatMessage{
		{Role: "user", Content: "hello"},
	})

	require.NoError(t, err)
	require.False(t, useAnthropicMessages)
	require.Contains(t, targetURL, "/v1/chat/completions")

	var req map[string]any
	require.NoError(t, json.Unmarshal(payload, &req))
	require.Equal(t, "gpt-5.5", req["model"])
	require.Equal(t, true, req["stream"])
	require.Contains(t, req, "messages")
	require.Equal(t, float64(1234), req["max_tokens"])
	require.Equal(t, 0.4, req["temperature"])
	require.Equal(t, true, gjson.GetBytes(payload, "stream_options.include_usage").Bool())
	require.Equal(t, "system", gjson.GetBytes(payload, "messages.0.role").String())
}

func TestWebChatOpenAIStreamCollectsUsage(t *testing.T) {
	h := &WebChatHandler{}
	body := strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":4,\"prompt_tokens_details\":{\"cached_tokens\":12}}}\n\ndata: [DONE]\n\n")
	var output bytes.Buffer
	result, err := h.forwardOpenAIChatCompletionsStream(&output, body)
	require.NoError(t, err)
	require.Equal(t, "hello", result.Content)
	require.Equal(t, int64(20), result.Usage.InputTokens)
	require.Equal(t, int64(4), result.Usage.OutputTokens)
	require.Equal(t, int64(12), result.Usage.CacheReadTokens)
}

func TestWebChatOpenAIStreamMarksDisconnectPartial(t *testing.T) {
	h := &WebChatHandler{}
	result, err := h.forwardOpenAIChatCompletionsStream(io.Discard, strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, "partial", result.Content)
}

func TestExtractAnthropicStreamText(t *testing.T) {
	text, done, errText := extractAnthropicStreamText(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`)
	require.Equal(t, "hello", text)
	require.False(t, done)
	require.Empty(t, errText)

	text, done, errText = extractAnthropicStreamText(`{"type":"message_stop"}`)
	require.Empty(t, text)
	require.True(t, done)
	require.Empty(t, errText)
}

func TestWebChatAnthropicUsageMerge(t *testing.T) {
	var usage service.WebChatUsage
	mergeAnthropicStreamUsage(`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":8,"cache_creation_input_tokens":2}}}`, &usage)
	mergeAnthropicStreamUsage(`{"type":"message_delta","usage":{"output_tokens":6}}`, &usage)
	require.Equal(t, service.WebChatUsage{InputTokens: 10, OutputTokens: 6, CacheReadTokens: 8, CacheCreationTokens: 2}, usage)
}
