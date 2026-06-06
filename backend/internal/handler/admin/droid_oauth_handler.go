package admin

import (
	"errors"

	droidpkg "github.com/Wei-Shaw/sub2api/internal/pkg/droid"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type DroidOAuthHandler struct {
	droidOAuthService *service.DroidOAuthService
}

func NewDroidOAuthHandler(droidOAuthService *service.DroidOAuthService) *DroidOAuthHandler {
	return &DroidOAuthHandler{droidOAuthService: droidOAuthService}
}

type DroidGenerateAuthURLRequest struct {
	ProxyID *int64 `json:"proxy_id"`
}

func (h *DroidOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req DroidGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}
	result, err := h.droidOAuthService.GenerateAuthURL(c.Request.Context(), &service.DroidGenerateAuthURLInput{
		ProxyID: req.ProxyID,
	})
	if err != nil {
		response.BadRequest(c, "生成授权信息失败: "+err.Error())
		return
	}
	response.Success(c, result)
}

type DroidExchangeCodeRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	ProxyID   *int64 `json:"proxy_id"`
}

func (h *DroidOAuthHandler) ExchangeCode(c *gin.Context) {
	var req DroidExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}
	tokenInfo, err := h.droidOAuthService.ExchangeCode(c.Request.Context(), &service.DroidExchangeCodeInput{
		SessionID: req.SessionID,
		ProxyID:   req.ProxyID,
	})
	if err != nil {
		var authErr *droidpkg.DeviceAuthError
		if errors.As(err, &authErr) && (authErr.Code == "authorization_pending" || authErr.Code == "slow_down") {
			response.Success(c, gin.H{
				"pending":     true,
				"error":       authErr.Code,
				"message":     authErr.Message,
				"retry_after": authErr.RetryAfter,
			})
			return
		}
		response.BadRequest(c, "Token 交换失败: "+err.Error())
		return
	}
	response.Success(c, tokenInfo)
}
