package admin

import (
	"net/http"
	"strings"

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
	// Mask api_key in response to avoid exposing secrets
	masked := *cfg
	if masked.ApiKey != "" {
		if len(masked.ApiKey) > 8 {
			masked.ApiKey = masked.ApiKey[:4] + "****" + masked.ApiKey[len(masked.ApiKey)-4:]
		} else {
			masked.ApiKey = "****"
		}
	}
	response.Success(c, &masked)
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
	// Normalize base_url: trim trailing slashes
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	// If api_key is masked or empty, preserve the existing key
	if cfg.ApiKey == "" || strings.Contains(cfg.ApiKey, "****") {
		existing, err := h.statusProbeService.LoadConfig(c.Request.Context())
		if err == nil && existing != nil {
			cfg.ApiKey = existing.ApiKey
		}
	}

	if err := h.statusProbeService.SaveConfig(c.Request.Context(), &cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Restart the cron scheduler with updated config.
	h.statusProbeService.Restart()

	// Return masked response
	masked := cfg
	if masked.ApiKey != "" {
		if len(masked.ApiKey) > 8 {
			masked.ApiKey = masked.ApiKey[:4] + "****" + masked.ApiKey[len(masked.ApiKey)-4:]
		} else {
			masked.ApiKey = "****"
		}
	}
	response.Success(c, &masked)
}
