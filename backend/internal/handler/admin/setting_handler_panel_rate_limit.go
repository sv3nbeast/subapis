package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetPanelRateLimitSettings(c *gin.Context) {
	settings, err := h.settingService.GetPanelRateLimitSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.PanelRateLimitSettings{
		Enabled: settings.Enabled, UserRPM: settings.UserRPM, HeavyRPM: settings.HeavyRPM,
		ExemptAdmin: settings.ExemptAdmin, PublicIPRPM: settings.PublicIPRPM,
	})
}

type UpdatePanelRateLimitSettingsRequest struct {
	Enabled     bool `json:"enabled"`
	UserRPM     int  `json:"user_rpm"`
	HeavyRPM    int  `json:"heavy_rpm"`
	ExemptAdmin bool `json:"exempt_admin"`
	PublicIPRPM int  `json:"public_ip_rpm"`
}

func (h *SettingHandler) UpdatePanelRateLimitSettings(c *gin.Context) {
	var req UpdatePanelRateLimitSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings := &service.PanelRateLimitSettings{
		Enabled: req.Enabled, UserRPM: req.UserRPM, HeavyRPM: req.HeavyRPM,
		ExemptAdmin: req.ExemptAdmin, PublicIPRPM: req.PublicIPRPM,
	}
	if err := h.settingService.SetPanelRateLimitSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	updated, err := h.settingService.GetPanelRateLimitSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.PanelRateLimitSettings{
		Enabled: updated.Enabled, UserRPM: updated.UserRPM, HeavyRPM: updated.HeavyRPM,
		ExemptAdmin: updated.ExemptAdmin, PublicIPRPM: updated.PublicIPRPM,
	})
}
