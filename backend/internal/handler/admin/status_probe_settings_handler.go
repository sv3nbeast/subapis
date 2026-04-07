package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// StatusProbeSettingsHandler handles admin configuration for the service status probe.
type StatusProbeSettingsHandler struct {
	statusProbeService *service.StatusProbeService
}

// NewStatusProbeSettingsHandler creates a new StatusProbeSettingsHandler.
func NewStatusProbeSettingsHandler(statusProbeService *service.StatusProbeService) *StatusProbeSettingsHandler {
	return &StatusProbeSettingsHandler{statusProbeService: statusProbeService}
}

// GetConfig returns the current status probe configuration.
func (h *StatusProbeSettingsHandler) GetConfig(c *gin.Context) {
	cfg, err := h.statusProbeService.LoadConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	response.Success(c, cfg)
}

// UpdateConfig validates and persists a new status probe configuration.
func (h *StatusProbeSettingsHandler) UpdateConfig(c *gin.Context) {
	var cfg service.StatusProbeConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config: " + err.Error()})
		return
	}
	if cfg.IntervalMinutes <= 0 {
		cfg.IntervalMinutes = 5
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 30
	}
	if err := h.statusProbeService.SaveConfig(c.Request.Context(), &cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Restart the cron scheduler with updated config.
	h.statusProbeService.Restart()
	response.Success(c, cfg)
}
