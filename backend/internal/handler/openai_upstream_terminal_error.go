package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// renderOpenAIUpstreamTerminalError writes the single client-facing terminal for
// an upstream failure the service classified but left unrendered: a JSON
// envelope while nothing has been written, otherwise the inbound protocol's
// in-band terminal event (response.failed for Responses, an error event for
// Chat Completions). The provider's own message stays recorded for ops.
func (h *OpenAIGatewayHandler) renderOpenAIUpstreamTerminalError(c *gin.Context, terminalErr *service.OpenAIUpstreamTerminalError, streamStarted bool) {
	if c == nil || terminalErr == nil {
		return
	}
	service.SetOpsUpstreamError(c, terminalErr.UpstreamStatus, terminalErr.UpstreamMessage, "")
	if c.Writer != nil && c.Writer.Written() {
		streamStarted = true
	}
	h.handleStreamingAwareErrorWithCode(c, terminalErr.ClientStatus, terminalErr.ErrType, terminalErr.Code, terminalErr.Message, streamStarted, false)
}
