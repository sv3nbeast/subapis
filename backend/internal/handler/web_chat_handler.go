package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type WebChatHandler struct {
	webChatService *service.WebChatService
	cfg            *config.Config
	httpClient     *http.Client
}

func NewWebChatHandler(webChatService *service.WebChatService, cfg *config.Config) *WebChatHandler {
	return &WebChatHandler{
		webChatService: webChatService,
		cfg:            cfg,
		httpClient:     &http.Client{},
	}
}

type webChatCreateSessionRequest struct {
	GroupID           int64  `json:"group_id"`
	Model             string `json:"model"`
	ProjectID         *int64 `json:"project_id"`
	DefaultTemplateID *int64 `json:"default_template_id"`
}

type webChatSendMessageRequest struct {
	Content    string `json:"content" binding:"required"`
	GroupID    int64  `json:"group_id"`
	Model      string `json:"model"`
	TemplateID *int64 `json:"template_id"`
}

type webChatReviseMessageRequest struct {
	Content string `json:"content" binding:"required"`
}

type webChatStreamResult struct {
	Content   string
	RequestID string
	Usage     service.WebChatUsage
}

func (h *WebChatHandler) Options(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	options, err := h.webChatService.Options(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, options)
}

func (h *WebChatHandler) ListSessions(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	sessions, err := h.webChatService.ListSessions(c.Request.Context(), subject.UserID, c.Query("q"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, sessions)
}

func (h *WebChatHandler) PatchSession(c *gin.Context) {
	subject, sessionID, ok := h.authSessionParam(c)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	var req service.WebChatPatchSessionRequest
	if v, ok := raw["title"]; ok {
		var value string
		if json.Unmarshal(v, &value) != nil {
			response.BadRequest(c, "Invalid title")
			return
		}
		req.Title = &value
	}
	if v, ok := raw["pinned"]; ok {
		var value bool
		if json.Unmarshal(v, &value) != nil {
			response.BadRequest(c, "Invalid pinned state")
			return
		}
		req.Pinned = &value
	}
	if v, ok := raw["system_prompt"]; ok {
		var value string
		if json.Unmarshal(v, &value) != nil {
			response.BadRequest(c, "Invalid system prompt")
			return
		}
		req.SystemPrompt = &value
	}
	if v, ok := raw["temperature"]; ok {
		req.TemperatureSet = true
		if string(v) != "null" {
			var value float64
			if json.Unmarshal(v, &value) != nil {
				response.BadRequest(c, "Invalid temperature")
				return
			}
			req.Temperature = &value
		}
	}
	if v, ok := raw["max_output_tokens"]; ok {
		var value int
		if json.Unmarshal(v, &value) != nil {
			response.BadRequest(c, "Invalid max output tokens")
			return
		}
		req.MaxOutputTokens = &value
	}
	if v, ok := raw["project_id"]; ok {
		req.ProjectIDSet = true
		if string(v) != "null" {
			var value int64
			if json.Unmarshal(v, &value) != nil || value <= 0 {
				response.BadRequest(c, "Invalid project ID")
				return
			}
			req.ProjectID = &value
		}
	}
	if v, ok := raw["default_template_id"]; ok {
		req.DefaultTemplateIDSet = true
		if string(v) != "null" {
			var value int64
			if json.Unmarshal(v, &value) != nil || value <= 0 {
				response.BadRequest(c, "Invalid template ID")
				return
			}
			req.DefaultTemplateID = &value
		}
	}
	session, err := h.webChatService.UpdateSession(c.Request.Context(), subject.UserID, sessionID, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, session)
}

func (h *WebChatHandler) CreateSession(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req webChatCreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	session, err := h.webChatService.CreateSession(c.Request.Context(), subject.UserID, service.WebChatCreateSessionRequest{
		GroupID:           req.GroupID,
		Model:             req.Model,
		ProjectID:         req.ProjectID,
		DefaultTemplateID: req.DefaultTemplateID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, session)
}

func (h *WebChatHandler) ListMessages(c *gin.Context) {
	subject, sessionID, ok := h.authSessionParam(c)
	if !ok {
		return
	}
	messages, err := h.webChatService.ListMessages(c.Request.Context(), subject.UserID, sessionID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, messages)
}

func (h *WebChatHandler) DeleteSession(c *gin.Context) {
	subject, sessionID, ok := h.authSessionParam(c)
	if !ok {
		return
	}
	if err := h.webChatService.DeleteSession(c.Request.Context(), subject.UserID, sessionID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *WebChatHandler) SendMessage(c *gin.Context) {
	subject, sessionID, ok := h.authSessionParam(c)
	if !ok {
		return
	}
	var req webChatSendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	generation, err := h.webChatService.PrepareSend(c.Request.Context(), subject.UserID, sessionID, service.WebChatSendMessageRequest{
		Content:    req.Content,
		GroupID:    req.GroupID,
		Model:      req.Model,
		TemplateID: req.TemplateID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	h.streamGeneration(c, subject.UserID, generation)
}

func (h *WebChatHandler) RegenerateMessage(c *gin.Context) {
	subject, sessionID, ok := h.authSessionParam(c)
	if !ok {
		return
	}
	messageID, err := strconv.ParseInt(c.Param("message_id"), 10, 64)
	if err != nil || messageID <= 0 {
		response.BadRequest(c, "Invalid message ID")
		return
	}
	generation, err := h.webChatService.PrepareRegenerate(c.Request.Context(), subject.UserID, sessionID, messageID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.streamGeneration(c, subject.UserID, generation)
}

func (h *WebChatHandler) ReviseMessage(c *gin.Context) {
	subject, sessionID, ok := h.authSessionParam(c)
	if !ok {
		return
	}
	messageID, err := strconv.ParseInt(c.Param("message_id"), 10, 64)
	if err != nil || messageID <= 0 {
		response.BadRequest(c, "Invalid message ID")
		return
	}
	var req webChatReviseMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	generation, err := h.webChatService.PrepareRevise(c.Request.Context(), subject.UserID, sessionID, messageID, req.Content)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.streamGeneration(c, subject.UserID, generation)
}

func (h *WebChatHandler) ListMessageVersions(c *gin.Context) {
	subject, sessionID, ok := h.authSessionParam(c)
	if !ok {
		return
	}
	messageID, ok := positiveParam(c, "message_id")
	if !ok {
		return
	}
	items, err := h.webChatService.ListMessageVersions(c.Request.Context(), subject.UserID, sessionID, messageID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *WebChatHandler) ActivateMessageVersion(c *gin.Context) {
	subject, sessionID, ok := h.authSessionParam(c)
	if !ok {
		return
	}
	messageID, ok := positiveParam(c, "message_id")
	if !ok {
		return
	}
	if err := h.webChatService.ActivateMessageVersion(c.Request.Context(), subject.UserID, sessionID, messageID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	messages, err := h.webChatService.ListMessages(c.Request.Context(), subject.UserID, sessionID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, messages)
}

func (h *WebChatHandler) ListProjects(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	items, err := h.webChatService.ListProjects(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}
func (h *WebChatHandler) CreateProject(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var in service.WebChatProjectInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.webChatService.CreateProject(c.Request.Context(), subject.UserID, in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, item)
}
func (h *WebChatHandler) UpdateProject(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, ok := positiveParam(c, "id")
	if !ok {
		return
	}
	var in service.WebChatProjectInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.webChatService.UpdateProject(c.Request.Context(), subject.UserID, id, in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}
func (h *WebChatHandler) DeleteProject(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, ok := positiveParam(c, "id")
	if !ok {
		return
	}
	if err := h.webChatService.DeleteProject(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *WebChatHandler) ListTemplates(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	items, err := h.webChatService.ListTemplates(c.Request.Context(), subject.UserID, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}
func (h *WebChatHandler) CreateTemplate(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var in service.WebChatTemplateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.webChatService.CreatePersonalTemplate(c.Request.Context(), subject.UserID, in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, item)
}
func (h *WebChatHandler) UpdateTemplate(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, ok := positiveParam(c, "id")
	if !ok {
		return
	}
	var in service.WebChatTemplateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.webChatService.UpdateTemplate(c.Request.Context(), subject.UserID, id, in, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}
func (h *WebChatHandler) DeleteTemplate(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, ok := positiveParam(c, "id")
	if !ok {
		return
	}
	if err := h.webChatService.DeleteTemplate(c.Request.Context(), subject.UserID, id, false); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
func (h *WebChatHandler) CopyTemplate(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, ok := positiveParam(c, "id")
	if !ok {
		return
	}
	item, err := h.webChatService.CopyTemplate(c.Request.Context(), subject.UserID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, item)
}

func (h *WebChatHandler) AdminListTemplates(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Admin not authenticated")
		return
	}
	items, err := h.webChatService.ListTemplates(c.Request.Context(), subject.UserID, true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	system := make([]service.WebChatTemplate, 0)
	for _, item := range items {
		if item.Scope == "system" {
			system = append(system, item)
		}
	}
	response.Success(c, system)
}
func (h *WebChatHandler) AdminCreateTemplate(c *gin.Context) {
	var in service.WebChatTemplateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.webChatService.CreateSystemTemplate(c.Request.Context(), in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, item)
}
func (h *WebChatHandler) AdminUpdateTemplate(c *gin.Context) {
	id, ok := positiveParam(c, "id")
	if !ok {
		return
	}
	var in service.WebChatTemplateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.webChatService.UpdateTemplate(c.Request.Context(), 0, id, in, true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}
func (h *WebChatHandler) AdminDeleteTemplate(c *gin.Context) {
	id, ok := positiveParam(c, "id")
	if !ok {
		return
	}
	if err := h.webChatService.DeleteTemplate(c.Request.Context(), 0, id, true); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func positiveParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid "+name)
		return 0, false
	}
	return id, true
}

func (h *WebChatHandler) streamGeneration(c *gin.Context, userID int64, generation *service.WebChatGeneration) {
	session, managedKey, messages, assistantMessage := generation.Session, generation.APIKey, generation.Messages, generation.AssistantMessage
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	writeSSE(c.Writer, "meta", gin.H{
		"session_id": session.ID,
		"message_id": assistantMessage.ID,
		"group_id":   session.GroupID,
		"model":      session.Model,
	})
	flushSSE(c.Writer)

	result, streamErr := h.forwardStreamingChat(c.Request.Context(), c.Writer, managedKey.Key, session, messages)
	if streamErr != nil {
		errMsg := streamErr.Error()
		if errors.Is(streamErr, context.Canceled) || errors.Is(c.Request.Context().Err(), context.Canceled) {
			errMsg = "generation stopped"
		}
		persisted, persistErr := h.webChatService.FailAssistantMessage(context.WithoutCancel(c.Request.Context()), userID, assistantMessage.ID, result.Content, errMsg, result.RequestID, result.Usage)
		if persistErr != nil {
			slog.Warn("failed to persist web chat error message", "message_id", assistantMessage.ID, "error", persistErr)
		}
		writeSSE(c.Writer, "error", gin.H{"message": errMsg, "persisted": persisted})
		flushSSE(c.Writer)
		return
	}

	persisted, err := h.webChatService.CompleteAssistantMessage(context.WithoutCancel(c.Request.Context()), userID, assistantMessage.ID, result.Content, result.RequestID, result.Usage)
	if err != nil {
		slog.Warn("failed to persist web chat assistant message", "message_id", assistantMessage.ID, "error", err)
		writeSSE(c.Writer, "error", gin.H{"message": "failed to persist generated message"})
		flushSSE(c.Writer)
		return
	}
	writeSSE(c.Writer, "done", gin.H{"message_id": assistantMessage.ID, "message": persisted, "usage": result.Usage, "request_id": result.RequestID})
	flushSSE(c.Writer)
}

func (h *WebChatHandler) authSessionParam(c *gin.Context) (middleware.AuthSubject, int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return middleware.AuthSubject{}, 0, false
	}
	sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || sessionID <= 0 {
		response.BadRequest(c, "Invalid session ID")
		return middleware.AuthSubject{}, 0, false
	}
	return subject, sessionID, true
}

func (h *WebChatHandler) forwardStreamingChat(ctx context.Context, writer io.Writer, apiKey string, session *service.WebChatSession, messages []service.OpenAIChatMessage) (webChatStreamResult, error) {
	targetURL, payload, useAnthropicMessages, err := h.buildUpstreamPayload(session, messages)
	if err != nil {
		return webChatStreamResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return webChatStreamResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "SubAPIs-WebChat/1.0")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return webChatStreamResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return webChatStreamResult{RequestID: webChatResponseRequestID(resp)}, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	requestID := webChatResponseRequestID(resp)
	if useAnthropicMessages {
		result, err := h.forwardAnthropicMessagesStream(writer, resp.Body)
		result.RequestID = requestID
		return result, err
	}
	result, err := h.forwardOpenAIChatCompletionsStream(writer, resp.Body)
	result.RequestID = requestID
	return result, err
}

func (h *WebChatHandler) buildUpstreamPayload(session *service.WebChatSession, messages []service.OpenAIChatMessage) (string, []byte, bool, error) {
	if shouldUseAnthropicMessages(session.Platform) {
		payload, err := buildAnthropicMessagesPayload(session, messages)
		if err != nil {
			return "", nil, false, err
		}
		return h.internalMessagesURL(), payload, true, nil
	}

	body := map[string]any{
		"model":          session.Model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"max_tokens":     session.MaxOutputTokens,
	}
	if session.Temperature != nil {
		body["temperature"] = *session.Temperature
	}
	openAIMessages := messages
	if strings.TrimSpace(session.SystemPrompt) != "" {
		openAIMessages = append([]service.OpenAIChatMessage{{Role: "system", Content: session.SystemPrompt}}, messages...)
	}
	body["messages"] = openAIMessages
	payload, err := json.Marshal(body)
	if err != nil {
		return "", nil, false, err
	}
	return h.internalChatCompletionsURL(), payload, false, nil
}

func buildAnthropicMessagesPayload(session *service.WebChatSession, messages []service.OpenAIChatMessage) ([]byte, error) {
	anthropicMessages := make([]apicompat.AnthropicMessage, 0, len(messages))
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = service.WebChatMessageRoleUser
		}
		content, err := buildAnthropicMessageContent(strings.TrimSpace(message.Content), role)
		if err != nil {
			return nil, err
		}
		anthropicMessages = append(anthropicMessages, apicompat.AnthropicMessage{
			Role:    role,
			Content: content,
		})
	}

	payload := apicompat.AnthropicRequest{
		Model:     session.Model,
		MaxTokens: session.MaxOutputTokens,
		Messages:  anthropicMessages,
		Stream:    true,
	}
	payload.Temperature = session.Temperature
	if strings.TrimSpace(session.SystemPrompt) != "" {
		payload.System, _ = json.Marshal(session.SystemPrompt)
	}
	return json.Marshal(payload)
}

func buildAnthropicMessageContent(text, role string) (json.RawMessage, error) {
	block := apicompat.AnthropicContentBlock{
		Type: "text",
		Text: text,
	}
	if role == service.WebChatMessageRoleUser {
		block.CacheControl = &apicompat.AnthropicCacheControl{
			Type: "ephemeral",
			TTL:  "5m",
		}
	}
	return json.Marshal([]apicompat.AnthropicContentBlock{block})
}

func shouldUseAnthropicMessages(platform string) bool {
	switch strings.TrimSpace(platform) {
	case service.PlatformAnthropic, service.PlatformAntigravity, service.PlatformGemini:
		return true
	default:
		return false
	}
}

func (h *WebChatHandler) forwardOpenAIChatCompletionsStream(writer io.Writer, body io.Reader) (webChatStreamResult, error) {
	var builder strings.Builder
	var usage service.WebChatUsage
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var eventLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			done, err := h.handleUpstreamSSEEvent(writer, eventLines, &builder, &usage)
			eventLines = eventLines[:0]
			if err != nil {
				return webChatStreamResult{Content: builder.String(), Usage: usage}, err
			}
			if done {
				return webChatStreamResult{Content: builder.String(), Usage: usage}, nil
			}
			continue
		}
		eventLines = append(eventLines, line)
	}
	if err := scanner.Err(); err != nil {
		return webChatStreamResult{Content: builder.String(), Usage: usage}, err
	}
	if len(eventLines) > 0 {
		done, err := h.handleUpstreamSSEEvent(writer, eventLines, &builder, &usage)
		if err != nil {
			return webChatStreamResult{Content: builder.String(), Usage: usage}, err
		}
		if done {
			return webChatStreamResult{Content: builder.String(), Usage: usage}, nil
		}
	}
	return webChatStreamResult{Content: builder.String(), Usage: usage}, io.ErrUnexpectedEOF
}

func (h *WebChatHandler) handleUpstreamSSEEvent(writer io.Writer, lines []string, builder *strings.Builder, usage *service.WebChatUsage) (bool, error) {
	for _, line := range lines {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return true, nil
		}
		mergeOpenAIStreamUsage(data, usage)
		text, errText := extractOpenAIStreamText(data)
		if errText != "" {
			return false, errors.New(errText)
		}
		if text == "" {
			continue
		}
		builder.WriteString(text)
		writeSSE(writer, "delta", gin.H{"text": text})
		flushSSE(writer)
	}
	return false, nil
}

func (h *WebChatHandler) forwardAnthropicMessagesStream(writer io.Writer, body io.Reader) (webChatStreamResult, error) {
	var builder strings.Builder
	var usage service.WebChatUsage
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var eventLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			done, err := h.handleAnthropicSSEEvent(writer, eventLines, &builder, &usage)
			eventLines = eventLines[:0]
			if err != nil {
				return webChatStreamResult{Content: builder.String(), Usage: usage}, err
			}
			if done {
				return webChatStreamResult{Content: builder.String(), Usage: usage}, nil
			}
			continue
		}
		eventLines = append(eventLines, line)
	}
	if err := scanner.Err(); err != nil {
		return webChatStreamResult{Content: builder.String(), Usage: usage}, err
	}
	if len(eventLines) > 0 {
		done, err := h.handleAnthropicSSEEvent(writer, eventLines, &builder, &usage)
		if err != nil {
			return webChatStreamResult{Content: builder.String(), Usage: usage}, err
		}
		if done {
			return webChatStreamResult{Content: builder.String(), Usage: usage}, nil
		}
	}
	return webChatStreamResult{Content: builder.String(), Usage: usage}, io.ErrUnexpectedEOF
}

func (h *WebChatHandler) handleAnthropicSSEEvent(writer io.Writer, lines []string, builder *strings.Builder, usage *service.WebChatUsage) (bool, error) {
	for _, line := range lines {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return true, nil
		}
		mergeAnthropicStreamUsage(data, usage)
		text, done, errText := extractAnthropicStreamText(data)
		if errText != "" {
			return false, errors.New(errText)
		}
		if text != "" {
			builder.WriteString(text)
			writeSSE(writer, "delta", gin.H{"text": text})
			flushSSE(writer)
		}
		if done {
			return true, nil
		}
	}
	return false, nil
}

func extractOpenAIStreamText(data string) (string, string) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return "", ""
	}
	if errObj, ok := raw["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok {
			return "", msg
		}
		return "", "upstream stream error"
	}
	choices, ok := raw["choices"].([]any)
	if !ok || len(choices) == 0 {
		return "", ""
	}
	first, ok := choices[0].(map[string]any)
	if !ok {
		return "", ""
	}
	if delta, ok := first["delta"].(map[string]any); ok {
		if content, ok := delta["content"].(string); ok {
			return content, ""
		}
	}
	if message, ok := first["message"].(map[string]any); ok {
		if content, ok := message["content"].(string); ok {
			return content, ""
		}
	}
	if content, ok := first["text"].(string); ok {
		return content, ""
	}
	return "", ""
}

func extractAnthropicStreamText(data string) (string, bool, string) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return "", false, ""
	}
	if errObj, ok := raw["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok {
			return "", false, msg
		}
		return "", false, "upstream stream error"
	}
	if eventType, _ := raw["type"].(string); eventType == "message_stop" {
		return "", true, ""
	}
	deltaObj, ok := raw["delta"].(map[string]any)
	if !ok {
		return "", false, ""
	}
	if deltaType, _ := deltaObj["type"].(string); deltaType != "text_delta" {
		return "", false, ""
	}
	if text, ok := deltaObj["text"].(string); ok {
		return text, false, ""
	}
	return "", false, ""
}

func mergeOpenAIStreamUsage(data string, usage *service.WebChatUsage) {
	if usage == nil {
		return
	}
	var raw struct {
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			InputTokens      int64 `json:"input_tokens"`
			OutputTokens     int64 `json:"output_tokens"`
			PromptDetails    struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(data), &raw) != nil || raw.Usage == nil {
		return
	}
	u := raw.Usage
	usage.InputTokens = maxInt64(u.PromptTokens, u.InputTokens)
	usage.OutputTokens = maxInt64(u.CompletionTokens, u.OutputTokens)
	usage.CacheReadTokens = maxInt64(u.PromptDetails.CachedTokens, u.CacheReadTokens)
	usage.CacheCreationTokens = u.CacheCreationTokens
}

func mergeAnthropicStreamUsage(data string, usage *service.WebChatUsage) {
	if usage == nil {
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &raw) != nil {
		return
	}
	merge := func(value json.RawMessage) {
		var u struct {
			InputTokens         int64 `json:"input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
		}
		if json.Unmarshal(value, &u) != nil {
			return
		}
		usage.InputTokens = maxInt64(usage.InputTokens, u.InputTokens)
		usage.OutputTokens = maxInt64(usage.OutputTokens, u.OutputTokens)
		usage.CacheReadTokens = maxInt64(usage.CacheReadTokens, u.CacheReadTokens)
		usage.CacheCreationTokens = maxInt64(usage.CacheCreationTokens, u.CacheCreationTokens)
	}
	if v, ok := raw["usage"]; ok {
		merge(v)
	}
	if messageRaw, ok := raw["message"]; ok {
		var m map[string]json.RawMessage
		if json.Unmarshal(messageRaw, &m) == nil {
			if v, ok := m["usage"]; ok {
				merge(v)
			}
		}
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func webChatResponseRequestID(resp *http.Response) string {
	for _, key := range []string{"X-Request-Id", "X-Request-ID", "Request-Id", "Request-ID"} {
		if value := strings.TrimSpace(resp.Header.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func (h *WebChatHandler) internalMessagesURL() string {
	port := 8080
	if h != nil && h.cfg != nil && h.cfg.Server.Port > 0 {
		port = h.cfg.Server.Port
	}
	return fmt.Sprintf("http://127.0.0.1:%d/v1/messages", port)
}

func (h *WebChatHandler) internalChatCompletionsURL() string {
	port := 8080
	if h != nil && h.cfg != nil && h.cfg.Server.Port > 0 {
		port = h.cfg.Server.Port
	}
	return fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port)
}

func writeSSE(writer io.Writer, event string, data any) {
	payload, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, payload)
}

func flushSSE(writer io.Writer) {
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
}
