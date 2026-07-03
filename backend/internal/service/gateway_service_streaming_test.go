package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type upstreamContextTestKey string

type queuedAnthropicHTTPUpstreamRecorder struct {
	bodies [][]byte
	resps  []*http.Response
	errs   []error
}

func (u *queuedAnthropicHTTPUpstreamRecorder) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *queuedAnthropicHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	if req != nil && req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		u.bodies = append(u.bodies, body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(strings.NewReader(string(body)))
	}
	if len(u.errs) > 0 {
		err := u.errs[0]
		u.errs = u.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(u.resps) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	resp := u.resps[0]
	u.resps = u.resps[1:]
	return resp, nil
}

func newStreamingResponseTestGatewayService() *GatewayService {
	return &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				StreamDataIntervalTimeout: 0,
				MaxLineSize:               defaultMaxLineSize,
			},
		},
		rateLimitService: &RateLimitService{},
	}
}

func TestGatewayService_StreamingReusesScannerBufferAndStillParsesUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStreamingResponseTestGatewayService()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		// Minimal SSE event to trigger parseSSEUsage
		_, _ = pw.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n\n"))
		_, _ = pw.Write([]byte("data: [DONE]\n\n"))
	}()

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.usage)
	require.Equal(t, 3, result.usage.InputTokens)
	require.Equal(t, 7, result.usage.OutputTokens)
}

func TestGatewayService_StreamingKeepaliveUsesIdleTimer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStreamingResponseTestGatewayService()
	svc.cfg.Gateway.StreamKeepaliveInterval = 1

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n"))
		time.Sleep(1100 * time.Millisecond)
		_, _ = pw.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}()

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), "event: ping")
}

func TestGatewayService_StreamingKeepaliveUsesNoopDeltaForAffectedClaudeCodeVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStreamingResponseTestGatewayService()
	svc.cfg.Gateway.StreamKeepaliveInterval = 1

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.198 (external, cli)")

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n"))
		_, _ = pw.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		time.Sleep(1100 * time.Millisecond)
		_, _ = pw.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		_, _ = pw.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}()

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"delta":{"type":"text_delta","text":""}`)
}

func TestGatewayService_StreamingKeepaliveUsesNoopDeltaDuringToolUseForAffectedClaudeCodeVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStreamingResponseTestGatewayService()
	svc.cfg.Gateway.StreamKeepaliveInterval = 1

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.198 (external, cli)")

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n"))
		_, _ = pw.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"Edit\",\"input\":{}}}\n\n"))
		time.Sleep(1100 * time.Millisecond)
		_, _ = pw.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"))
		_, _ = pw.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}()

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"index":1`)
	require.Contains(t, body, `"delta":{"type":"input_json_delta","partial_json":""}`)
}

func TestGatewayService_StreamingKeepaliveKeepsPingForOlderClaudeCodeVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStreamingResponseTestGatewayService()
	svc.cfg.Gateway.StreamKeepaliveInterval = 1

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.187 (external, cli)")

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n"))
		_, _ = pw.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		time.Sleep(1100 * time.Millisecond)
		_, _ = pw.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		_, _ = pw.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}()

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.Contains(t, body, "event: ping")
	require.NotContains(t, body, `"delta":{"type":"text_delta","text":""}`)
}

func TestGatewayService_StreamingDropsInternalKiroPing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStreamingResponseTestGatewayService()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := strings.Join([]string{
		"event: sub2api_internal_kiro_ping",
		"data: {}",
		"",
		"event: message_start",
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"text":"ok"`)
	require.NotContains(t, rec.Body.String(), "sub2api_internal_kiro_ping")
}

func TestDetachUpstreamContextIgnoresClientCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), upstreamContextTestKey("test-key"), "test-value"))
	upstreamCtx, release := detachUpstreamContext(parent)
	defer release()

	cancel()

	require.NoError(t, upstreamCtx.Err())
	require.Equal(t, "test-value", upstreamCtx.Value(upstreamContextTestKey("test-key")))
}

func TestGatewayService_Forward_PreResponseNetworkErrorTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed := &ParsedRequest{
		Body:   NewRequestBodyRef(body),
		Model:  "claude-sonnet-4-6",
		Stream: true,
	}
	upstream := &anthropicHTTPUpstreamRecorder{err: io.ErrUnexpectedEOF}
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		httpUpstream:        upstream,
		rateLimitService:    &RateLimitService{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          42,
		Name:        "anthropic-oauth-test",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "oauth-token",
		},
		Status:      StatusActive,
		Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.Error(t, err)
	require.Nil(t, result)

	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "pre-response EOF should be returned as failover error")
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Contains(t, ExtractUpstreamErrorMessage(failoverErr.ResponseBody), "upstream request disconnected before response")
	require.Empty(t, rec.Body.String(), "service must not write a 502 body before handler failover can run")
}

func TestGatewayService_Forward_RewritesInlineSystemRoleToUser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-opus-4-8","stream":true,"messages":[{"role":"user","content":"hello"},{"role":"system","content":[{"type":"text","text":"mid instruction","cache_control":{"type":"ephemeral"}}]}]}`)
	parsed := &ParsedRequest{
		Body:   NewRequestBodyRef(body),
		Model:  "claude-opus-4-8",
		Stream: true,
	}
	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		"",
		`event: message_delta`,
		`data: {"type":"message_delta","usage":{"output_tokens":2}}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	upstream := &queuedAnthropicHTTPUpstreamRecorder{
		resps: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"req_ok"}},
				Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
			},
		},
	}
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		httpUpstream:        upstream,
		rateLimitService:    &RateLimitService{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          1,
		Name:        "anthropic-api-key",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-key"},
	}

	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	// inline role=system 就地改成 role=user 留原位，content + cache_control 原样保留；
	// 顶层 system 不被引入（原请求无 system）。
	require.Equal(t, "user", gjson.GetBytes(upstream.bodies[0], "messages.1.role").String())
	require.Equal(t, "mid instruction", gjson.GetBytes(upstream.bodies[0], "messages.1.content.0.text").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(upstream.bodies[0], "messages.1.content.0.cache_control.type").String())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "system").Exists())
	require.Contains(t, rec.Body.String(), `"type":"message_stop"`)
}

// TestGatewayService_Forward_RewritesStringContentInlineSystemRoleToUser 验证末尾带
// string-content role=system 的请求经 Forward 一次性转成 role=user 留原位、只发一次请求
// （生产已无 400-retry 流程：migration 前置规范化，上游永远收不到非法 role=system）。
func TestGatewayService_Forward_RewritesStringContentInlineSystemRoleToUser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := []byte(`{"model":"claude-opus-4-8","stream":true,"messages":[{"role":"user","content":"hello"},{"role":"system","content":"late instruction"}]}`)
	parsed := &ParsedRequest{
		Body:   NewRequestBodyRef(body),
		Model:  "claude-opus-4-8",
		Stream: true,
	}
	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	upstream := &queuedAnthropicHTTPUpstreamRecorder{
		resps: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"req_ok"}},
				Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
			},
		},
	}
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		httpUpstream:        upstream,
		rateLimitService:    &RateLimitService{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          1,
		Name:        "anthropic-api-key",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-key"},
	}

	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1, "single request, no 400 retry needed")
	// 末尾 string-content role=system 就地转 role=user，content 原样，无顶层 system。
	require.Equal(t, "user", gjson.GetBytes(upstream.bodies[0], "messages.1.role").String())
	require.Equal(t, "late instruction", gjson.GetBytes(upstream.bodies[0], "messages.1.content").String())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "system").Exists())
	require.Contains(t, rec.Body.String(), `"type":"message_stop"`)
}

func TestGatewayService_StreamingReadErrorAfterOutputMarksSSEErrorWritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamReadCloser{
			payload: []byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5}}}\n\n"),
			err:     io.ErrUnexpectedEOF,
		},
	}

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.Error(t, err)
	require.NotNil(t, result)
	require.True(t, HasGatewaySSEErrorWritten(c))
	require.Contains(t, rec.Body.String(), `"stream_read_error"`)
}

func TestGatewayService_StreamingFlushesRawInvokeBeforeTerminalWithoutBlockStop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<invoke name=\"Bash\"><parameter name=\"command\">pwd</parameter></invoke>"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.NotContains(t, body, "<invoke")
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

func TestGatewayService_StreamingDoesNotBridgeXMLInvokeForClaudeCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<invoke name=\"Read\"><parameter name=\"file_path\">README.md</parameter></invoke>"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	ctx := SetClaudeCodeClient(context.Background(), true)
	result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.Contains(t, body, `<invoke name=\"Read\">`)
	require.NotContains(t, body, `"type":"tool_use"`)
	require.NotContains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"stop_reason":"end_turn"`)
}

func TestGatewayService_StreamingDoesNotBridgeXMLInvokeForPlainClaudeExternalCLI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<invoke name=\"Bash\"><parameter name=\"command\">pwd</parameter><parameter name=\"description\">print cwd</parameter></invoke>"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	ctx := SetClaudeCodeClient(context.Background(), true)
	ctx = SetClaudeCodeUserAgent(ctx, "claude-cli/2.1.156 (external, cli)")
	result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.Contains(t, body, `<invoke name=\"Bash\">`)
	require.NotContains(t, body, `"type":"tool_use"`)
	require.NotContains(t, body, `"name":"Bash"`)
	require.NotContains(t, body, `"input_json_delta"`)
	require.Contains(t, body, `"stop_reason":"end_turn"`)
}

func TestGatewayService_StreamingStripsCallPreambleForClaudeExternalCLI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"call\n"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"<invoke name=\"Bash\"><parameter name=\"command\">pwd</parameter></invoke>"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	ctx := SetClaudeCodeClient(context.Background(), true)
	ctx = SetClaudeCodeUserAgent(ctx, "claude-cli/2.1.170 (external, claude-desktop-3p, agent-sdk/0.3.170)")
	result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.NotContains(t, body, "<invoke")
	require.NotContains(t, body, `"text":"call`)
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

func TestGatewayService_StreamingBridgesInvokeWithoutTextBlockStart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"call\n<invoke name=\"Bash\"><parameter name=\"command\">pwd</parameter></invoke>"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	ctx := SetClaudeCodeClient(context.Background(), true)
	ctx = SetClaudeCodeUserAgent(ctx, "claude-cli/2.1.170 (external, claude-desktop-3p, agent-sdk/0.3.170)")
	result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.NotContains(t, body, "<invoke")
	require.NotContains(t, body, `"text":"call`)
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

func TestGatewayService_StreamingBridgesInvokeInBlockStartTextForClaudeDesktopAgentCLI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"call\n<invoke name=\"Bash\"><parameter name=\"command\">pwd</parameter></invoke>"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	ctx := SetClaudeCodeClient(context.Background(), true)
	ctx = SetClaudeCodeUserAgent(ctx, "claude-cli/2.1.170 (external, claude-desktop-3p, agent-sdk/0.3.170)")
	result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.NotContains(t, body, "<invoke")
	require.NotContains(t, body, `"text":"call`)
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

func TestGatewayService_StreamingBridgesEscapedInvokeInBlockStartTextForClaudeDesktopAgentCLI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"call\n&lt;invoke name=&quot;Bash&quot;&gt;&lt;parameter name=&quot;command&quot;&gt;pwd&lt;/parameter&gt;&lt;parameter name=&quot;description&quot;&gt;print cwd&lt;/parameter&gt;&lt;/invoke&gt;"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	ctx := SetClaudeCodeClient(context.Background(), true)
	ctx = SetClaudeCodeUserAgent(ctx, "claude-cli/2.1.170 (external, claude-desktop-3p, agent-sdk/0.3.170)")
	result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.NotContains(t, body, "&lt;invoke")
	require.NotContains(t, body, `"text":"call`)
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\",\"description\":\"print cwd\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

func TestGatewayService_StreamingStripsIndentedCallPreambleFromRealEscapedInvoke(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	invokeText := `  call
  &lt;invoke name="Bash"&gt;
  &lt;parameter name="command"&gt;cd /Users/sven.sun/Desktop/Tools/Strategy/AutoGetCode
python3 - &lt;&lt;'PYEOF'
f = "chatgpt_login.py"
src = open(f, encoding="utf-8").read()
method = '''    def login_with_phone(
        self,
        sms_fetch_code,
    ) -&gt; str:
        """已注册到一半的手机号账号 用手机号+密码登录续接 抓 session。"""
'''
python3 -c "import ast;
ast.parse(open('/Users/sven.sun/Desktop/Tools/Strategy/AutoGetCode/chatgpt_login.py').read()); print('syntax
OK')"&lt;/parameter&gt;
  &lt;parameter name="description"&gt;Add login_with_phone method&lt;/parameter&gt;
  &lt;/invoke&gt;`
	invokeJSON, err := json.Marshal(invokeText)
	require.NoError(t, err)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":` + string(invokeJSON) + `}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	ctx := SetClaudeCodeClient(context.Background(), true)
	ctx = SetClaudeCodeUserAgent(ctx, "claude-cli/2.1.170 (external, claude-desktop-3p, agent-sdk/0.3.170)")
	result, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)

	require.NoError(t, err)
	require.NotNil(t, result)
	body := rec.Body.String()
	require.NotContains(t, body, "&lt;invoke")
	require.NotContains(t, body, `"text":"  call`)
	require.NotContains(t, body, `"text":"call`)
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	var partialJSON string
	for _, block := range strings.Split(body, "\n\n") {
		payload := strings.TrimPrefix(strings.TrimSpace(block), "event: content_block_delta\ndata: ")
		if payload == strings.TrimSpace(block) {
			continue
		}
		if gjson.Get(payload, "delta.type").String() == "input_json_delta" {
			partialJSON = gjson.Get(payload, "delta.partial_json").String()
			break
		}
	}
	require.NotEmpty(t, partialJSON)
	require.Contains(t, gjson.Get(partialJSON, "command").String(), "python3 - <<'PYEOF'")
	require.Contains(t, gjson.Get(partialJSON, "command").String(), ") -> str:")
	require.Equal(t, "Add login_with_phone method", gjson.Get(partialJSON, "description").String())
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

func TestGatewayService_Forward_LooseClaudeCLIHeadersDoNotSkipMimicry(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.156 (external, cli)")

	metadataUserID := `{"device_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","account_uuid":"550e8400-e29b-41d4-a716-446655440000","session_id":"123e4567-e89b-12d3-a456-426614174000"}`
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"metadata":{"user_id":` + strconv.Quote(metadataUserID) + `},"system":"custom tool instructions","messages":[{"role":"user","content":"hello"}],"tools":[{"name":"bash","description":"run shell","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}]}`)
	parsed := &ParsedRequest{
		Body:           NewRequestBodyRef(body),
		Model:          "claude-sonnet-4-6",
		Stream:         true,
		MetadataUserID: metadataUserID,
	}
	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
		},
	}
	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		httpUpstream:        upstream,
		rateLimitService:    &RateLimitService{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          43,
		Name:        "anthropic-oauth-test",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "oauth-token",
		},
		Status:      StatusActive,
		Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, gjson.GetBytes(upstream.lastBody, "system.0.text").String(), "x-anthropic-billing-header")
	require.Equal(t, "Bash", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
	require.Contains(t, buildClaudeMimicDebugLine(upstream.lastReq, upstream.lastBody, account, "oauth", true), "tools_count=1")
}

func TestIsRetryablePreResponseNetworkError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unexpected_eof", io.ErrUnexpectedEOF, true},
		{"read_connect_unexpected_eof", errors.New("read CONNECT response: unexpected EOF"), true},
		{"connection_reset", syscall.ECONNRESET, true},
		{"context_canceled", context.Canceled, false},
		{"proxy_407_not_retryable_network", errors.New("proxy CONNECT failed: 407 Unauthorized"), false},
		{"wrapped_proxy_407_not_retryable_network", errors.New("read CONNECT response: proxy CONNECT failed: 407 Unauthorized"), false},
		{"ordinary_error", errors.New("invalid request"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isRetryablePreResponseNetworkError(tc.err))
		})
	}
}
