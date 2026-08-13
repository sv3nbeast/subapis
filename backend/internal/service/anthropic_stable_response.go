package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

var (
	ErrAnthropicStableResponseTruncated             = errors.New("stable upstream stream ended before a terminal event")
	ErrAnthropicStableResponseErrorEvent            = errors.New("stable upstream stream ended with an error event")
	ErrAnthropicStableResponseObservationIncomplete = errors.New("stable upstream stream terminal state could not be fully observed")
	ErrAnthropicStableResponseInvalidTerminal       = errors.New("stable upstream stream did not contain exactly one final terminal event")
)

type AnthropicStableResponseMetrics struct {
	FirstUpstreamByteAt    time.Time
	FirstDownstreamWriteAt time.Time
	FirstDownstreamFlushAt time.Time
	FirstSemanticOutputAt  time.Time
	TerminalAt             time.Time
	UpstreamBytes          int64
	DownstreamBytes        int64
	DownstreamError        bool
	TerminalSeen           bool
	TerminalCount          int
	EventAfterTerminal     bool
	ErrorEventSeen         bool
	ObserverDroppedBytes   int64
	InputTokens            int64
	OutputTokens           int64
	CacheReadTokens        int64
	CacheCreationTokens    int64
	UpstreamRequestID      string
}

// AnthropicStableSSEObserver is intentionally a side-channel parser.  It never
// hands modified data back to the response copier, so a malformed or future
// SSE event cannot change what the client receives.
type AnthropicStableSSEObserver struct {
	mu       sync.Mutex
	metrics  AnthropicStableResponseMetrics
	now      func() time.Time
	pending  []byte
	maxBytes int
}

func NewAnthropicStableSSEObserver(now func() time.Time) *AnthropicStableSSEObserver {
	if now == nil {
		now = time.Now
	}
	// pending normally contains only one incomplete SSE event. Keep it bounded,
	// but allow a large tool-input delta without degrading valid raw delivery into
	// an accounting-only observation failure.
	return &AnthropicStableSSEObserver{now: now, maxBytes: 8 << 20}
}

func (o *AnthropicStableSSEObserver) Metrics() AnthropicStableResponseMetrics {
	if o == nil {
		return AnthropicStableResponseMetrics{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.metrics
}

func (o *AnthropicStableSSEObserver) ObserveChunk(chunk []byte) {
	if o == nil || len(chunk) == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pending)+len(chunk) > o.maxBytes {
		// The raw stream has already been delivered.  Drop only observer bytes;
		// billing/audit can mark the observation incomplete without corrupting
		// the protocol response or blocking first output.
		o.metrics.ObserverDroppedBytes += int64(len(chunk))
		return
	}
	o.pending = append(o.pending, chunk...)
	o.consumeCompleteEventsLocked()
}

func (o *AnthropicStableSSEObserver) Finalize() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pending) == 0 {
		return
	}
	// An unterminated final event is not a complete protocol event. Discard it
	// from the observer; the copier will classify the stream as truncated.
	o.pending = nil
}

func (o *AnthropicStableSSEObserver) consumeCompleteEventsLocked() {
	for {
		lfIndex := bytes.Index(o.pending, []byte("\n\n"))
		crlfIndex := bytes.Index(o.pending, []byte("\r\n\r\n"))
		index, separatorLen := lfIndex, 2
		if crlfIndex >= 0 && (index < 0 || crlfIndex < index) {
			index, separatorLen = crlfIndex, 4
		}
		if index < 0 {
			return
		}
		event := append([]byte(nil), o.pending[:index]...)
		o.pending = o.pending[index+separatorLen:]
		o.consumeEventLocked(event)
	}
}

func (o *AnthropicStableSSEObserver) consumeEventLocked(event []byte) {
	var eventType string
	var dataLines []string
	scanner := bufio.NewScanner(bytes.NewReader(event))
	scanner.Buffer(make([]byte, 64<<10), o.maxBytes)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}
	if scanner.Err() != nil {
		o.metrics.ObserverDroppedBytes += int64(len(event))
		return
	}
	if len(dataLines) == 0 {
		return
	}
	data := strings.TrimSpace(strings.Join(dataLines, "\n"))
	if data == "" || data == "[DONE]" {
		// [DONE] belongs to OpenAI-compatible streams. Accepting it as an
		// Anthropic terminal would hide a missing message_stop event.
		return
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &payload) != nil {
		return
	}
	if eventType == "" {
		_ = json.Unmarshal(payload["type"], &eventType)
	}
	if o.metrics.TerminalSeen {
		o.metrics.EventAfterTerminal = true
	}
	now := o.now()
	if o.metrics.FirstSemanticOutputAt.IsZero() && anthropicOAuthNativeSSEHasSemanticOutput(data) {
		o.metrics.FirstSemanticOutputAt = now
	}
	switch eventType {
	case "message_start":
		if raw, ok := payload["message"]; ok {
			var message struct {
				ID    string         `json:"id"`
				Usage map[string]any `json:"usage"`
			}
			if json.Unmarshal(raw, &message) == nil {
				if o.metrics.UpstreamRequestID == "" {
					o.metrics.UpstreamRequestID = strings.TrimSpace(message.ID)
				}
				o.addUsageMapLocked(message.Usage)
			}
		}
	case "message_delta":
		if raw, ok := payload["usage"]; ok {
			var usage map[string]any
			if json.Unmarshal(raw, &usage) == nil {
				o.addUsageMapLocked(usage)
			}
		}
	case "message_stop":
		o.markTerminalLocked(false)
	case "error":
		o.metrics.ErrorEventSeen = true
		o.markTerminalLocked(true)
	}
}

func (o *AnthropicStableSSEObserver) markTerminalLocked(isError bool) {
	o.metrics.TerminalCount++
	if !o.metrics.TerminalSeen {
		o.metrics.TerminalSeen = true
		o.metrics.TerminalAt = o.now()
	}
	if isError {
		o.metrics.ErrorEventSeen = true
	}
}

func (o *AnthropicStableSSEObserver) addUsageMapLocked(values map[string]any) {
	for key, target := range map[string]*int64{
		"input_tokens":                &o.metrics.InputTokens,
		"output_tokens":               &o.metrics.OutputTokens,
		"cache_read_input_tokens":     &o.metrics.CacheReadTokens,
		"cache_creation_input_tokens": &o.metrics.CacheCreationTokens,
	} {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch number := value.(type) {
		case float64:
			if number >= 0 && number <= float64(^uint64(0)>>1) {
				*target = int64(number)
			}
		case json.Number:
			if parsed, err := number.Int64(); err == nil && parsed >= 0 {
				*target = parsed
			}
		}
	}
}

// CopyAnthropicStableResponse copies raw bytes in order and optionally flushes
// each stream chunk.  The observer is invoked only after the downstream write,
// so it cannot move the client-visible first byte behind usage parsing.
func CopyAnthropicStableResponse(
	ctx context.Context,
	dst io.Writer,
	src io.Reader,
	stream bool,
	flush func() error,
	observer *AnthropicStableSSEObserver,
) (AnthropicStableResponseMetrics, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if dst == nil || src == nil {
		return AnthropicStableResponseMetrics{}, fmt.Errorf("stable response copier is incomplete")
	}
	if stream && observer == nil {
		return AnthropicStableResponseMetrics{}, ErrAnthropicStableResponseObservationIncomplete
	}
	now := time.Now
	if observer != nil && observer.now != nil {
		now = observer.now
	}
	metrics := AnthropicStableResponseMetrics{}
	buffer := make([]byte, 32<<10)
	for {
		select {
		case <-ctx.Done():
			// A client disconnect can happen after one or more complete SSE
			// events have already been delivered. Finalize the side-channel
			// observer here so usage/request-id accounting keeps the evidence
			// that was received without delaying cancellation or replaying.
			if observer != nil {
				observer.Finalize()
				observed := observer.Metrics()
				mergeStableResponseMetrics(&metrics, observed)
			}
			return metrics, context.Cause(ctx)
		default:
		}
		read, readErr := src.Read(buffer)
		// A request context can be cancelled while an HTTP response-body Read is
		// blocked. The transport commonly releases that Read as EOF or a network
		// error, so re-check the context before classifying the read result as an
		// upstream truncation. Bytes returned by the same Read are still relayed
		// first; they were already received from the accepted upstream attempt.
		if read > 0 {
			if metrics.FirstUpstreamByteAt.IsZero() {
				metrics.FirstUpstreamByteAt = now()
			}
			metrics.UpstreamBytes += int64(read)
			written, writeErr := dst.Write(buffer[:read])
			if written > 0 {
				metrics.DownstreamBytes += int64(written)
				if metrics.FirstDownstreamWriteAt.IsZero() {
					metrics.FirstDownstreamWriteAt = now()
				}
			}
			if writeErr != nil {
				metrics.DownstreamError = true
				return metrics, writeErr
			}
			if written != read {
				metrics.DownstreamError = true
				return metrics, io.ErrShortWrite
			}
			if stream && flush != nil {
				if err := flush(); err != nil {
					metrics.DownstreamError = true
					return metrics, err
				}
				if metrics.FirstDownstreamFlushAt.IsZero() {
					metrics.FirstDownstreamFlushAt = now()
				}
			}
			if observer != nil {
				observer.ObserveChunk(buffer[:read])
			}
		}
		if cause := context.Cause(ctx); cause != nil {
			if observer != nil {
				observer.Finalize()
				observed := observer.Metrics()
				mergeStableResponseMetrics(&metrics, observed)
			}
			return metrics, cause
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if observer != nil {
					observer.Finalize()
					observed := observer.Metrics()
					mergeStableResponseMetrics(&metrics, observed)
				}
				if stream {
					if metrics.ObserverDroppedBytes > 0 {
						return metrics, ErrAnthropicStableResponseObservationIncomplete
					}
					if metrics.TerminalCount != 1 || metrics.EventAfterTerminal {
						if metrics.TerminalCount == 0 {
							return metrics, ErrAnthropicStableResponseTruncated
						}
						return metrics, ErrAnthropicStableResponseInvalidTerminal
					}
					if metrics.ErrorEventSeen {
						return metrics, ErrAnthropicStableResponseErrorEvent
					}
				}
				return metrics, nil
			}
			return metrics, readErr
		}
	}
}

func mergeStableResponseMetrics(dst *AnthropicStableResponseMetrics, observed AnthropicStableResponseMetrics) {
	if dst == nil {
		return
	}
	dst.TerminalSeen = observed.TerminalSeen
	dst.TerminalCount = observed.TerminalCount
	dst.EventAfterTerminal = observed.EventAfterTerminal
	dst.ErrorEventSeen = observed.ErrorEventSeen
	dst.DownstreamError = observed.DownstreamError
	dst.ObserverDroppedBytes = observed.ObserverDroppedBytes
	dst.InputTokens = observed.InputTokens
	dst.OutputTokens = observed.OutputTokens
	dst.CacheReadTokens = observed.CacheReadTokens
	dst.CacheCreationTokens = observed.CacheCreationTokens
	dst.UpstreamRequestID = observed.UpstreamRequestID
	dst.FirstSemanticOutputAt = observed.FirstSemanticOutputAt
	dst.TerminalAt = observed.TerminalAt
}
