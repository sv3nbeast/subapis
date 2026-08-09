package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newAnthropicOAuthNativeAccountForTest() *Account {
	return &Account{
		ID:          901,
		Name:        "anthropic-oauth-native-test",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeSetupToken,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "oauth-upstream-token",
			"model_mapping": map[string]any{
				"claude-opus-5-thinking": "should-never-be-used-in-native-mode",
			},
		},
		Extra:       map[string]any{anthropicOAuthPassthroughExtra: true},
		Status:      StatusActive,
		Schedulable: true,
	}
}

func newNativePassthroughTestContext(t *testing.T, path string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.220 (external, cli)")
	c.Request.Header.Set("Authorization", "Bearer inbound-token")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")
	c.Request.Header.Set("Cookie", "session=secret")
	c.Request.Header.Set("Anthropic-Version", "2023-06-01")
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
	c.Request.Header.Set("X-Stainless-Runtime", "node")
	c.Request.Header.Set("X-Client-Request-Id", "client-rid")
	c.Request.Header.Set("X-Request-Id", "request-rid")
	c.Request.Header.Set("X-Session-Id", "session-rid")
	return c, rec
}

type closeBlockingReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

type prefixThenCloseBlockingReadCloser struct {
	reader *bytes.Reader
	closed chan struct{}
	once   sync.Once
}

func newPrefixThenCloseBlockingReadCloser(prefix []byte) *prefixThenCloseBlockingReadCloser {
	return &prefixThenCloseBlockingReadCloser{reader: bytes.NewReader(prefix), closed: make(chan struct{})}
}

func (r *prefixThenCloseBlockingReadCloser) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	<-r.closed
	return 0, io.EOF
}

func (r *prefixThenCloseBlockingReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

type nativePassthroughHTTPUpstream struct {
	mu    sync.Mutex
	calls int
	do    func(*http.Request) (*http.Response, error)
}

type nativePassthroughAccountRepo struct {
	AccountRepository
	setErrorCalls int
	lastError     string
}

type nativePassthroughTrackedBody struct {
	io.Reader
	closed bool
}

func (b *nativePassthroughTrackedBody) Close() error {
	b.closed = true
	return nil
}

func (r *nativePassthroughAccountRepo) SetError(_ context.Context, _ int64, message string) error {
	r.setErrorCalls++
	r.lastError = message
	return nil
}

func (u *nativePassthroughHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	return u.do(req)
}

func (u *nativePassthroughHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *nativePassthroughHTTPUpstream) Calls() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

func newCloseBlockingReadCloser() *closeBlockingReadCloser {
	return &closeBlockingReadCloser{closed: make(chan struct{})}
}

func (r *closeBlockingReadCloser) Read(_ []byte) (int, error) {
	<-r.closed
	return 0, io.EOF
}

func (r *closeBlockingReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func TestAnthropicOAuthNativePassthrough_RequestPreservesBodyHeadersAndAuthReplacement(t *testing.T) {
	c, _ := newNativePassthroughTestContext(t, "/v1/messages")
	account := newAnthropicOAuthNativeAccountForTest()
	account.Extra["custom_base_url_enabled"] = true
	account.Extra["custom_base_url"] = "https://relay.example.com/"
	original := []byte(`{"model":"claude-opus-5","stream":false,"system":[{"type":"text","text":"Todayʹs date is 2026/08/09."}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"thinking":{"type":"adaptive"},"metadata":{"user_id":"native-user"}}`)
	svc := &GatewayService{}
	req, err := svc.buildAnthropicOAuthNativeRequest(context.Background(), c, account, original, "oauth-upstream-token")
	require.NoError(t, err)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.True(t, bytes.Equal(original, body), "strict passthrough must keep the original JSON bytes")
	require.Equal(t, "https://relay.example.com/v1/messages", req.URL.String(), "strict passthrough must not append beta or relay query parameters")
	require.Equal(t, "Bearer oauth-upstream-token", getHeaderRaw(req.Header, "authorization"))
	require.Empty(t, getHeaderRaw(req.Header, "x-api-key"))
	require.Empty(t, getHeaderRaw(req.Header, "cookie"))
	require.Equal(t, "claude-cli/2.1.220 (external, cli)", getHeaderRaw(req.Header, "user-agent"))
	require.Equal(t, "interleaved-thinking-2025-05-14", getHeaderRaw(req.Header, "anthropic-beta"))
	require.Equal(t, "node", getHeaderRaw(req.Header, "x-stainless-runtime"))
	require.Equal(t, "client-rid", getHeaderRaw(req.Header, "x-client-request-id"))
	require.Equal(t, "request-rid", getHeaderRaw(req.Header, "x-request-id"))
	require.Equal(t, "session-rid", getHeaderRaw(req.Header, "x-session-id"))
}

func TestAnthropicOAuthNativePassthrough_UsesIngressHeaderSnapshotAfterPriorAttemptMutation(t *testing.T) {
	c, _ := newNativePassthroughTestContext(t, "/v1/messages")
	CaptureAnthropicOAuthNativeIngressHeaders(c)
	c.Request.Header.Set("User-Agent", "gateway-mimic/changed")
	c.Request.Header.Set("Anthropic-Beta", "bedrock-filtered-token")

	svc := &GatewayService{}
	req, err := svc.buildAnthropicOAuthNativeRequest(
		context.Background(),
		c,
		newAnthropicOAuthNativeAccountForTest(),
		[]byte(`{"model":"claude-opus-5","messages":[]}`),
		"oauth-upstream-token",
	)
	require.NoError(t, err)
	require.Equal(t, "claude-cli/2.1.220 (external, cli)", getHeaderRaw(req.Header, "user-agent"))
	require.Equal(t, "interleaved-thinking-2025-05-14", getHeaderRaw(req.Header, "anthropic-beta"))
}

func TestGatewayService_AnthropicOAuthNativePassthrough_ForwardDoesNotRewriteBody(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5-thinking","stream":false,"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.220; cc_entrypoint=cli; cch=native;"},{"type":"text","text":"Todayʹs date is 2026/08/09."}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"thinking":{"type":"adaptive"},"metadata":{"user_id":"native-user"}}`)
	upstreamBody := `{"id":"msg_native","type":"message","role":"assistant","model":"claude-opus-5-thinking","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":4,"output_tokens":2}}`
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-native"}},
		Body:       io.NopCloser(bytes.NewBufferString(upstreamBody)),
	}}
	svc := &GatewayService{
		cfg:                  &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		responseHeaderFilter: compileResponseHeaderFilter(&config.Config{}),
		httpUpstream:         upstream,
	}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(original), PlatformAnthropic)
	require.NoError(t, err)
	require.NotEqual(t, string(original), string(parsed.Body.Bytes()), "legacy working body should still strip the billing attribution block")
	parsed.Model = "channel-mapped-alias"
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, bytes.Equal(original, upstream.lastBody), "native Forward must not normalize date, model, thinking, cache, or system")
	require.Equal(t, "claude-opus-5-thinking", result.Model, "usage attribution must use the model present on the preserved wire body")
	require.Equal(t, "claude-opus-5-thinking", result.UpstreamModel)
	require.Equal(t, "Bearer oauth-upstream-token", getHeaderRaw(upstream.lastReq.Header, "authorization"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "cookie"))
	require.Equal(t, upstreamBody, rec.Body.String())
}

func TestGatewayService_AnthropicOAuthNativePassthrough_DoesNotTriggerCompanionRequests(t *testing.T) {
	c, _ := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":false,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	upstream := &nativePassthroughHTTPUpstream{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"msg_native","type":"message","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":4,"output_tokens":2}}`,
			)),
		}, nil
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{
		ClaudeCodeMimicry: config.GatewayClaudeCodeMimicryConfig{
			Enabled: true,
			SyntheticCompanion: config.GatewayClaudeCodeSyntheticCompanionConfig{
				Enabled: true,
				Mode:    config.ClaudeCodeSyntheticCompanionModeAuxAndTitle,
			},
		},
	}}
	svc := &GatewayService{
		cfg:                      cfg,
		responseHeaderFilter:     compileResponseHeaderFilter(cfg),
		httpUpstream:             upstream,
		claudeCodeCompanionProbe: NewClaudeCodeCompanionProbeService(upstream),
	}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), OriginalBody: original, Model: "claude-opus-5"}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, upstream.Calls())
	require.Never(t, func() bool { return upstream.Calls() > 1 }, 150*time.Millisecond, 10*time.Millisecond,
		"native strict mode must not launch synthetic companion endpoints")
}

func TestGatewayService_AnthropicOAuthNativePassthrough_ForwardCountTokensDoesNotRewriteBody(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages/count_tokens")
	original := []byte(`{"model":"claude-opus-5-thinking","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"thinking":{"type":"adaptive"},"metadata":{"user_id":"native-user"}}`)
	upstreamBody := `{"input_tokens":17}`
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-count-native"}},
		Body:       io.NopCloser(bytes.NewBufferString(upstreamBody)),
	}}
	svc := &GatewayService{
		cfg:                  &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		responseHeaderFilter: compileResponseHeaderFilter(&config.Config{}),
		httpUpstream:         upstream,
	}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5-thinking"}
	err := svc.ForwardCountTokens(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.NoError(t, err)
	require.True(t, bytes.Equal(original, upstream.lastBody), "native count_tokens must keep the original JSON bytes")
	require.Equal(t, "Bearer oauth-upstream-token", getHeaderRaw(upstream.lastReq.Header, "authorization"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "cookie"))
	require.Equal(t, upstreamBody, rec.Body.String())
}

func TestGatewayService_AnthropicOAuthNativePassthrough_ForwardCountTokensPreservesErrorResponse(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages/count_tokens")
	original := []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hello"}]}`)
	upstreamBody := `{"type":"error","error":{"type":"invalid_request_error","message":"native count error"},"extra":{"preserve":true}}`
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTeapot,
		Header: http.Header{
			"Content-Type": []string{"application/problem+json"},
			"x-request-id": []string{"rid-count-native-error"},
		},
		Body: io.NopCloser(bytes.NewBufferString(upstreamBody)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
	}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), OriginalBody: original, Model: "claude-opus-5"}
	err := svc.ForwardCountTokens(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.ErrorContains(t, err, "native count error")
	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	require.Equal(t, upstreamBody, rec.Body.String(), "strict count_tokens errors must remain byte-for-byte intact")
	require.True(t, IsResponseCommitted(c))
}

func TestGatewayService_AnthropicOAuthNativePassthrough_NonFailoverErrorPreservesResponseBody(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	upstreamBody := `{"type":"error","error":{"type":"invalid_request_error","message":"native error detail"},"extra":{"preserve":true}}`
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTeapot,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-native-error"}},
		Body:       io.NopCloser(bytes.NewBufferString(upstreamBody)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5"}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Equal(t, upstreamBody, rec.Body.String(), "strict native errors must not be rewritten into generic gateway text")
}

func TestGatewayService_AnthropicOAuthNativePassthrough_StreamingCopiesSSEBytesExactly(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	upstreamSSE := "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11}}}\r\n\r\n" +
		"event: content_block_delta\r\ndata:  {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\r\n\r\n" +
		"event: message_delta\r\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\r\n\r\n" +
		"event: message_stop\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-stream-native"}},
		Body:       io.NopCloser(bytes.NewBufferString(upstreamSSE)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
	}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5", Stream: true}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, upstreamSSE, rec.Body.String())
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.NotNil(t, result.FirstTokenMs)
}

func TestAnthropicOAuthNativeSSEHasSemanticOutput(t *testing.T) {
	require.False(t, anthropicOAuthNativeSSEHasSemanticOutput(`{"type":"message_start","message":{"usage":{"input_tokens":11}}}`))
	require.False(t, anthropicOAuthNativeSSEHasSemanticOutput(`{"type":"content_block_start","content_block":{"type":"text","text":""}}`))
	require.False(t, anthropicOAuthNativeSSEHasSemanticOutput(`{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"sig"}}`))
	require.True(t, anthropicOAuthNativeSSEHasSemanticOutput(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`))
	require.True(t, anthropicOAuthNativeSSEHasSemanticOutput(`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"plan"}}`))
	require.True(t, anthropicOAuthNativeSSEHasSemanticOutput(`{"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_1","name":"Read"}}`))
}

func TestGatewayService_AnthropicOAuthNativePassthrough_RecognizesTerminalEventWithoutData(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	upstreamSSE := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4}}}\n\n" +
		"event: message_stop\n\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(upstreamSSE)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5", Stream: true}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, upstreamSSE, rec.Body.String())
	require.Nil(t, result.FirstTokenMs, "message_start and message_stop are not semantic output")
}

func TestGatewayService_AnthropicOAuthNativePassthrough_EventOnlyTerminalWithoutDelimiterIsIncomplete(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	truncatedSSE := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4}}}\n\n" +
		"event: message_stop\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(truncatedSSE)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5", Stream: true}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.ErrorContains(t, err, "missing terminal event")
	require.NotNil(t, result, "usage before an incomplete terminal record remains billable")
	require.Equal(t, truncatedSSE, rec.Body.String())
	require.Nil(t, result.FirstTokenMs)
}

func TestGatewayService_AnthropicOAuthNativePassthrough_DataTerminalWithoutDelimiterIsIncomplete(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	truncatedSSE := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4}}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(truncatedSSE)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5", Stream: true}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.ErrorContains(t, err, "missing terminal event")
	require.NotNil(t, result, "usage before an incomplete terminal record remains billable")
	require.Equal(t, truncatedSSE, rec.Body.String())
	require.Nil(t, result.FirstTokenMs)
}

func TestGatewayService_AnthropicOAuthNativePassthrough_StreamingEOFBeforeTerminalReturnsPartialUsage(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	truncatedSSE := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":9}}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(truncatedSSE)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5", Stream: true}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.ErrorContains(t, err, "missing terminal event")
	require.NotNil(t, result, "usage observed before a truncated terminal event must remain billable")
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, truncatedSSE, rec.Body.String())
}

func TestGatewayService_AnthropicOAuthNativePassthrough_UpstreamErrorEventIsCopiedOnce(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	upstreamSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"upstream busy\"}}\n\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(upstreamSSE)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5", Stream: true}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.ErrorContains(t, err, "native upstream stream error")
	require.NotNil(t, result, "usage before an upstream error remains billable")
	require.Equal(t, upstreamSSE, rec.Body.String(), "native mode must copy the upstream error event without rewriting it")
	require.True(t, HasGatewaySSEErrorWritten(c))
}

func TestGatewayService_AnthropicOAuthNativePassthrough_ClientDisconnectStillDrainsUsage(t *testing.T) {
	c, _ := newNativePassthroughTestContext(t, "/v1/messages")
	c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
	original := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	upstreamSSE := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(upstreamSSE)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5", Stream: true}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Nil(t, result.FirstTokenMs, "a failed downstream write is not a client-visible first token")
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
}

func TestGatewayService_AnthropicOAuthNativePassthrough_PreservesSoftRateLimitFailoverSemantics(t *testing.T) {
	c, _ := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":false,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(`{"type":"error","error":{"type":"rate_limit_error","message":"try again"}}`)),
	}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5"}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.AnthropicSoftRateLimit)
	require.True(t, failoverErr.RetryableOnSameAccount)
}

func TestGatewayService_AnthropicOAuthNativePassthrough_403IsSentOnce(t *testing.T) {
	c, _ := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":false,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	responseBody := &nativePassthroughTrackedBody{Reader: strings.NewReader(`{"type":"error","error":{"type":"permission_error","message":"forbidden"}}`)}
	upstream := &nativePassthroughHTTPUpstream{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       responseBody,
		}, nil
	}}
	repo := &nativePassthroughAccountRepo{}
	svc := &GatewayService{
		cfg:              &config.Config{},
		httpUpstream:     upstream,
		rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
	}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), OriginalBody: original, Model: "claude-opus-5"}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusForbidden, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, 1, upstream.Calls(), "strict 403 must not be replayed on the same account")
	require.Equal(t, 1, repo.setErrorCalls, "single 403 must still use the gateway's existing account error policy")
	require.Contains(t, repo.lastError, "forbidden", "account policy must receive the captured upstream error body")
	require.True(t, responseBody.closed, "captured error bodies must release the tracked upstream connection before replacement")
}

func TestGatewayService_AnthropicOAuthNativePassthrough_PreSemanticTimeoutIsFailoverSafe(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	blockingBody := newCloseBlockingReadCloser()
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       blockingBody,
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:          defaultMaxLineSize,
		FirstSemanticTimeout: 1,
	}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5", Stream: true}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.PreSemanticTimeout)
	require.True(t, failoverErr.SuppressTempUnschedule)
	require.Empty(t, rec.Body.String(), "pre-semantic timeout must not commit downstream bytes before failover")
}

func TestGatewayService_AnthropicOAuthNativePassthrough_MetadataThenSemanticTimeoutDoesNotFailover(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	messageStart := []byte("event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5}}}\n\n")
	blockingBody := newPrefixThenCloseBlockingReadCloser(messageStart)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       blockingBody,
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, FirstSemanticTimeout: 1}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), OriginalBody: original, Model: "claude-opus-5", Stream: true}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.ErrorContains(t, err, "first semantic timeout after response start")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "client-visible metadata makes replay unsafe")
	require.NotNil(t, result, "message_start usage remains observable on timeout")
	require.Nil(t, result.FirstTokenMs, "message_start must not be reported as first token")
	require.Equal(t, string(messageStart), rec.Body.String())
}

func TestGatewayService_AnthropicOAuthNativePassthrough_PreConnectNetworkErrorIsFailoverSafe(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":false,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	upstream := &nativePassthroughHTTPUpstream{do: func(*http.Request) (*http.Response, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "api.anthropic.com"}
	}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), Model: "claude-opus-5"}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.False(t, failoverErr.FailoverProhibited)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.Equal(t, 1, upstream.Calls())
	require.Empty(t, rec.Body.String())
}

func TestGatewayService_AnthropicOAuthNativePassthrough_AmbiguousNetworkErrorProhibitsFailover(t *testing.T) {
	c, rec := newNativePassthroughTestContext(t, "/v1/messages")
	original := []byte(`{"model":"claude-opus-5","stream":false,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"native-user"}}`)
	upstream := &nativePassthroughHTTPUpstream{do: func(req *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(req.Context())
		require.NotNil(t, trace)
		require.NotNil(t, trace.WroteHeaders)
		trace.WroteHeaders()
		return nil, io.ErrUnexpectedEOF
	}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(original), OriginalBody: original, Model: "claude-opus-5"}
	result, err := svc.Forward(SetClaudeCodeClient(context.Background(), true), c, newAnthropicOAuthNativeAccountForTest(), parsed)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.FailoverProhibited)
	require.False(t, failoverErr.ShouldRetryNextAccount())
	require.Equal(t, NextAccountStop, failoverErr.NextAccountAction)
	require.Equal(t, 1, upstream.Calls())
	require.Empty(t, rec.Body.String())
}

func TestAnthropicOAuthNativePassthrough_OnlyClaudeCodeAndEnabledAccount(t *testing.T) {
	c, _ := newNativePassthroughTestContext(t, "/v1/messages")
	parsed := &ParsedRequest{Body: NewRequestBodyRef([]byte(`{"model":"claude-opus-5"}`)), Model: "claude-opus-5"}
	svc := &GatewayService{}
	account := newAnthropicOAuthNativeAccountForTest()
	claudeCodeCtx := SetClaudeCodeClient(context.Background(), true)
	require.True(t, svc.shouldUseAnthropicOAuthNativePassthrough(claudeCodeCtx, c, account, parsed))
	account.Extra[anthropicOAuthPassthroughExtra] = false
	require.False(t, svc.shouldUseAnthropicOAuthNativePassthrough(claudeCodeCtx, c, account, parsed))
	markAnthropicOAuthNativePassthroughFallback(c, account)
	mode, _ := c.Get("anthropic_passthrough_mode")
	fallback, _ := c.Get("anthropic_passthrough_fallback")
	require.Equal(t, "mimic", mode)
	require.Equal(t, "", fallback)
	account.Extra[anthropicOAuthPassthroughExtra] = true
	c.Request.Header.Set("User-Agent", "curl/8.0.0")
	require.False(t, svc.shouldUseAnthropicOAuthNativePassthrough(context.Background(), c, account, parsed))
	markAnthropicOAuthNativePassthroughFallback(c, account)
	fallback, _ = c.Get("anthropic_passthrough_fallback")
	require.Equal(t, "non_claude_code", fallback)
	markAnthropicOAuthNativePassthroughFallback(c, &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey})
	mode, _ = c.Get("anthropic_passthrough_mode")
	fallback, _ = c.Get("anthropic_passthrough_fallback")
	require.Empty(t, mode, "a failover to API Key must not retain an OAuth passthrough marker")
	require.Empty(t, fallback)
}
