package handler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestWebChatHandleUpstreamSSEEventWritesDelta(t *testing.T) {
	h := &WebChatHandler{}
	var out bytes.Buffer
	var builder strings.Builder

	done, err := h.handleUpstreamSSEEvent(&out, []string{
		`data: {"choices":[{"delta":{"content":"hello"}}]}`,
	}, &builder)

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

	done, err := h.handleUpstreamSSEEvent(&out, []string{"data: [DONE]"}, &builder)

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
	}, &builder)

	require.ErrorContains(t, err, "upstream unavailable")
	require.False(t, done)
	require.Empty(t, builder.String())
	require.Empty(t, out.String())
}

func TestWebChatBuildUpstreamPayloadUsesMessagesForClaudeLikePlatforms(t *testing.T) {
	h := &WebChatHandler{}
	targetURL, payload, useAnthropicMessages, err := h.buildUpstreamPayload(service.PlatformAnthropic, "claude-opus-4-6", []service.OpenAIChatMessage{
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
}

func TestWebChatBuildUpstreamPayloadUsesChatCompletionsForOpenAI(t *testing.T) {
	h := &WebChatHandler{}
	targetURL, payload, useAnthropicMessages, err := h.buildUpstreamPayload(service.PlatformOpenAI, "gpt-5.5", []service.OpenAIChatMessage{
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
