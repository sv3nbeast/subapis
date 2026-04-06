package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// StatusHandler handles public service-status API requests.
type StatusHandler struct {
	statusProbeService *service.StatusProbeService
}

// NewStatusHandler creates a new StatusHandler.
func NewStatusHandler(statusProbeService *service.StatusProbeService) *StatusHandler {
	return &StatusHandler{statusProbeService: statusProbeService}
}

// GetStatus returns the current service status for all monitored models.
func (h *StatusHandler) GetStatus(c *gin.Context) {
	resp, err := h.statusProbeService.GetStatus(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if resp == nil {
		c.JSON(http.StatusOK, gin.H{"overall_status": "unknown", "models": []any{}, "last_updated": nil})
		return
	}
	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, resp)
}
