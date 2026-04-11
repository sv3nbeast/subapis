package handler

import (
	"errors"
	"net/http"
	"strings"

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
		c.JSON(http.StatusOK, gin.H{
			"overall_status": "unknown",
			"public_visible": false,
			"models":         []any{},
			"last_updated":   nil,
		})
		return
	}
	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, resp)
}

// GetModelStatus returns the current service status for a single monitored model.
func (h *StatusHandler) GetModelStatus(c *gin.Context) {
	model := strings.TrimSpace(c.Param("model"))
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}

	resp, err := h.statusProbeService.GetMonitorStatus(c.Request.Context(), model)
	if err != nil {
		if errors.Is(err, service.ErrStatusProbeModelNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, resp)
}
