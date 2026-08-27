package kiro

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetadataOnlyEOFClassificationStreamingAndNonStreaming(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "contextUsageEvent", map[string]any{
		"contextUsageEvent": map[string]any{"contextUsagePercentage": 6.17},
	}))
	raw := append([]byte(nil), stream.Bytes()...)

	_, err := ParseNonStreamingEventStreamWithContext(bytes.NewReader(raw), "claude-opus-5", KiroRequestContext{RequireTerminalEvent: true})
	require.Error(t, err)
	require.True(t, IsMetadataOnlyIncompleteStream(err))
	var incomplete *IncompleteStreamError
	require.True(t, errors.As(err, &incomplete))
	require.Equal(t, IncompleteStreamReasonMetadataOnlyEOF, incomplete.Reason)

	var out bytes.Buffer
	_, err = StreamEventStreamAsAnthropicWithContext(context.Background(), bytes.NewReader(raw), &out, "claude-opus-5", 100, KiroRequestContext{RequireTerminalEvent: true})
	require.Error(t, err)
	require.True(t, IsMetadataOnlyIncompleteStream(err))
	require.Empty(t, out.String(), "metadata-only failure must be classified before client-visible bytes")
}

func TestIncompleteStreamReasonsDoNotConflateTransportAndToolTruncation(t *testing.T) {
	t.Run("empty EOF", func(t *testing.T) {
		_, err := ParseNonStreamingEventStreamWithContext(bytes.NewReader(nil), "claude-opus-5", KiroRequestContext{RequireTerminalEvent: true})
		require.Error(t, err)
		var incomplete *IncompleteStreamError
		require.True(t, errors.As(err, &incomplete))
		require.Equal(t, IncompleteStreamReasonEmptyEOF, incomplete.Reason)
		require.False(t, IsMetadataOnlyIncompleteStream(err))
	})

	t.Run("unfinished tool input", func(t *testing.T) {
		stream := bytes.NewBuffer(nil)
		_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
			"toolUseEvent": map[string]any{
				"toolUseId": "toolu_partial",
				"name":      "Write",
				"input":     `{"path":"/tmp/a"}`,
				"stop":      false,
			},
		}))
		_, err := ParseNonStreamingEventStreamWithContext(stream, "claude-opus-5", KiroRequestContext{RequireTerminalEvent: true})
		require.Error(t, err)
		var incomplete *IncompleteStreamError
		require.True(t, errors.As(err, &incomplete))
		require.Equal(t, IncompleteStreamReasonMissingTerminal, incomplete.Reason)
		require.False(t, IsMetadataOnlyIncompleteStream(err))
	})
}
