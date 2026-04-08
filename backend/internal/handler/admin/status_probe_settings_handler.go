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
	// Mask api_key per model to avoid exposing secrets
	masked := *cfg
	masked.Models = maskModelApiKeys(cfg.Models)
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

	// Load existing config to preserve masked api_keys
	existing, _ := h.statusProbeService.LoadConfig(c.Request.Context())
	existingKeyMap := make(map[string]string)
	if existing != nil {
		for _, m := range existing.Models {
			if m.ApiKey != "" {
				existingKeyMap[m.Model] = m.ApiKey
			}
		}
	}

	for i := range cfg.Models {
		// Normalize base_url per model
		cfg.Models[i].BaseURL = strings.TrimRight(cfg.Models[i].BaseURL, "/")
		// If api_key is masked or empty, preserve the existing key
		if cfg.Models[i].ApiKey == "" || strings.Contains(cfg.Models[i].ApiKey, "****") {
			if key, ok := existingKeyMap[cfg.Models[i].Model]; ok {
				cfg.Models[i].ApiKey = key
			}
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
	masked.Models = maskModelApiKeys(cfg.Models)
	response.Success(c, &masked)
}

// maskApiKey returns a masked version of an API key.
func maskApiKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) > 8 {
		return key[:4] + "****" + key[len(key)-4:]
	}
	return "****"
}

// maskModelApiKeys returns a copy of models with api_keys masked.
func maskModelApiKeys(models []service.StatusProbeModelConfig) []service.StatusProbeModelConfig {
	result := make([]service.StatusProbeModelConfig, len(models))
	copy(result, models)
	for i := range result {
		result[i].ApiKey = maskApiKey(result[i].ApiKey)
	}
	return result
}
