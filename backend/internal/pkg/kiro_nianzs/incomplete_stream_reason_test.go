package kiro

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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

func TestNonStreamingFrameErrorsRetainStructuralCause(t *testing.T) {
	_, err := ParseNonStreamingEventStreamWithContext(bytes.NewReader([]byte{0, 0, 0}), "claude-opus-5", KiroRequestContext{RequireTerminalEvent: true})
	require.Error(t, err)
	var parseErr *EventStreamParseError
	require.ErrorAs(t, err, &parseErr)
	require.Equal(t, "unexpected_eof", parseErr.Class)
	require.Zero(t, parseErr.FrameCount)
	require.Zero(t, parseErr.DecodedFrameCount)
	require.False(t, parseErr.HasCompletionEvidence)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestNonStreamingIgnoresPartialFrameAfterCompletion(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "complete answer"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))
	_, _ = stream.Write([]byte{0, 0, 0})

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-opus-5", KiroRequestContext{RequireTerminalEvent: true})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)
	require.Equal(t, "complete answer", gjson.GetBytes(result.ResponseBody, "content.0.text").String())
}

func TestNonStreamingDoesNotTreatUsageAsDefinitiveBeforePartialFrame(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "not yet terminal"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "usageEvent", map[string]any{
		"usageEvent": map[string]any{"inputTokens": 10, "outputTokens": 4},
	}))
	_, _ = stream.Write([]byte{0, 0, 0})

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-opus-5", KiroRequestContext{RequireTerminalEvent: true})
	require.Nil(t, result)
	var parseErr *EventStreamParseError
	require.ErrorAs(t, err, &parseErr)
	require.Equal(t, "unexpected_eof", parseErr.Class)
	require.Equal(t, 2, parseErr.FrameCount)
	require.Equal(t, 2, parseErr.DecodedFrameCount)
	require.Equal(t, 1, parseErr.SemanticCandidateFrames)
	require.True(t, parseErr.HasCompletionEvidence)
}
