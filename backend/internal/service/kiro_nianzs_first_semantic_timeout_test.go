package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNianzsKiroFirstSemanticTimeoutForRequestPrecedence(t *testing.T) {
	awsGroupID := int64(29)
	unlistedGroupID := int64(30)
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		FirstSemanticTimeout: 50,
		KiroResilience: config.GatewayKiroResilienceConfig{
			Mode:                        config.KiroResilienceModeEnforce,
			GroupIDs:                    []int64{awsGroupID},
			FirstSemanticTimeoutSeconds: 90,
		},
	}}}

	require.Equal(t, 90*time.Second, svc.nianzsKiroFirstSemanticTimeoutForRequest(context.Background(), &awsGroupID))
	require.Equal(t, 50*time.Second, svc.nianzsKiroFirstSemanticTimeoutForRequest(context.Background(), &unlistedGroupID))

	subscriptionPolicy := KiroAnthropicFallbackPolicy{
		Enabled:              true,
		FirstSemanticTimeout: 60 * time.Second,
		MaxAnthropicAttempts: 2,
	}
	ctx := WithKiroAnthropicFallbackPolicy(context.Background(), subscriptionPolicy)
	require.Equal(t, 60*time.Second, svc.nianzsKiroFirstSemanticTimeoutForRequest(ctx, &awsGroupID))
}

func TestGatewayStreamingResponseHonorsRequestFirstSemanticTimeoutOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := &GatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			FirstSemanticTimeout: 1,
			MaxLineSize:          defaultMaxLineSize,
		}},
		rateLimitService: &RateLimitService{},
	}

	reader, writer := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       reader,
	}

	started := time.Now()
	ctx := withGatewayFirstSemanticTimeoutOverride(context.Background(), 25*time.Millisecond)
	result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 29}, started, "claude-opus-4-8", "claude-opus-4-8", false)
	elapsed := time.Since(started)
	_ = writer.Close()
	_ = reader.Close()

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, UpstreamFailureFirstSemanticTimeout, failoverErr.FailureKind)
	require.True(t, failoverErr.PreSemanticTimeout)
	require.Nil(t, result)
	require.Empty(t, recorder.Body.String())
	require.Less(t, elapsed, 500*time.Millisecond, "request override must win over the one-second global timeout")
}

func TestNianzsKiroRouteUsesResilienceGroupFirstSemanticTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(29)
	body := []byte(`{"model":"claude-opus-4-8","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"wait for semantic output"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	parsed.GroupID = &groupID

	upstreamReader, upstreamWriter := io.Pipe()
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       upstreamReader,
	}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
	svc.cfg.Gateway.FirstSemanticTimeout = 5
	svc.cfg.Gateway.KiroResilience = config.GatewayKiroResilienceConfig{
		Mode:                        config.KiroResilienceModeEnforce,
		GroupIDs:                    []int64{groupID},
		FirstSemanticTimeoutSeconds: 1,
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	started := time.Now()
	result, err := svc.Forward(context.Background(), c, account, parsed)
	elapsed := time.Since(started)
	_ = upstreamWriter.Close()
	_ = upstreamReader.Close()

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, UpstreamFailureFirstSemanticTimeout, failoverErr.FailureKind)
	require.True(t, failoverErr.PreSemanticTimeout)
	require.Nil(t, result)
	require.Empty(t, recorder.Body.String(), "Nianzs timeout must stay invisible so the handler can switch accounts")
	require.GreaterOrEqual(t, elapsed, 900*time.Millisecond)
	require.Less(t, elapsed, 2*time.Second, "group timeout must win over the five-second gateway default")
}
