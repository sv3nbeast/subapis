package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stableChunkReader struct {
	chunks [][]byte
	index  int
}

func (r *stableChunkReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.index]
	r.index++
	copy(p, chunk)
	return len(chunk), nil
}

type stableFlushWriter struct {
	bytes.Buffer
	flushes int
}

func (w *stableFlushWriter) Flush() error {
	w.flushes++
	return nil
}

type stableCancelAfterChunkReader struct {
	chunk  []byte
	cancel context.CancelFunc
	reads  int
}

func (r *stableCancelAfterChunkReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, io.EOF
	}
	n := copy(p, r.chunk)
	r.cancel()
	return n, io.EOF
}

func TestCopyAnthropicStableResponsePreservesRawSSEAndObservesUsage(t *testing.T) {
	chunks := [][]byte{
		[]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":12}}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"),
		[]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7,\"cache_read_input_tokens\":4}}\n\n"),
		[]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
	}
	original := bytes.Join(chunks, nil)
	nowValue := time.Unix(100, 0)
	now := func() time.Time {
		value := nowValue
		nowValue = nowValue.Add(time.Millisecond)
		return value
	}
	observer := NewAnthropicStableSSEObserver(now)
	writer := &stableFlushWriter{}
	metrics, err := CopyAnthropicStableResponse(
		context.Background(), writer, &stableChunkReader{chunks: chunks}, true, writer.Flush, observer,
	)
	require.NoError(t, err)
	require.Equal(t, string(original), writer.String())
	require.Equal(t, len(chunks), writer.flushes)
	require.True(t, metrics.TerminalSeen)
	require.False(t, metrics.ErrorEventSeen)
	require.Equal(t, int64(12), metrics.InputTokens)
	require.Equal(t, int64(7), metrics.OutputTokens)
	require.Equal(t, int64(4), metrics.CacheReadTokens)
	require.Equal(t, "msg_1", metrics.UpstreamRequestID)
	require.False(t, metrics.FirstDownstreamWriteAt.IsZero())
	require.False(t, metrics.FirstDownstreamFlushAt.IsZero())
	require.False(t, metrics.FirstSemanticOutputAt.IsZero())
	require.True(t, metrics.FirstSemanticOutputAt.After(metrics.FirstDownstreamFlushAt) || metrics.FirstSemanticOutputAt.Equal(metrics.FirstDownstreamFlushAt))
}

func TestCopyAnthropicStableResponseRejectsEOFBeforeTerminal(t *testing.T) {
	raw := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
	var writer bytes.Buffer
	metrics, err := CopyAnthropicStableResponse(
		context.Background(), &writer, bytes.NewReader(raw), true, nil, NewAnthropicStableSSEObserver(nil),
	)
	require.ErrorIs(t, err, ErrAnthropicStableResponseTruncated)
	require.False(t, metrics.TerminalSeen)
	require.Equal(t, string(raw), writer.String())
}

func TestCopyAnthropicStableResponsePreservesErrorEventAndReturnsSentinel(t *testing.T) {
	raw := []byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n")
	var writer bytes.Buffer
	metrics, err := CopyAnthropicStableResponse(
		context.Background(), &writer, bytes.NewReader(raw), true, nil, NewAnthropicStableSSEObserver(nil),
	)
	require.ErrorIs(t, err, ErrAnthropicStableResponseErrorEvent)
	require.True(t, metrics.TerminalSeen)
	require.True(t, metrics.ErrorEventSeen)
	require.Equal(t, string(raw), writer.String(), "an upstream error event is already a client-visible raw SSE frame")
}

func TestCopyAnthropicStableResponseRejectsDuplicateTerminalWithoutChangingRawBytes(t *testing.T) {
	raw := []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	var writer bytes.Buffer
	metrics, err := CopyAnthropicStableResponse(
		context.Background(), &writer, bytes.NewReader(raw), true, nil, NewAnthropicStableSSEObserver(nil),
	)
	require.ErrorIs(t, err, ErrAnthropicStableResponseInvalidTerminal)
	require.Equal(t, 2, metrics.TerminalCount)
	require.True(t, metrics.EventAfterTerminal)
	require.Equal(t, string(raw), writer.String())
}

func TestCopyAnthropicStableResponseRejectsOpenAIDoneAsAnthropicTerminal(t *testing.T) {
	raw := []byte("data: [DONE]\n\n")
	var writer bytes.Buffer
	metrics, err := CopyAnthropicStableResponse(
		context.Background(), &writer, bytes.NewReader(raw), true, nil, NewAnthropicStableSSEObserver(nil),
	)
	require.ErrorIs(t, err, ErrAnthropicStableResponseTruncated)
	require.Zero(t, metrics.TerminalCount)
	require.Equal(t, string(raw), writer.String())
}

func TestCopyAnthropicStableResponseFailsClosedWhenObserverDropsBytes(t *testing.T) {
	raw := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"")
	raw = append(raw, bytes.Repeat([]byte("x"), 1024)...)
	raw = append(raw, []byte("\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")...)
	observer := NewAnthropicStableSSEObserver(nil)
	observer.maxBytes = 128
	var writer bytes.Buffer
	metrics, err := CopyAnthropicStableResponse(context.Background(), &writer, bytes.NewReader(raw), true, nil, observer)
	require.ErrorIs(t, err, ErrAnthropicStableResponseObservationIncomplete)
	require.Positive(t, metrics.ObserverDroppedBytes)
	require.Equal(t, raw, writer.Bytes())
}

func TestCopyAnthropicStableResponseUsesEarliestMixedLineSeparator(t *testing.T) {
	raw := []byte("event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_mixed\"}}\r\n\r\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: message_stop\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n")
	var writer bytes.Buffer

	metrics, err := CopyAnthropicStableResponse(
		context.Background(), &writer, bytes.NewReader(raw), true, nil, NewAnthropicStableSSEObserver(nil),
	)

	require.NoError(t, err)
	require.Equal(t, raw, writer.Bytes())
	require.Equal(t, "msg_mixed", metrics.UpstreamRequestID)
	require.Equal(t, 1, metrics.TerminalCount)
	require.False(t, metrics.FirstSemanticOutputAt.IsZero())
}

func TestCopyAnthropicStableResponseObservesLargeToolDeltaWithoutChangingBytes(t *testing.T) {
	partialJSON := strings.Repeat("x", 128<<10)
	raw := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"" + partialJSON + "\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	var writer bytes.Buffer

	metrics, err := CopyAnthropicStableResponse(
		context.Background(), &writer, bytes.NewReader(raw), true, nil, NewAnthropicStableSSEObserver(nil),
	)

	require.NoError(t, err)
	require.Equal(t, raw, writer.Bytes())
	require.Zero(t, metrics.ObserverDroppedBytes)
	require.False(t, metrics.FirstSemanticOutputAt.IsZero())
	require.Equal(t, 1, metrics.TerminalCount)
}

func TestCopyAnthropicStableResponsePreservesThinkingTextAndToolInterleaving(t *testing.T) {
	raw := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_mixed_content\",\"usage\":{\"input_tokens\":10}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"/tmp/a\\\"}\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":9,\"cache_creation_input_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	var writer bytes.Buffer

	metrics, err := CopyAnthropicStableResponse(
		context.Background(), &writer, bytes.NewReader(raw), true, nil, NewAnthropicStableSSEObserver(nil),
	)

	require.NoError(t, err)
	require.Equal(t, raw, writer.Bytes())
	require.True(t, metrics.TerminalSeen)
	require.Equal(t, 1, metrics.TerminalCount)
	require.False(t, metrics.FirstSemanticOutputAt.IsZero())
	require.Equal(t, int64(10), metrics.InputTokens)
	require.Equal(t, int64(9), metrics.OutputTokens)
	require.Equal(t, int64(3), metrics.CacheCreationTokens)
}

func TestCopyAnthropicStableResponseReturnsClientWriteErrorWithoutRetry(t *testing.T) {
	writeErr := errors.New("client disconnected")
	writer := stableErrorWriter{err: writeErr}
	raw := []byte("event: message_start\ndata: {}\n\n")
	metrics, err := CopyAnthropicStableResponse(
		context.Background(), writer, bytes.NewReader(raw), true, nil, NewAnthropicStableSSEObserver(nil),
	)
	require.ErrorIs(t, err, writeErr)
	require.Zero(t, metrics.DownstreamBytes)
}

func TestCopyAnthropicStableResponseCancellationAfterBlockedReadIsNotTruncation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	raw := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
	reader := &stableCancelAfterChunkReader{chunk: raw, cancel: cancel}
	writer := &stableFlushWriter{}

	metrics, err := CopyAnthropicStableResponse(
		ctx, writer, reader, true, writer.Flush, NewAnthropicStableSSEObserver(nil),
	)

	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, ErrAnthropicStableResponseTruncated)
	require.Equal(t, string(raw), writer.String(), "bytes returned with cancellation must still be relayed once")
	require.Equal(t, int64(len(raw)), metrics.UpstreamBytes)
	require.Equal(t, int64(len(raw)), metrics.DownstreamBytes)
	require.False(t, metrics.TerminalSeen)
	require.Equal(t, 1, writer.flushes)
}

type stableErrorWriter struct{ err error }

func (w stableErrorWriter) Write([]byte) (int, error) { return 0, w.err }
