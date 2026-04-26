package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIClientCanceledClassificationRequiresInboundContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	upstreamErr := fmt.Errorf(`Post "https://chatgpt.com/backend-api/codex/responses": %w`, context.Canceled)

	require.True(t, shouldTreatOpenAIRequestErrorAsClientCanceled(ctx, upstreamErr))
	wrapped := newOpenAIClientCanceledError(upstreamErr)
	require.True(t, IsOpenAIClientCanceledError(wrapped))
	require.ErrorIs(t, wrapped, context.Canceled)

	require.False(t, shouldTreatOpenAIRequestErrorAsClientCanceled(context.Background(), upstreamErr))
	require.True(t, shouldTreatOpenAIRequestErrorAsClientCanceled(ctx, fmt.Errorf("unexpected EOF")))
	require.False(t, shouldTreatOpenAIRequestErrorAsClientCanceled(context.Background(), fmt.Errorf("unexpected EOF")))
}

func TestForwardAsChatCompletionsClientCanceledSkipsOpsUpstreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{
		err: fmt.Errorf(`Post "https://chatgpt.com/backend-api/codex/responses": %w`, context.Canceled),
	}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          864,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsChatCompletions(ctx, c, account, body, "", "gpt-5.5")
	require.Nil(t, result)
	require.True(t, IsOpenAIClientCanceledError(err), "got %v", err)
	require.False(t, c.Writer.Written())
	_, exists := c.Get(OpsUpstreamErrorsKey)
	require.False(t, exists)
}
