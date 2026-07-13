package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKiroInputTokenBudgetPrefersSemanticPromptOverWireEnvelope(t *testing.T) {
	requestCtx := kiropkg.KiroRequestContext{InputTokenBudget: 52_921}

	effective := kiroInputTokenBudget(&requestCtx, 31_181)

	require.Equal(t, 31_181, effective)
	require.Equal(t, 31_181, requestCtx.SemanticInputTokenBudget)
	require.Equal(t, 52_921, requestCtx.InputTokenBudget, "wire hard bound must remain available for exact upstream usage")
}

func TestKiroInputTokenBudgetStillHonorsSmallerWireBound(t *testing.T) {
	requestCtx := kiropkg.KiroRequestContext{InputTokenBudget: 20_000}

	effective := kiroInputTokenBudget(&requestCtx, 31_181)

	require.Equal(t, 20_000, effective)
	require.Equal(t, 31_181, requestCtx.SemanticInputTokenBudget)
}

func TestKiroInputTokenBudgetDoesNotCapBinaryPromptToTextOnlyEstimate(t *testing.T) {
	requestCtx := kiropkg.KiroRequestContext{InputTokenBudget: 18_000}
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}]}`)

	effective := kiroInputTokenBudgetForBody(&requestCtx, body, 4)

	require.Equal(t, 18_000, effective)
	require.Zero(t, requestCtx.SemanticInputTokenBudget)
}

func TestKiroUsageToClaudeDoesNotDoubleCountCacheOnlyInput(t *testing.T) {
	usage := kiroUsageToClaude(kiropkg.Usage{
		InputTokens:              0,
		CacheReadInputTokens:     20_596,
		CacheCreationInputTokens: 15_563,
		OutputTokens:             430,
	}, 36_159)

	require.Zero(t, usage.InputTokens)
	require.Equal(t, 20_596, usage.CacheReadInputTokens)
	require.Equal(t, 15_563, usage.CacheCreationInputTokens)
	require.Equal(t, 36_159, usage.InputTokens+usage.CacheReadInputTokens+usage.CacheCreationInputTokens)
}

func TestKiroUsageToClaudeUsesFallbackOnlyWhenAllInputUsageMissing(t *testing.T) {
	usage := kiroUsageToClaude(kiropkg.Usage{OutputTokens: 7}, 1234)
	require.Equal(t, 1234, usage.InputTokens)
}

func TestKiro429FailoverAccountsOnlySuccessfulTerminalUsage(t *testing.T) {
	resetKiroCacheTracker()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	successBody := bytes.NewBuffer(nil)
	_, _ = successBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":"ok after 429"}}`)))
	_, _ = successBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageMetadataEvent",
	}, []byte(`{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":100,"outputTokens":7,"totalTokens":107}}}`)))
	_, _ = successBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stopReason":"end_turn"}}`)))

	upstream := &kiroStreamFailoverQueuedUpstream{responses: []*http.Response{
		{
			StatusCode: http.StatusTooManyRequests,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"message":"slow down"}`)),
		},
		newKiroEventStreamResponse(http.StatusOK, successBody.Bytes()),
	}}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          1701,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"api_region":   "us-east-1",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"messages":[{"role":"user","content":"stable prompt"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	parsed.Group = kiroCacheGroup(1)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, 100, result.Usage.InputTokens+result.Usage.CacheReadInputTokens+result.Usage.CacheCreationInputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "ok after 429")
}
