package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func messagesContextErrorTestConfig() *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: true,
			},
		},
	}
}

func messagesContextErrorTestAccount() *Account {
	return &Account{
		ID:          101,
		Name:        "messages-context-oauth-test",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-test-token",
			"chatgpt_account_id": "chatgpt-test-account",
		},
	}
}

func buildResponsesContextLengthFailedSSEStream(prefix ...string) string {
	failed := `{"type":"response.failed","response":{"id":"resp_context","object":"response","status":"failed","error":{"code":"context_length_exceeded","type":"invalid_request_error","message":"Your input exceeds the context window of this model. Please adjust your input and try again."},"output":[],"usage":{"input_tokens":100000,"output_tokens":0,"total_tokens":100000}}}`
	events := append(append([]string(nil), prefix...), "data: "+failed, "")
	return strings.Join(events, "\n")
}

func bindMessagesContextErrorPassthroughRule(c *gin.Context, responseCode int) {
	rule := &model.ErrorPassthroughRule{
		ID:              1,
		Enabled:         true,
		Platforms:       []string{PlatformOpenAI},
		MatchMode:       model.MatchModeAny,
		Keywords:        []string{"context_length_exceeded"},
		ResponseCode:    &responseCode,
		PassthroughBody: true,
	}
	svc := &ErrorPassthroughService{}
	svc.setLocalCache([]*model.ErrorPassthroughRule{rule})
	BindErrorPassthroughService(c, svc)
}

func TestForwardAsAnthropic_BufferedContextLengthFailed_ReturnsClientError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-context"}},
		Body:       io.NopCloser(strings.NewReader(buildResponsesContextLengthFailedSSEStream())),
	}}
	svc := &OpenAIGatewayService{cfg: messagesContextErrorTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, messagesContextErrorTestAccount(), body, "", "")

	require.Error(t, err)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "client context limits must not fail over")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "error", gjson.Get(rec.Body.String(), "type").String())
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "prompt is too long")
	require.True(t, IsResponseCommitted(c))
	require.True(t, HasOpsClientBusinessLimitedReason(c, OpsClientBusinessLimitedReasonContextLimit))
	status, ok := c.Get(OpsUpstreamStatusCodeKey)
	require.True(t, ok)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestForwardAsAnthropic_StreamingContextLengthFailedBeforeOutput_ReturnsHTTPClientError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buildResponsesContextLengthFailedSSEStream())),
	}}
	svc := &OpenAIGatewayService{cfg: messagesContextErrorTestConfig(), httpUpstream: upstream}

	_, err := svc.ForwardAsAnthropic(context.Background(), c, messagesContextErrorTestAccount(), body, "", "")

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "prompt is too long")
	require.NotContains(t, rec.Body.String(), "event: error")
	require.True(t, IsResponseCommitted(c))
}

func TestForwardAsAnthropic_StreamingContextLengthFailedAfterOutput_ReturnsInBandClientError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := buildResponsesContextLengthFailedSSEStream(
		`data: {"type":"response.created","response":{"id":"resp_context","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"partial"}`,
		"",
	)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{cfg: messagesContextErrorTestConfig(), httpUpstream: upstream}

	_, err := svc.ForwardAsAnthropic(context.Background(), c, messagesContextErrorTestAccount(), body, "", "")

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusOK, rec.Code, "HTTP status is already committed once streaming output starts")
	require.Contains(t, rec.Body.String(), "partial")
	require.Contains(t, rec.Body.String(), "event: error")
	require.Contains(t, rec.Body.String(), `"type":"invalid_request_error"`)
	require.Contains(t, rec.Body.String(), "prompt is too long")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
	require.True(t, IsResponseCommitted(c))
}

func TestForwardAsAnthropic_BufferedContextLengthFailed_CustomRuleTakesPriority(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	bindMessagesContextErrorPassthroughRule(c, http.StatusConflict)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(buildResponsesContextLengthFailedSSEStream())),
	}}
	svc := &OpenAIGatewayService{cfg: messagesContextErrorTestConfig(), httpUpstream: upstream}

	_, err := svc.ForwardAsAnthropic(context.Background(), c, messagesContextErrorTestAccount(), body, "", "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "passthrough")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "upstream_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "context window")
	require.True(t, HasOpsClientBusinessLimitedReason(c, OpsClientBusinessLimitedReasonContextLimit))
}

func TestForwardAsAnthropic_BufferedNonContextFailed_KeepsGatewayError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	failed := `{"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"content_policy","message":"Content policy violation"},"output":[]}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: " + failed + "\n\n")),
	}}
	svc := &OpenAIGatewayService{cfg: messagesContextErrorTestConfig(), httpUpstream: upstream}

	_, err := svc.ForwardAsAnthropic(context.Background(), c, messagesContextErrorTestAccount(), body, "", "")

	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "api_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.False(t, HasOpsClientBusinessLimitedReason(c, OpsClientBusinessLimitedReasonContextLimit))
}

func TestOpenAIStreamFailedEventBuiltInClientError_IgnoresContextTextOutsideError(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"status":"failed","instructions":"Document context_length_exceeded handling","error":{"code":"content_policy","message":"Content policy violation"}}}`)

	status, errType, errMsg, matched := openAIStreamFailedEventBuiltInClientError(payload, "Content policy violation")

	require.False(t, matched)
	require.Zero(t, status)
	require.Empty(t, errType)
	require.Empty(t, errMsg)
	require.False(t, isOpenAIStreamFailedEventContextWindowError(payload, "Content policy violation"))
}
