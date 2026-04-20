package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHandleResponsesFailoverExhausted_UsesUpstreamMappingAndOpsContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	h := &GatewayHandler{}
	failoverErr := &service.UpstreamFailoverError{
		StatusCode:   http.StatusTooManyRequests,
		ResponseBody: []byte(`{"error":{"message":"upstream quota exhausted"}}`),
	}

	h.handleResponsesFailoverExhausted(c, failoverErr, false)

	require.Equal(t, http.StatusTooManyRequests, w.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	errObj, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "rate_limit_error", errObj["code"])
	require.Equal(t, "Upstream rate limit exceeded, please retry later", errObj["message"])

	status, exists := c.Get(service.OpsUpstreamStatusCodeKey)
	require.True(t, exists)
	require.Equal(t, http.StatusTooManyRequests, status)

	msg, exists := c.Get(service.OpsUpstreamErrorMessageKey)
	require.True(t, exists)
	require.Equal(t, "upstream quota exhausted", msg)
}

func TestHandleCCFailoverExhausted_UsesSelectedPlatformForPassthroughAndOpsContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(withPlatform(req.Context(), service.PlatformAntigravity))
	c.Request = req

	h := &GatewayHandler{}
	failoverErr := &service.UpstreamFailoverError{
		StatusCode:   http.StatusServiceUnavailable,
		ResponseBody: []byte(`{"error":{"message":"No capacity available for model claude-opus-4-6-thinking on the server"}}`),
	}

	h.handleCCFailoverExhausted(c, failoverErr, false)

	require.Equal(t, http.StatusBadGateway, w.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	errObj, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "upstream_error", errObj["type"])
	require.Equal(t, "Upstream service temporarily unavailable", errObj["message"])

	status, exists := c.Get(service.OpsUpstreamStatusCodeKey)
	require.True(t, exists)
	require.Equal(t, http.StatusServiceUnavailable, status)
}

func TestHandleAnthropicFailoverExhausted_UsesUpstreamMappingAndOpsContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req = req.WithContext(withPlatform(req.Context(), service.PlatformAntigravity))
	c.Request = req

	h := &OpenAIGatewayHandler{}
	failoverErr := &service.UpstreamFailoverError{
		StatusCode:   http.StatusTooManyRequests,
		ResponseBody: []byte(`{"error":{"message":"Resource has been exhausted (e.g. check quota)."}}`),
	}

	h.handleAnthropicFailoverExhausted(c, failoverErr, false)

	require.Equal(t, http.StatusTooManyRequests, w.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, "error", payload["type"])
	errObj, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "rate_limit_error", errObj["type"])
	require.Equal(t, "Upstream rate limit exceeded, please retry later", errObj["message"])

	status, exists := c.Get(service.OpsUpstreamStatusCodeKey)
	require.True(t, exists)
	require.Equal(t, http.StatusTooManyRequests, status)
}

func withPlatform(ctx context.Context, platform string) context.Context {
	return context.WithValue(ctx, ctxkey.Platform, platform)
}
