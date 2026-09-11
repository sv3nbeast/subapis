package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *WebChatHandler) AdminGetModelCatalog(c *gin.Context) {
	cfg, err := h.webChatService.AdminModelCatalog(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

func (h *WebChatHandler) AdminSaveModelCatalog(c *gin.Context) {
	var cfg service.WebChatCatalogConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid model catalog")
		return
	}
	if err := h.webChatService.SaveModelCatalog(c.Request.Context(), cfg); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

func (h *WebChatHandler) AdminValidateModelCatalog(c *gin.Context) {
	var cfg service.WebChatCatalogConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid model catalog")
		return
	}
	if err := h.webChatService.ValidateModelCatalog(c.Request.Context(), cfg); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"valid": true})
}
