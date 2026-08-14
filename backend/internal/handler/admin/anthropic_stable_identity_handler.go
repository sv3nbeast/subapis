package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type anthropicStableIdentityConfigureRequest struct {
	GroupIDs  []int64 `json:"group_ids" binding:"required,min=1"`
	APIKeyIDs []int64 `json:"api_key_ids"`
	ProfileID string  `json:"profile_id"`
	DeviceID  string  `json:"device_id"`
}

func (h *AccountHandler) anthropicStableIdentityAdmin(c *gin.Context) (service.AnthropicStableIdentityAdminService, int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return nil, 0, false
	}
	admin, ok := h.adminService.(service.AnthropicStableIdentityAdminService)
	if !ok || admin == nil {
		response.Error(c, http.StatusServiceUnavailable, "Anthropic stable identity administration is unavailable")
		return nil, 0, false
	}
	return admin, accountID, true
}

func (h *AccountHandler) afterAnthropicStableIdentityMutation(accountID int64, clearRuntimeBlock bool) {
	if h == nil || h.stableIdentityGateway == nil {
		return
	}
	if clearRuntimeBlock {
		h.stableIdentityGateway.ClearAnthropicStableIdentityRuntimeBlock(accountID)
	}
	h.stableIdentityGateway.InvalidateAnthropicStableIdentityRoutes()
}

// GetAnthropicStableIdentity reports the redacted account-scoped lifecycle.
// GET /api/v1/admin/accounts/:id/anthropic-stable-identity
func (h *AccountHandler) GetAnthropicStableIdentity(c *gin.Context) {
	admin, accountID, ok := h.anthropicStableIdentityAdmin(c)
	if !ok {
		return
	}
	status, err := admin.GetAnthropicStableIdentity(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// ConfigureAnthropicStableIdentity enrolls the account into selected existing
// groups. It never creates a group and never replaces unrelated memberships.
// PUT /api/v1/admin/accounts/:id/anthropic-stable-identity
func (h *AccountHandler) ConfigureAnthropicStableIdentity(c *gin.Context) {
	admin, accountID, ok := h.anthropicStableIdentityAdmin(c)
	if !ok {
		return
	}
	var req anthropicStableIdentityConfigureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid stable identity configuration: "+err.Error())
		return
	}
	h.stableIdentityMu.Lock()
	defer h.stableIdentityMu.Unlock()
	status, err := admin.ConfigureAnthropicStableIdentity(c.Request.Context(), accountID, &service.AnthropicStableIdentityConfigureInput{
		GroupIDs: req.GroupIDs, APIKeyIDs: req.APIKeyIDs, ProfileID: req.ProfileID, DeviceID: req.DeviceID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.afterAnthropicStableIdentityMutation(accountID, true)
	response.Success(c, status)
}

// PauseAnthropicStableIdentity keeps the reservation but stops all selected
// native routes until an explicit resume.
func (h *AccountHandler) PauseAnthropicStableIdentity(c *gin.Context) {
	h.mutateAnthropicStableIdentity(c, "pause")
}

// ResumeAnthropicStableIdentity revalidates group/API-key bindings and clears
// a finite runtime block before reopening the native route.
func (h *AccountHandler) ResumeAnthropicStableIdentity(c *gin.Context) {
	h.mutateAnthropicStableIdentity(c, "resume")
}

// DisableAnthropicStableIdentity atomically restores the captured scheduler,
// concurrency and group membership state.
func (h *AccountHandler) DisableAnthropicStableIdentity(c *gin.Context) {
	h.mutateAnthropicStableIdentity(c, "disable")
}

func (h *AccountHandler) mutateAnthropicStableIdentity(c *gin.Context, action string) {
	admin, accountID, ok := h.anthropicStableIdentityAdmin(c)
	if !ok {
		return
	}
	h.stableIdentityMu.Lock()
	defer h.stableIdentityMu.Unlock()
	var (
		status *service.AnthropicStableIdentityStatus
		err    error
	)
	switch action {
	case "pause":
		status, err = admin.PauseAnthropicStableIdentity(c.Request.Context(), accountID)
	case "resume":
		status, err = admin.ResumeAnthropicStableIdentity(c.Request.Context(), accountID)
	case "disable":
		status, err = admin.DisableAnthropicStableIdentity(c.Request.Context(), accountID)
	default:
		response.BadRequest(c, "Invalid stable identity action")
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.afterAnthropicStableIdentityMutation(accountID, action != "pause")
	response.Success(c, status)
}
