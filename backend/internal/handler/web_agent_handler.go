package handler

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func webAgentOwner(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	return subject.UserID, true
}
func webAgentPositiveID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid task or session ID")
		return 0, false
	}
	return id, true
}
func webAgentCursor(c *gin.Context, name string) (int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		response.BadRequest(c, "Invalid task cursor")
		return 0, false
	}
	return id, true
}
func (h *WebChatHandler) CreateTask(c *gin.Context) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	sessionID, ok := webAgentPositiveID(c, "id")
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	var input service.WebAgentCreateRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid task request")
		return
	}
	if key := c.GetHeader("Idempotency-Key"); key != "" {
		if input.IdempotencyKey != "" && input.IdempotencyKey != key {
			response.BadRequest(c, "Conflicting idempotency keys")
			return
		}
		input.IdempotencyKey = key
	}
	task, err := h.webChatService.Agent().Create(c.Request.Context(), userID, sessionID, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, task)
}
func (h *WebChatHandler) ListTasks(c *gin.Context) {
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
	tasks, err := h.webChatService.Agent().List(c.Request.Context(), userID, sessionID, before)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	next := int64(0)
	if len(tasks) == 50 {
		next = tasks[len(tasks)-1].ID
	}
	response.Success(c, gin.H{"items": tasks, "next_before": next})
}
func (h *WebChatHandler) GetTask(c *gin.Context) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	id, ok := webAgentPositiveID(c, "task_id")
	if !ok {
		return
	}
	task, err := h.webChatService.Agent().Get(c.Request.Context(), userID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, task)
}
func (h *WebChatHandler) TaskEvents(c *gin.Context) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	id, ok := webAgentPositiveID(c, "task_id")
	if !ok {
		return
	}
	after, ok := webAgentCursor(c, "after")
	if !ok {
		return
	}
	events, err := h.webChatService.Agent().Events(c.Request.Context(), userID, id, after)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	next := after
	if len(events) > 0 {
		next = events[len(events)-1].ID
	}
	response.Success(c, gin.H{"items": events, "next_after": next})
}
func (h *WebChatHandler) CancelTask(c *gin.Context) {
	userID, ok := webAgentOwner(c)
	if !ok {
		return
	}
	id, ok := webAgentPositiveID(c, "task_id")
	if !ok {
		return
	}
	task, err := h.webChatService.Agent().Cancel(c.Request.Context(), userID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, task)
}
