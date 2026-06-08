package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAIChatFailingWriter struct {
	gin.ResponseWriter
	failAfter int
	writes    int
}

func (w *openAIChatFailingWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed: client disconnected")
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}

type testKiroCooldownStore struct{}

func (s *testKiroCooldownStore) ReserveRequest(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *testKiroCooldownStore) MarkSuccess(context.Context, string) error {
	return nil
}

func (s *testKiroCooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *testKiroCooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *testKiroCooldownStore) GetState(context.Context, string) (*kirocooldown.State, error) {
	return nil, nil
}

func (s *testKiroCooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	return false, nil
}

func TestNormalizeResponsesRequestServiceTier(t *testing.T) {
	t.Parallel()

	req := &apicompat.ResponsesRequest{ServiceTier: " fast "}
	normalizeResponsesRequestServiceTier(req)
	require.Equal(t, "priority", req.ServiceTier)

	req.ServiceTier = "flex"
	normalizeResponsesRequestServiceTier(req)
	require.Equal(t, "flex", req.ServiceTier)

	// OpenAI 官方合法 tier 应被透传保留。
	req.ServiceTier = "auto"
	normalizeResponsesRequestServiceTier(req)
	require.Equal(t, "auto", req.ServiceTier)

	req.ServiceTier = "default"
	normalizeResponsesRequestServiceTier(req)
	require.Equal(t, "default", req.ServiceTier)

	req.ServiceTier = "scale"
	normalizeResponsesRequestServiceTier(req)
	require.Equal(t, "scale", req.ServiceTier)

	// 真未知值仍被剥离。
	req.ServiceTier = "turbo"
	normalizeResponsesRequestServiceTier(req)
	require.Empty(t, req.ServiceTier)
}

func TestNormalizeResponsesBodyServiceTier(t *testing.T) {
	t.Parallel()

	body, tier, err := normalizeResponsesBodyServiceTier([]byte(`{"model":"gpt-5.1","service_tier":"fast"}`))
	require.NoError(t, err)
	require.Equal(t, "priority", tier)
	require.Equal(t, "priority", gjson.GetBytes(body, "service_tier").String())

	body, tier, err = normalizeResponsesBodyServiceTier([]byte(`{"model":"gpt-5.1","service_tier":"flex"}`))
	require.NoError(t, err)
	require.Equal(t, "flex", tier)
	require.Equal(t, "flex", gjson.GetBytes(body, "service_tier").String())

	// OpenAI 官方 tier 直接保留在 body 中（透传上游）。
	body, tier, err = normalizeResponsesBodyServiceTier([]byte(`{"model":"gpt-5.1","service_tier":"auto"}`))
	require.NoError(t, err)
	require.Equal(t, "auto", tier)
	require.Equal(t, "auto", gjson.GetBytes(body, "service_tier").String())

	body, tier, err = normalizeResponsesBodyServiceTier([]byte(`{"model":"gpt-5.1","service_tier":"default"}`))
	require.NoError(t, err)
	require.Equal(t, "default", tier)
	require.Equal(t, "default", gjson.GetBytes(body, "service_tier").String())

	body, tier, err = normalizeResponsesBodyServiceTier([]byte(`{"model":"gpt-5.1","service_tier":"scale"}`))
	require.NoError(t, err)
	require.Equal(t, "scale", tier)
	require.Equal(t, "scale", gjson.GetBytes(body, "service_tier").String())

	// 真未知值才会被删除。
	body, tier, err = normalizeResponsesBodyServiceTier([]byte(`{"model":"gpt-5.1","service_tier":"turbo"}`))
	require.NoError(t, err)
	require.Empty(t, tier)
	require.False(t, gjson.GetBytes(body, "service_tier").Exists())
}

func TestForwardAsChatCompletions_UnknownModelDoesNotUseDefaultMappedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt6","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chat_unknown_model"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"model not found"}}`)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.4")
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, "gpt6", gjson.GetBytes(upstream.lastBody, "model").String())
	require.NotEqual(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestForwardAsChatCompletions_AnthropicOpus48ThinkingAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-opus-4-8-thinking","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chat_opus_48"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"stop after capture"}}`)),
	}}

	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "anthropic-apikey",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "anthropic-key"},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, &ParsedRequest{
		Body:  body,
		Model: "claude-opus-4-8-thinking",
	})
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, "claude-opus-4-8", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "enabled", gjson.GetBytes(upstream.lastBody, "thinking.type").String())
	require.Equal(t, int64(BudgetRectifyBudgetTokens), gjson.GetBytes(upstream.lastBody, "thinking.budget_tokens").Int())
	require.Equal(t, int64(BudgetRectifyMaxTokens), gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
}

func TestForwardAsResponses_AnthropicOpus48ThinkingAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-opus-4.8-thinking","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_responses_opus_48"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"stop after capture"}}`)),
	}}

	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "anthropic-apikey",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "anthropic-key"},
	}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
		Body:  body,
		Model: "claude-opus-4.8-thinking",
	})
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, "claude-opus-4-8", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "enabled", gjson.GetBytes(upstream.lastBody, "thinking.type").String())
	require.Equal(t, int64(BudgetRectifyBudgetTokens), gjson.GetBytes(upstream.lastBody, "thinking.budget_tokens").Int())
	require.Equal(t, int64(BudgetRectifyMaxTokens), gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
}

func TestForwardAsChatCompletions_KiroUsesRuntimeRequestPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: kiroEventStreamResponse(t, "kiro chat ok", 11, 5)}
	svc := &GatewayService{
		cfg:                 &config.Config{},
		httpUpstream:        upstream,
		kiroCooldownStore:   &testKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          1459,
		Name:        "kiro-oauth",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/KIRO",
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, &ParsedRequest{
		Body:  body,
		Model: "claude-sonnet-4-6",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
	require.Equal(t, "q.us-east-1.amazonaws.com", upstream.lastReq.Host)
	require.Equal(t, "Bearer kiro-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "vibe", upstream.lastReq.Header.Get("x-amzn-kiro-agent-mode"))
	require.Equal(t, "claude-sonnet-4.6", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.content").String())
	require.Equal(t, "kiro chat ok", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
	require.Equal(t, "claude-sonnet-4-6", result.Model)
	require.Equal(t, "claude-sonnet-4.6", result.UpstreamModel)
}

func TestForwardAsResponses_KiroUsesRuntimeRequestPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-sonnet-4-6","input":[{"type":"input_text","text":"hello responses"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: kiroEventStreamResponse(t, "kiro responses ok", 12, 6)}
	svc := &GatewayService{
		cfg:                 &config.Config{},
		httpUpstream:        upstream,
		kiroCooldownStore:   &testKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          1460,
		Name:        "kiro-oauth",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/KIRO",
		},
	}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
		Body:  body,
		Model: "claude-sonnet-4-6",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
	require.Equal(t, "q.us-east-1.amazonaws.com", upstream.lastReq.Host)
	require.Equal(t, "Bearer kiro-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "vibe", upstream.lastReq.Header.Get("x-amzn-kiro-agent-mode"))
	require.Equal(t, "claude-sonnet-4.6", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
	require.Equal(t, "hello responses", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.content").String())
	require.Equal(t, "kiro responses ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.Equal(t, "claude-sonnet-4-6", result.Model)
	require.Equal(t, "claude-sonnet-4.6", result.UpstreamModel)
}

func TestForwardAsResponses_KiroExpandsPreviousResponseHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()

	firstBody := []byte(`{"model":"claude-sonnet-4-6","instructions":"project rules","input":[{"type":"input_text","text":"inspect files"}],"stream":false}`)
	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstCtx.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		anthropicMessagesSSECompletedResponse("msg_first", "claude-sonnet-4-6", "use a tool", "", 10, 3),
		anthropicMessagesSSECompletedResponse("msg_second", "claude-sonnet-4-6", "done", "", 20, 4),
	}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "kiro",
		Platform:    PlatformKiro,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "kiro-key"},
	}

	firstResult, err := svc.ForwardAsResponses(context.Background(), firstCtx, account, firstBody, &ParsedRequest{
		Body:  firstBody,
		Model: "claude-sonnet-4-6",
	})
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.Equal(t, "msg_first", firstResult.ResponseID)

	secondBody := []byte(`{"model":"claude-sonnet-4-6","previous_response_id":"msg_first","input":[{"type":"function_call_output","call_id":"call_lookup","output":"file list"}],"stream":false}`)
	secondRec := httptest.NewRecorder()
	secondCtx, _ := gin.CreateTestContext(secondRec)
	secondCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondCtx.Request.Header.Set("Content-Type", "application/json")

	secondResult, err := svc.ForwardAsResponses(context.Background(), secondCtx, account, secondBody, &ParsedRequest{
		Body:  secondBody,
		Model: "claude-sonnet-4-6",
	})
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Len(t, upstream.bodies, 2)

	body := upstream.bodies[1]
	require.Equal(t, "project rules", gjson.GetBytes(body, "system").String())
	require.Equal(t, "inspect files", gjson.GetBytes(body, "messages.0.content.0.text").String())
	require.Equal(t, "use a tool", gjson.GetBytes(body, "messages.1.content.0.text").String())
	require.Equal(t, "tool_result", gjson.GetBytes(body, "messages.2.content.0.type").String())
	require.Equal(t, "call_lookup", gjson.GetBytes(body, "messages.2.content.0.tool_use_id").String())
	require.Equal(t, "file list", gjson.GetBytes(body, "messages.2.content.0.content").String())
}

func TestForwardAsResponses_KiroStoresStreamingOutputForPreviousResponseHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()

	firstBody := []byte(`{"model":"claude-sonnet-4-6","input":[{"type":"input_text","text":"start stream"}],"stream":true}`)
	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstCtx.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		anthropicMessagesSSECompletedResponse("msg_stream_first", "claude-sonnet-4-6", "streamed answer", "", 10, 3),
		anthropicMessagesSSECompletedResponse("msg_stream_second", "claude-sonnet-4-6", "done", "", 20, 4),
	}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "kiro",
		Platform:    PlatformKiro,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "kiro-key"},
	}

	firstResult, err := svc.ForwardAsResponses(context.Background(), firstCtx, account, firstBody, &ParsedRequest{
		Body:  firstBody,
		Model: "claude-sonnet-4-6",
	})
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.Equal(t, "msg_stream_first", firstResult.ResponseID)
	require.NotEmpty(t, firstResult.ResponsesOutput)

	secondBody := []byte(`{"model":"claude-sonnet-4-6","previous_response_id":"msg_stream_first","input":[{"type":"input_text","text":"continue"}],"stream":false}`)
	secondRec := httptest.NewRecorder()
	secondCtx, _ := gin.CreateTestContext(secondRec)
	secondCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondCtx.Request.Header.Set("Content-Type", "application/json")

	secondResult, err := svc.ForwardAsResponses(context.Background(), secondCtx, account, secondBody, &ParsedRequest{
		Body:  secondBody,
		Model: "claude-sonnet-4-6",
	})
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Len(t, upstream.bodies, 2)

	body := upstream.bodies[1]
	require.Equal(t, "start stream", gjson.GetBytes(body, "messages.0.content.0.text").String())
	require.Equal(t, "streamed answer", gjson.GetBytes(body, "messages.1.content.0.text").String())
	require.Equal(t, "continue", gjson.GetBytes(body, "messages.2.content.0.text").String())
}

func TestForwardAsResponses_KiroStoreFalseDoesNotPersistHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()

	firstBody := []byte(`{"model":"claude-sonnet-4-6","input":[{"type":"input_text","text":"do not store"}],"store":false,"stream":false}`)
	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstCtx.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		anthropicMessagesSSECompletedResponse("msg_store_false", "claude-sonnet-4-6", "not stored", "", 10, 3),
	}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "kiro",
		Platform:    PlatformKiro,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "kiro-key"},
	}

	firstResult, err := svc.ForwardAsResponses(context.Background(), firstCtx, account, firstBody, &ParsedRequest{
		Body:  firstBody,
		Model: "claude-sonnet-4-6",
	})
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.Equal(t, "msg_store_false", firstResult.ResponseID)

	secondBody := []byte(`{"model":"claude-sonnet-4-6","previous_response_id":"msg_store_false","input":[{"type":"input_text","text":"continue"}],"stream":false}`)
	secondRec := httptest.NewRecorder()
	secondCtx, _ := gin.CreateTestContext(secondRec)
	secondCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondCtx.Request.Header.Set("Content-Type", "application/json")

	secondResult, err := svc.ForwardAsResponses(context.Background(), secondCtx, account, secondBody, &ParsedRequest{
		Body:  secondBody,
		Model: "claude-sonnet-4-6",
	})
	require.Error(t, err)
	require.Nil(t, secondResult)
	require.Equal(t, http.StatusNotFound, secondRec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(secondRec.Body.String(), "error.code").String())
	require.Contains(t, gjson.Get(secondRec.Body.String(), "error.message").String(), "previous_response_id not found")
	require.Len(t, upstream.bodies, 1, "missing history must fail before calling upstream")
}

func TestForwardAsResponses_KiroMissingPreviousResponseIDReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()

	body := []byte(`{"model":"claude-sonnet-4-6","previous_response_id":"resp_missing","input":[{"type":"input_text","text":"continue"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "kiro",
		Platform:    PlatformKiro,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "kiro-key"},
	}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
		Body:  body,
		Model: "claude-sonnet-4-6",
	})
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.code").String())
	require.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "previous_response_id not found")
	require.Empty(t, upstream.bodies)
}

func TestForwardAsChatCompletions_ClientDisconnectDrainsUpstreamUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Writer = &openAIChatFailingWriter{ResponseWriter: c.Writer, failAfter: 0}
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":11,"output_tokens":5,"total_tokens":16,"input_tokens_details":{"cached_tokens":4}}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_disconnect"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
}

func TestForwardAsChatCompletions_StreamsUsageWithoutClientStreamOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":13,"output_tokens":7,"total_tokens":20,"input_tokens_details":{"cached_tokens":5}}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_usage_no_stream_options"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 5, result.Usage.CacheReadInputTokens)

	responseBody := rec.Body.String()
	require.Contains(t, responseBody, `"usage"`)
	require.Contains(t, responseBody, `"prompt_tokens":13`)
	require.Contains(t, responseBody, `"completion_tokens":7`)
	require.Contains(t, responseBody, `"cached_tokens":5`)
}

func TestForwardAsChatCompletions_StreamsTopLevelTerminalUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_top","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_top","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]},"usage":{"input_tokens":21,"output_tokens":9,"total_tokens":30,"input_tokens_details":{"cached_tokens":4}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_top_level_usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 21, result.Usage.InputTokens)
	require.Equal(t, 9, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)

	responseBody := rec.Body.String()
	require.Contains(t, responseBody, `"usage"`)
	require.Contains(t, responseBody, `"prompt_tokens":21`)
	require.Contains(t, responseBody, `"completion_tokens":9`)
	require.Contains(t, responseBody, `"cached_tokens":4`)
}

func TestForwardAsChatCompletions_BufferedTopLevelTerminalUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_top_buffered","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]},"usage":{"input_tokens":18,"output_tokens":6,"total_tokens":24,"input_tokens_details":{"cached_tokens":3}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_buffered_top_level_usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 18, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)

	responseBody := rec.Body.String()
	require.Contains(t, responseBody, `"usage"`)
	require.Contains(t, responseBody, `"prompt_tokens":18`)
	require.Contains(t, responseBody, `"completion_tokens":6`)
	require.Contains(t, responseBody, `"cached_tokens":3`)
}

func TestForwardAsChatCompletions_TerminalUsageWithoutUpstreamCloseReturns(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Writer = &openAIChatFailingWriter{ResponseWriter: c.Writer, failAfter: 0}
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := []byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":17,"output_tokens":8,"total_tokens":25,"input_tokens_details":{"cached_tokens":6}}}}` + "\n\n")
	upstreamStream := newOpenAICompatBlockingReadCloser(upstreamBody)
	defer func() {
		require.NoError(t, upstreamStream.Close())
	}()
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_terminal_no_close"}},
		Body:       upstreamStream,
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	type forwardResult struct {
		result *OpenAIForwardResult
		err    error
	}
	resultCh := make(chan forwardResult, 1)
	go func() {
		result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.1")
		resultCh <- forwardResult{result: result, err: err}
	}()

	select {
	case got := <-resultCh:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.Equal(t, 17, got.result.Usage.InputTokens)
		require.Equal(t, 8, got.result.Usage.OutputTokens)
		require.Equal(t, 6, got.result.Usage.CacheReadInputTokens)
	case <-time.After(time.Second):
		require.Fail(t, "ForwardAsChatCompletions should return after terminal usage event even if upstream keeps the connection open")
	}
}

func TestForwardAsChatCompletions_EventNamedTerminalWithoutUpstreamCloseReturns(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := []byte(strings.Join([]string{
		`event: response.created`,
		`data: {"response":{"id":"resp_1","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		``,
		`event: response.output_text.delta`,
		`data: {"delta":"ok"}`,
		``,
		`event: response.completed`,
		`data: {"response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":17,"output_tokens":8,"total_tokens":25,"input_tokens_details":{"cached_tokens":6}}}}`,
		``,
		``,
	}, "\n"))
	upstreamStream := newOpenAICompatBlockingReadCloser(upstreamBody)
	defer func() {
		require.NoError(t, upstreamStream.Close())
	}()
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_event_named_terminal"}},
		Body:       upstreamStream,
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	type forwardResult struct {
		result *OpenAIForwardResult
		err    error
	}
	resultCh := make(chan forwardResult, 1)
	go func() {
		result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.1")
		resultCh <- forwardResult{result: result, err: err}
	}()

	select {
	case got := <-resultCh:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.Equal(t, 17, got.result.Usage.InputTokens)
		require.Equal(t, 8, got.result.Usage.OutputTokens)
		require.Equal(t, 6, got.result.Usage.CacheReadInputTokens)
		require.Contains(t, rec.Body.String(), `"content":"ok"`)
	case <-time.After(time.Second):
		require.Fail(t, "ForwardAsChatCompletions should use SSE event names when data payloads omit type")
	}
}

func TestForwardAsChatCompletions_EventTypeDoesNotLeakAcrossFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`event: response.created`,
		`data: {"response":{"id":"resp_1","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		``,
		`event: response.completed`,
		`data: {"response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":17,"output_tokens":8,"total_tokens":25,"input_tokens_details":{"cached_tokens":6}}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_event_boundary"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"content":"ok"`)
	require.Contains(t, rec.Body.String(), `data: [DONE]`)
}

func TestForwardAsChatCompletions_BufferedTerminalWithoutUpstreamCloseReturns(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := []byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":17,"output_tokens":8,"total_tokens":25,"input_tokens_details":{"cached_tokens":6}}}}` + "\n\n")
	upstreamStream := newOpenAICompatBlockingReadCloser(upstreamBody)
	defer func() {
		require.NoError(t, upstreamStream.Close())
	}()
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_buffered_terminal_no_close"}},
		Body:       upstreamStream,
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	type forwardResult struct {
		result *OpenAIForwardResult
		err    error
	}
	resultCh := make(chan forwardResult, 1)
	go func() {
		result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.1")
		resultCh <- forwardResult{result: result, err: err}
	}()

	select {
	case got := <-resultCh:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.Equal(t, 17, got.result.Usage.InputTokens)
		require.Equal(t, 8, got.result.Usage.OutputTokens)
		require.Equal(t, 6, got.result.Usage.CacheReadInputTokens)
		require.Contains(t, rec.Body.String(), `"finish_reason":"stop"`)
	case <-time.After(time.Second):
		require.Fail(t, "ForwardAsChatCompletions buffered response should return after terminal usage event even if upstream keeps the connection open")
	}
}

func TestForwardAsChatCompletions_DoneSentinelWithoutTerminalReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := "data: [DONE]\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_missing_terminal"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.NotNil(t, result)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
}

func TestForwardAsChatCompletions_UpstreamRequestIgnoresClientCancel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	reqCtx, cancel := context.WithCancel(context.Background())
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(reqCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	cancel()

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_ctx"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsChatCompletions(reqCtx, c, account, body, "", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.NoError(t, upstream.lastReq.Context().Err())
}

func resetKiroResponsesHistoryStoreForTest() {
	globalKiroResponsesHistoryStore = &kiroResponsesHistoryStore{
		items: make(map[string]kiroResponsesHistoryEntry),
		now:   time.Now,
		dir:   "",
	}
}

func anthropicMessagesSSECompletedResponse(responseID, model, text, stopReason string, inputTokens, outputTokens int) *http.Response {
	if stopReason == "" {
		stopReason = "end_turn"
	}
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"` + responseID + `","type":"message","role":"assistant","content":[],"model":"` + model + `","usage":{"input_tokens":` + fmt.Sprint(inputTokens) + `}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"` + stopReason + `"},"usage":{"output_tokens":` + fmt.Sprint(outputTokens) + `}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_" + responseID}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func kiroEventStreamResponse(t *testing.T, text string, inputTokens, outputTokens int) *http.Response {
	t.Helper()
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": text},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": inputTokens,
				"outputTokens":        outputTokens,
			},
		},
	}))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-request-id": []string{"rid_kiro_runtime"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
}

func kiroEventStreamFrame(t *testing.T, eventType string, payload any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	headers := map[string]string{":event-type": eventType, ":message-type": "event"}
	headerBytes := bytes.NewBuffer(nil)
	for name, value := range headers {
		headerBytes.WriteByte(byte(len(name)))
		headerBytes.WriteString(name)
		headerBytes.WriteByte(7)
		headerBytes.WriteByte(byte(len(value) >> 8))
		headerBytes.WriteByte(byte(len(value)))
		headerBytes.WriteString(value)
	}
	totalLen := 16 + headerBytes.Len() + len(payloadBytes)
	frame := bytes.NewBuffer(make([]byte, 0, totalLen))
	_ = binary.Write(frame, binary.BigEndian, uint32(totalLen))
	_ = binary.Write(frame, binary.BigEndian, uint32(headerBytes.Len()))
	_ = binary.Write(frame, binary.BigEndian, uint32(0))
	frame.Write(headerBytes.Bytes())
	frame.Write(payloadBytes)
	_ = binary.Write(frame, binary.BigEndian, uint32(0))
	return frame.Bytes()
}
