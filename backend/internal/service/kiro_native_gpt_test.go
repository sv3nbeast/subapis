package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const kiroNativeGPTTestModel = "gpt-5.6-sol"

func newKiroNativeGPTTestRuntime(t *testing.T, responseText string) (*GatewayService, *httpUpstreamRecorder, *Account) {
	t.Helper()
	upstream := &httpUpstreamRecorder{resp: kiroEventStreamResponse(t, responseText, 11, 5)}
	svc := &GatewayService{
		cfg:                 &config.Config{},
		httpUpstream:        upstream,
		kiroCooldownStore:   &testKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          1701,
		Name:        "kiro-native-gpt",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/KIRO",
			"model_mapping": map[string]any{
				kiroNativeGPTTestModel: kiroNativeGPTTestModel,
			},
		},
	}
	return svc, upstream, account
}

func assertKiroNativeGPTUpstreamRequest(t *testing.T, upstream *httpUpstreamRecorder) {
	t.Helper()
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
	require.Equal(t, kiroNativeGPTTestModel, gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
}

func TestForwardAsChatCompletionsKiroUsesNativeGPTModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello chat"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "native gpt chat ok")

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: kiroNativeGPTTestModel,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assertKiroNativeGPTUpstreamRequest(t, upstream)
	require.Equal(t, "native gpt chat ok", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
	require.Equal(t, kiroNativeGPTTestModel, result.Model)
	require.Equal(t, kiroNativeGPTTestModel, result.UpstreamModel)
}

func TestForwardAsResponsesKiroUsesNativeGPTModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"type":"input_text","text":"hello responses"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "native gpt responses ok")

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: kiroNativeGPTTestModel,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assertKiroNativeGPTUpstreamRequest(t, upstream)
	require.Equal(t, "native gpt responses ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.Equal(t, kiroNativeGPTTestModel, result.Model)
	require.Equal(t, kiroNativeGPTTestModel, result.UpstreamModel)
}

func TestForwardMessagesKiroUsesNativeGPTModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","max_tokens":256,"messages":[{"role":"user","content":"hello messages"}],"stream":false}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "native gpt messages ok")

	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	assertKiroNativeGPTUpstreamRequest(t, upstream)
	require.Equal(t, "native gpt messages ok", gjson.Get(rec.Body.String(), "content.0.text").String())
	require.Equal(t, kiroNativeGPTTestModel, result.Model)
	require.Equal(t, kiroNativeGPTTestModel, result.UpstreamModel)
}
