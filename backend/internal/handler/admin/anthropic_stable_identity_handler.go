package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

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

// ConfigureAnthropicStableIdentity enables the account-level switch. Current
// Anthropic group membership is derived automatically; request-body routing
// selectors from older frontends are intentionally ignored.
// PUT /api/v1/admin/accounts/:id/anthropic-stable-identity
func (h *AccountHandler) ConfigureAnthropicStableIdentity(c *gin.Context) {
	admin, accountID, ok := h.anthropicStableIdentityAdmin(c)
	if !ok {
		return
	}
	h.stableIdentityMu.Lock()
	defer h.stableIdentityMu.Unlock()
	status, err := admin.ConfigureAnthropicStableIdentity(c.Request.Context(), accountID, &service.AnthropicStableIdentityConfigureInput{})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.afterAnthropicStableIdentityMutation(accountID, true)
	response.Success(c, status)
}

// PauseAnthropicStableIdentity keeps the reservation but stops all native
// routes for the account's current groups until an explicit resume.
func (h *AccountHandler) PauseAnthropicStableIdentity(c *gin.Context) {
	h.mutateAnthropicStableIdentity(c, "pause")
}

// ResumeAnthropicStableIdentity revalidates current group membership and
// clears a finite runtime block before reopening the native route.
func (h *AccountHandler) ResumeAnthropicStableIdentity(c *gin.Context) {
	h.mutateAnthropicStableIdentity(c, "resume")
}

// DisableAnthropicStableIdentity restores captured scheduler/concurrency state
// and deliberately leaves current group membership unchanged.
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
