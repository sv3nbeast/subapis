package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// CursorOAuthHandler 暴露 Cursor 浏览器登录的两个步骤：取授权地址、轮询结果。
type CursorOAuthHandler struct {
	cursorOAuthService *service.CursorOAuthService
}

func NewCursorOAuthHandler(cursorOAuthService *service.CursorOAuthService) *CursorOAuthHandler {
	return &CursorOAuthHandler{cursorOAuthService: cursorOAuthService}
}

type CursorGenerateAuthURLRequest struct {
	ProxyID *int64 `json:"proxy_id"`
}

func (h *CursorOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req CursorGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}
	result, err := h.cursorOAuthService.GenerateAuthURL(c.Request.Context(), req.ProxyID)
	if err != nil {
		response.BadRequest(c, "生成授权信息失败: "+err.Error())
		return
	}
	response.Success(c, result)
}

type CursorPollRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	ProxyID   *int64 `json:"proxy_id"`
}

// Poll 返回 {pending:true} 表示用户尚未在浏览器完成登录，前端应继续轮询。
func (h *CursorOAuthHandler) Poll(c *gin.Context) {
	var req CursorPollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}
	tokenInfo, err := h.cursorOAuthService.Poll(c.Request.Context(), req.SessionID, req.ProxyID)
	if err != nil {
		response.BadRequest(c, "获取授权结果失败: "+err.Error())
		return
	}
	response.Success(c, tokenInfo)
}
