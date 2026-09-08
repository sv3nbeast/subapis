package handler

import (
	"mime"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *WebChatHandler) ListArtifacts(c *gin.Context) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	sessionID, ok := webAgentCursor(c, "session_id")
	if !ok {
		return
	}
	before, ok := webAgentCursor(c, "before")
	if !ok {
		return
	}
	items, err := h.webChatService.Artifacts().List(c.Request.Context(), userID, sessionID, before)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	next := int64(0)
	if len(items) == 50 {
		next = items[len(items)-1].ID
	}
	response.Success(c, gin.H{"items": items, "next_before": next})
}
func (h *WebChatHandler) GetArtifact(c *gin.Context) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	id, ok := webAgentPositiveID(c, "artifact_id")
	if !ok {
		return
	}
	item, err := h.webChatService.Artifacts().Get(c.Request.Context(), userID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}
func (h *WebChatHandler) ArtifactVersions(c *gin.Context) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	id, ok := webAgentPositiveID(c, "artifact_id")
	if !ok {
		return
	}
	before, ok := webAgentCursor(c, "before")
	if !ok {
		return
	}
	items, err := h.webChatService.Artifacts().Versions(c.Request.Context(), userID, id, before)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	next := int64(0)
	if len(items) == 50 {
		next = items[len(items)-1].ID
	}
	response.Success(c, gin.H{"items": items, "next_before": next})
}
func (h *WebChatHandler) DownloadArtifact(c *gin.Context) { h.serveArtifact(c, false) }
func (h *WebChatHandler) PreviewArtifact(c *gin.Context)  { h.serveArtifact(c, true) }
func (h *WebChatHandler) DeleteArtifact(c *gin.Context) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	id, ok := webAgentPositiveID(c, "artifact_id")
	if !ok {
		return
	}
	if err := h.webChatService.Artifacts().Delete(c.Request.Context(), userID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true, "cleanup_pending": true})
}
func (h *WebChatHandler) ArtifactStorageUsage(c *gin.Context) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	usage, err := h.webChatService.Artifacts().StorageUsage(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, usage)
}
func (h *WebChatHandler) serveArtifact(c *gin.Context, preview bool) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	id, ok := webAgentPositiveID(c, "artifact_id")
	if !ok {
		return
	}
	item, reader, size, err := h.webChatService.Artifacts().Download(c.Request.Context(), userID, id, preview)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer reader.Close()
	disposition, name, contentType := "attachment", item.Filename, item.MIME
	if preview {
		disposition, name, contentType = "inline", "preview.pdf", "application/pdf"
	}
	c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'")
	c.DataFromReader(http.StatusOK, size, contentType, reader, nil)
}
