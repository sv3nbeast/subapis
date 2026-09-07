package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type nativeCompactReadResult struct {
	message []byte
	err     error
}

// A completed compaction must carry the encrypted continuation item. Preserve
// provider item IDs; do not mistake ordinary text or metadata for a summary.
func nativeCompactItemComplete(item gjson.Result) bool {
	return item.Get("type").String() == "compaction" && strings.TrimSpace(item.Get("encrypted_content").String()) != ""
}

func nativeCompactTerminalItemEvent(message []byte) []byte {
	for index, item := range gjson.GetBytes(message, "response.output").Array() {
		if nativeCompactItemComplete(item) {
			return nativeCompactItemEvent(item, index)
		}
	}
	return nil
}

func nativeCompactItemEvent(item gjson.Result, index int) []byte {
	body, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "output_index": index, "item": json.RawMessage(item.Raw)})
	return body
}

// The reader is demand-driven: never read ahead beyond a terminal event on a
// pooled connection. Only the calling request goroutine touches Gin or writes
// SSE (including heartbeats). Close joins the reader before the lease is released.
type nativeCompactWSReader struct {
	ctx      context.Context
	cancel   context.CancelFunc
	requests chan struct{}
	results  chan nativeCompactReadResult
	done     chan struct{}
}

func newNativeCompactWSReader(ctx context.Context, read func(context.Context) ([]byte, error)) *nativeCompactWSReader {
	ctx, cancel := context.WithCancel(ctx)
	r := &nativeCompactWSReader{ctx: ctx, cancel: cancel, requests: make(chan struct{}), results: make(chan nativeCompactReadResult), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.requests:
			}
			message, err := read(ctx)
			select {
			case <-ctx.Done():
				return
			case r.results <- nativeCompactReadResult{message, err}:
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

func (r *nativeCompactWSReader) Close() { r.cancel(); <-r.done }

func (r *nativeCompactWSReader) Read(heartbeat <-chan time.Time, beat func() error) ([]byte, error) {
	select {
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	case r.requests <- struct{}{}:
	}
	for {
		select {
		case result := <-r.results:
			return result.message, result.err
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		case <-heartbeat:
			// Prefer a ready upstream event over a heartbeat.
			select {
			case result := <-r.results:
				return result.message, result.err
			default:
			}
			if err := beat(); err != nil {
				return nil, err
			}
		}
	}
}

// This is a native Responses terminal, not the legacy unary compact bridge.
// Keep an already-announced response ID, and prevent outer layers from appending
// another error or silently reconnecting/replaying an ambiguous sent request.
func finalizeNativeCompactStreamFailure(c *gin.Context, responseID, code, message string, status int, emit func([]byte, bool)) error {
	message = sanitizeUpstreamErrorMessage(message)
	if responseID == "" {
		responseID = "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	body, _ := json.Marshal(map[string]any{
		"type":     "response.failed",
		"response": map[string]any{"id": responseID, "object": "response", "created_at": time.Now().Unix(), "status": "failed", "output": []any{}, "error": map[string]any{"type": "upstream_error", "code": code, "message": message}},
	})
	if status <= 0 {
		status = http.StatusBadGateway
	}
	MarkOpsStreamFailure(c, "upstream_error", code, message, status)
	MarkGatewaySSEErrorWritten(c)
	if emit != nil {
		emit(body, true)
	} else {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
		_, _ = c.Writer.Write(append(append([]byte("data: "), body...), '\n', '\n'))
		c.Writer.Flush()
	}
	return newOpenAIStreamAlreadyFinalizedError(message)
}
