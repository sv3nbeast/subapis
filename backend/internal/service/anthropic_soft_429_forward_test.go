package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type anthropicSoft429Upstream struct {
	calls int
}

func (u *anthropicSoft429Upstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.DoWithTLS(req, "", 0, 0, nil)
}

func (u *anthropicSoft429Upstream) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls++
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"rid-anthropic-soft-429"},
		},
		Body: io.NopCloser(bytes.NewReader([]byte(`{"error":{"type":"rate_limit_error","message":"try again"}}`))),
	}, nil
}

func newAnthropicSoft429ForwardService(upstream *anthropicSoft429Upstream) *GatewayService {
	return &GatewayService{
		cfg:                 &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream:        upstream,
		rateLimitService:    &RateLimitService{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
}

func newAnthropicSoft429OAuthAccount() *Account {
	return &Account{
		ID:          319,
		Name:        "anthropic-soft-429-oauth",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Status:      StatusActive,
		Schedulable: true,
	}
}

func newAnthropicSoft429APIKeyAccount(passthrough bool) *Account {
	account := &Account{
		ID:          320,
		Name:        "anthropic-soft-429-apikey",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-key"},
		Status:      StatusActive,
		Schedulable: true,
	}
	if passthrough {
		account.Extra = map[string]any{"anthropic_passthrough": true}
	}
	return account
}

func newAnthropicSoft429GinContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c, recorder
}

func requireAnthropicSoft429Failover(t *testing.T, err error, recorder *httptest.ResponseRecorder, upstream *anthropicSoft429Upstream) {
	t.Helper()
	var failoverErr *UpstreamFailoverError
	require.Error(t, err)
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.AnthropicSoftRateLimit)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, "rid-anthropic-soft-429", failoverErr.ResponseHeaders.Get("X-Request-Id"))
	require.Empty(t, recorder.Body.String(), "the service must not write before handler failover")
	require.Equal(t, 1, upstream.calls, "a soft 429 must bypass generic same-account retries")
}

func TestGatewayService_Forward_ClassifiesAnthropicSoft429BeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &anthropicSoft429Upstream{}
	svc := newAnthropicSoft429ForwardService(upstream)
	c, recorder := newAnthropicSoft429GinContext()
	parsed := &ParsedRequest{
		Body:   NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)),
		Model:  "claude-sonnet-4-6",
		Stream: false,
	}

	_, err := svc.Forward(context.Background(), c, newAnthropicSoft429OAuthAccount(), parsed)

	requireAnthropicSoft429Failover(t, err, recorder, upstream)
}

func TestGatewayService_Forward_AgentClassifierSoft429BeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &anthropicSoft429Upstream{}
	svc := newAnthropicSoft429ForwardService(upstream)
	c, recorder := newAnthropicSoft429GinContext()
	parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(claudeCodeAgentClassifierBodyForTest()), PlatformAnthropic)
	require.NoError(t, parseErr)
	ctx := SetClaudeCodeClient(context.Background(), true)

	_, err := svc.Forward(ctx, c, newAnthropicSoft429OAuthAccount(), parsed)

	requireAnthropicSoft429Failover(t, err, recorder, upstream)
	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events := rawEvents.([]*OpsUpstreamErrorEvent)
	require.NotEmpty(t, events)
	require.True(t, events[len(events)-1].NativeNonStream)
	require.Equal(t, anthropicNativeNonStreamAgentClassifier, events[len(events)-1].NativeNonStreamKind)
	require.NotNil(t, events[len(events)-1].UpstreamStream)
	require.False(t, *events[len(events)-1].UpstreamStream)
}

func TestGatewayService_ForwardPassthrough_ClassifiesAnthropicSoft429BeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &anthropicSoft429Upstream{}
	svc := newAnthropicSoft429ForwardService(upstream)
	c, recorder := newAnthropicSoft429GinContext()
	parsed := &ParsedRequest{
		Body:   NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)),
		Model:  "claude-sonnet-4-6",
		Stream: false,
	}

	_, err := svc.Forward(context.Background(), c, newAnthropicSoft429APIKeyAccount(true), parsed)

	requireAnthropicSoft429Failover(t, err, recorder, upstream)
}

func TestGatewayService_ForwardAsChatCompletions_ClassifiesAnthropicSoft429BeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &anthropicSoft429Upstream{}
	svc := newAnthropicSoft429ForwardService(upstream)
	c, recorder := newAnthropicSoft429GinContext()
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)

	_, err := svc.ForwardAsChatCompletions(context.Background(), c, newAnthropicSoft429APIKeyAccount(false), body, nil)

	requireAnthropicSoft429Failover(t, err, recorder, upstream)
}

func TestGatewayService_ForwardAsResponses_ClassifiesAnthropicSoft429BeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &anthropicSoft429Upstream{}
	svc := newAnthropicSoft429ForwardService(upstream)
	c, recorder := newAnthropicSoft429GinContext()
	body := []byte(`{"model":"claude-sonnet-4-6","input":"hello"}`)

	_, err := svc.ForwardAsResponses(context.Background(), c, newAnthropicSoft429APIKeyAccount(false), body, nil)

	requireAnthropicSoft429Failover(t, err, recorder, upstream)
}

func TestGatewayService_AnthropicSoft429StreamingPathsClassifyBeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name    string
		forward func(*GatewayService, *gin.Context) error
	}{
		{
			name: "messages",
			forward: func(svc *GatewayService, c *gin.Context) error {
				_, err := svc.Forward(context.Background(), c, newAnthropicSoft429OAuthAccount(), &ParsedRequest{
					Body:   NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hello"}]}`)),
					Model:  "claude-sonnet-4-6",
					Stream: true,
				})
				return err
			},
		},
		{
			name: "messages_passthrough",
			forward: func(svc *GatewayService, c *gin.Context) error {
				_, err := svc.Forward(context.Background(), c, newAnthropicSoft429APIKeyAccount(true), &ParsedRequest{
					Body:   NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hello"}]}`)),
					Model:  "claude-sonnet-4-6",
					Stream: true,
				})
				return err
			},
		},
		{
			name: "chat_completions",
			forward: func(svc *GatewayService, c *gin.Context) error {
				_, err := svc.ForwardAsChatCompletions(context.Background(), c, newAnthropicSoft429APIKeyAccount(false), []byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hello"}]}`), nil)
				return err
			},
		},
		{
			name: "responses",
			forward: func(svc *GatewayService, c *gin.Context) error {
				_, err := svc.ForwardAsResponses(context.Background(), c, newAnthropicSoft429APIKeyAccount(false), []byte(`{"model":"claude-sonnet-4-6","stream":true,"input":"hello"}`), nil)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &anthropicSoft429Upstream{}
			svc := newAnthropicSoft429ForwardService(upstream)
			c, recorder := newAnthropicSoft429GinContext()

			requireAnthropicSoft429Failover(t, tc.forward(svc, c), recorder, upstream)
		})
	}
}
