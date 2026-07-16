package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

	detail, exists := c.Get(service.OpsUpstreamErrorDetailKey)
	require.True(t, exists)
	require.JSONEq(t, `{"error":{"message":"upstream quota exhausted"}}`, detail.(string))
}

func TestHandleMessagesFailoverExhausted_KeepsClientMessageAndStoresRawUpstreamDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h := &GatewayHandler{}
	failoverErr := &service.UpstreamFailoverError{
		StatusCode:   http.StatusForbidden,
		ResponseBody: []byte(`{"error":{"type":"permission_error","message":"workspace forbidden by policy","code":"policy_denied"}}`),
	}

	h.handleFailoverExhausted(c, failoverErr, service.PlatformAnthropic, false)

	require.Equal(t, http.StatusBadGateway, w.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, "error", payload["type"])
	errObj, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "upstream_error", errObj["type"])
	require.Equal(t, "Upstream access forbidden, please contact administrator", errObj["message"])

	msg, exists := c.Get(service.OpsUpstreamErrorMessageKey)
	require.True(t, exists)
	require.Equal(t, "workspace forbidden by policy", msg)

	detail, exists := c.Get(service.OpsUpstreamErrorDetailKey)
	require.True(t, exists)
	require.JSONEq(t, `{"error":{"type":"permission_error","message":"workspace forbidden by policy","code":"policy_denied"}}`, detail.(string))
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

	detail, exists := c.Get(service.OpsUpstreamErrorDetailKey)
	require.True(t, exists)
	require.JSONEq(t, `{"error":{"message":"Resource has been exhausted (e.g. check quota)."}}`, detail.(string))
}

func TestHandleKiroFailoverExhaustedReturnsStandard429WithRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h := &GatewayHandler{}
	h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusTooManyRequests,
		FailureKind:  service.UpstreamFailureRateLimited,
		RetryAfter:   1500 * time.Millisecond,
		ResponseBody: []byte(`{"error":{"message":"rate limited"}}`),
	}, service.PlatformKiro, false)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "2", w.Header().Get("Retry-After"))
	require.Contains(t, w.Body.String(), `"type":"rate_limit_error"`)
}

func TestHandleAnthropicKiroTransportExhaustedReturns503WithRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/messages", nil)

	h := &OpenAIGatewayHandler{}
	h.handleAnthropicFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusServiceUnavailable,
		FailureKind:  service.UpstreamFailureResponseHeaderTimeout,
		RetryAfter:   30 * time.Second,
		ResponseBody: []byte(`{"error":{"message":"header timeout"}}`),
	}, false)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Equal(t, "30", w.Header().Get("Retry-After"))
	require.Contains(t, w.Body.String(), `"type":"upstream_error"`)
	require.Equal(t, "response_header_timeout", service.GetOpsNetworkErrorType(c))
	_, hasUpstreamStatus := c.Get(service.OpsUpstreamStatusCodeKey)
	require.False(t, hasUpstreamStatus, "a gateway timer must not be persisted as an upstream HTTP 503")
}

func TestHandleKiroGatewayTimeoutDoesNotInventUpstream503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h := &GatewayHandler{}
	h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusServiceUnavailable,
		FailureKind:  service.UpstreamFailureFirstSemanticTimeout,
		RetryAfter:   15 * time.Second,
		ResponseBody: []byte(`{"error":{"message":"semantic timeout"}}`),
	}, service.PlatformKiro, false)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Equal(t, "first_semantic_timeout", service.GetOpsNetworkErrorType(c))
	_, hasUpstreamStatus := c.Get(service.OpsUpstreamStatusCodeKey)
	require.False(t, hasUpstreamStatus)
	message, ok := c.Get(service.OpsUpstreamErrorMessageKey)
	require.True(t, ok)
	require.Equal(t, "Kiro gateway first semantic timeout", message)
}

func TestHandleKiroIncompleteStreamExhaustedReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	h := &GatewayHandler{}
	h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusServiceUnavailable,
		FailureKind:  service.UpstreamFailureIncompleteStream,
		RetryAfter:   time.Second,
		ResponseBody: []byte(`{"error":{"message":"missing terminal event"}}`),
	}, service.PlatformKiro, false)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Equal(t, "1", w.Header().Get("Retry-After"))
	require.Contains(t, w.Body.String(), `"type":"upstream_error"`)
}

func withPlatform(ctx context.Context, platform string) context.Context {
	return context.WithValue(ctx, ctxkey.Platform, platform)
}
