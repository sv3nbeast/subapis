package handler

import (
	"bytes"
	"strings"
	"testing"

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
