package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// openAIUpstreamCapacityClientMessagePrefix opens every client-facing message
// for an OpenAI capacity shed. It names the provider and states that this
// platform is not at fault, so users can tell official overload from a gateway
// problem. Keep it stable: OpenAIUpstreamCapacityClientMessage relies on it to
// stay idempotent when the same event is sanitized more than once.
const openAIUpstreamCapacityClientMessagePrefix = "OpenAI 官方服务当前算力过载，请稍后重试。该错误来自 OpenAI 官方上游，并非本平台故障。" +
	"OpenAI upstream is overloaded (provider-side capacity issue, not this platform); please retry shortly."

// OpenAIUpstreamCapacityClientMessage builds the client-facing text for an
// OpenAI upstream capacity shed. The provider's own sanitized message is quoted
// after the attribution so users see the official wording as well.
func OpenAIUpstreamCapacityClientMessage(upstreamMsg string) string {
	upstreamMsg = sanitizeUpstreamErrorMessage(strings.TrimSpace(upstreamMsg))
	if strings.HasPrefix(upstreamMsg, openAIUpstreamCapacityClientMessagePrefix) {
		return upstreamMsg
	}
	if upstreamMsg == "" {
		return openAIUpstreamCapacityClientMessagePrefix
	}
	return openAIUpstreamCapacityClientMessagePrefix + " 上游原始错误 / Upstream message: " + upstreamMsg
}

// IsOpenAIUpstreamCapacityShedBody reports whether an upstream error body is
// an OpenAI capacity shed (server_is_overloaded / slow_down / overloaded text).
func IsOpenAIUpstreamCapacityShedBody(body []byte) bool {
	return isOpenAIRequestScopedCapacityShed("", body)
}

// OpenAIUpstreamTerminalError reports an upstream failure that the service
// classified but deliberately left for the inbound handler to render, so the
// client receives exactly one protocol-correct terminal: a JSON envelope while
// nothing has been written, or the protocol's in-band terminal event after a
// stream started. UpstreamMessage keeps the provider's own wording for ops.
type OpenAIUpstreamTerminalError struct {
	ClientStatus    int
	ErrType         string
	Code            string
	Message         string
	UpstreamStatus  int
	UpstreamMessage string
	// Rendered is set once the service itself wrote the client-facing envelope
	// (non-streaming callers rely on that write); handlers must then only log.
	Rendered bool
}

// writeJSONEnvelope writes the single JSON error envelope for a client that
// has not received any bytes yet and marks the response committed so no
// handler fallback appends a second frame.
func (e *OpenAIUpstreamTerminalError) writeJSONEnvelope(c *gin.Context) {
	if e == nil || c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	errorObject := gin.H{"type": e.ErrType, "message": e.Message}
	if strings.TrimSpace(e.Code) != "" {
		errorObject["code"] = e.Code
	}
	MarkResponseCommitted(c)
	c.JSON(e.ClientStatus, gin.H{"error": errorObject})
	e.Rendered = true
}

func (e *OpenAIUpstreamTerminalError) Error() string {
	if e == nil {
		return "openai upstream terminal error"
	}
	return fmt.Sprintf("openai upstream terminal error: upstream_status=%d client_status=%d message=%s", e.UpstreamStatus, e.ClientStatus, e.UpstreamMessage)
}

// newOpenAIWSUpstreamTerminalError classifies a WebSocket error event that is
// not eligible for fallback. A capacity shed becomes a retryable 503
// server_error carrying the provider attribution; any other event keeps its
// mapped status and the provider's sanitized message.
func newOpenAIWSUpstreamTerminalError(upstreamStatus int, upstreamMsg string, payload []byte) *OpenAIUpstreamTerminalError {
	upstreamMsg = strings.TrimSpace(upstreamMsg)
	terminal := &OpenAIUpstreamTerminalError{
		ClientStatus:    upstreamStatus,
		ErrType:         "upstream_error",
		Message:         sanitizeUpstreamErrorMessage(upstreamMsg),
		UpstreamStatus:  upstreamStatus,
		UpstreamMessage: upstreamMsg,
	}
	if isOpenAIUpstreamCapacityShedEvent(payload) || isOpenAICapacityShedMessage(upstreamMsg) {
		terminal.ClientStatus = http.StatusServiceUnavailable
		terminal.ErrType = "server_error"
		terminal.Code = openAICapacityShedRetryableClientCode
		terminal.Message = OpenAIUpstreamCapacityClientMessage(upstreamMsg)
	}
	return terminal
}
