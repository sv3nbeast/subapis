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
	GroupID int64  `json:"group_id" binding:"required"`
	Model   string `json:"model" binding:"required"`
}

type webChatSendMessageRequest struct {
	Content string `json:"content" binding:"required"`
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
	sessions, err := h.webChatService.ListSessions(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, sessions)
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
		GroupID: req.GroupID,
		Model:   req.Model,
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

	session, managedKey, messages, assistantMessage, err := h.webChatService.PrepareSend(c.Request.Context(), subject.UserID, sessionID, req.Content)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

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

	fullContent, streamErr := h.forwardStreamingChat(c.Request.Context(), c.Writer, managedKey.Key, session.Model, messages)
	if streamErr != nil {
		errMsg := streamErr.Error()
		if errors.Is(streamErr, context.Canceled) || errors.Is(c.Request.Context().Err(), context.Canceled) {
			errMsg = "client disconnected"
		}
		if err := h.webChatService.FailAssistantMessage(context.WithoutCancel(c.Request.Context()), assistantMessage.ID, fullContent, errMsg); err != nil {
			slog.Warn("failed to persist web chat error message", "message_id", assistantMessage.ID, "error", err)
		}
		writeSSE(c.Writer, "error", gin.H{"message": errMsg})
		flushSSE(c.Writer)
		return
	}

	if err := h.webChatService.CompleteAssistantMessage(context.WithoutCancel(c.Request.Context()), assistantMessage.ID, fullContent); err != nil {
		slog.Warn("failed to persist web chat assistant message", "message_id", assistantMessage.ID, "error", err)
	}
	writeSSE(c.Writer, "done", gin.H{"message_id": assistantMessage.ID})
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

func (h *WebChatHandler) forwardStreamingChat(ctx context.Context, writer io.Writer, apiKey, model string, messages []service.OpenAIChatMessage) (string, error) {
	body := map[string]any{
		"model":    model,
		"stream":   true,
		"messages": messages,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.internalChatCompletionsURL(), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "SubAPIs-WebChat/1.0")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var builder strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var eventLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			done, err := h.handleUpstreamSSEEvent(writer, eventLines, &builder)
			eventLines = eventLines[:0]
			if err != nil {
				return builder.String(), err
			}
			if done {
				return builder.String(), nil
			}
			continue
		}
		eventLines = append(eventLines, line)
	}
	if err := scanner.Err(); err != nil {
		return builder.String(), err
	}
	if len(eventLines) > 0 {
		done, err := h.handleUpstreamSSEEvent(writer, eventLines, &builder)
		if err != nil {
			return builder.String(), err
		}
		if done {
			return builder.String(), nil
		}
	}
	return builder.String(), nil
}

func (h *WebChatHandler) handleUpstreamSSEEvent(writer io.Writer, lines []string, builder *strings.Builder) (bool, error) {
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
