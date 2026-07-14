package handler

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIForwardResultFromGatewayPreservesKiroUsage(t *testing.T) {
	effort := "max"
	firstTokenMs := 123
	result := openAIForwardResultFromGateway(&service.ForwardResult{
		RequestID:     "req_kiro",
		ResponseID:    "resp_kiro",
		Model:         service.OpenAIKiroBridgeModel,
		UpstreamModel: service.OpenAIKiroBridgeModel,
		Usage: service.ClaudeUsage{
			InputTokens:              100,
			OutputTokens:             20,
			CacheCreationInputTokens: 5,
			CacheReadInputTokens:     30,
			KiroCredits:              0.17,
		},
		Stream:          true,
		Duration:        2 * time.Second,
		FirstTokenMs:    &firstTokenMs,
		ReasoningEffort: &effort,
	})

	require.NotNil(t, result)
	require.Equal(t, "req_kiro", result.RequestID)
	require.Equal(t, service.OpenAIKiroBridgeModel, result.BillingModel)
	require.Equal(t, 135, result.Usage.InputTokens)
	require.Equal(t, 30, result.Usage.CacheReadInputTokens)
	require.InDelta(t, 0.17, result.Usage.KiroCredits, 1e-9)
	require.Equal(t, &effort, result.ReasoningEffort)
	require.Equal(t, &firstTokenMs, result.FirstTokenMs)
}

func TestOpenAIKiroBridgeEndpointScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		path      string
		responses bool
		chat      bool
	}{
		{path: "/v1/responses", responses: true},
		{path: "/backend-api/codex/responses", responses: true},
		{path: "/v1/responses/compact"},
		{path: "/v1/messages"},
		{path: "/v1/chat/completions", chat: true},
		{path: "/chat/completions", chat: true},
		{path: "/v1/embeddings"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tt.path, nil)
			require.Equal(t, tt.responses, isOpenAIKiroBridgeResponsesRequest(c, service.PlatformOpenAI, service.OpenAIKiroBridgeModel))
			require.Equal(t, tt.chat, isOpenAIKiroBridgeChatRequest(c, service.PlatformOpenAI, service.OpenAIKiroBridgeModel))
			require.False(t, isOpenAIKiroBridgeResponsesRequest(c, service.PlatformGrok, service.OpenAIKiroBridgeModel))
			require.False(t, isOpenAIKiroBridgeChatRequest(c, service.PlatformOpenAI, "gpt-5.4"))
		})
	}
}
