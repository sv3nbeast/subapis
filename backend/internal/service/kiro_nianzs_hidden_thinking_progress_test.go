package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type nianzsProgressResponseRecorder struct {
	*httptest.ResponseRecorder
	mu               sync.Mutex
	messageStart     chan struct{}
	publicPing       chan struct{}
	messageStartOnce sync.Once
	publicPingOnce   sync.Once
}

func newNianzsProgressResponseRecorder() *nianzsProgressResponseRecorder {
	return &nianzsProgressResponseRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		messageStart:     make(chan struct{}),
		publicPing:       make(chan struct{}),
	}
}

func (r *nianzsProgressResponseRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if bytes.Contains(p, []byte("event: message_start")) {
		r.messageStartOnce.Do(func() { close(r.messageStart) })
	}
	if bytes.Contains(p, []byte("event: ping\n")) {
		r.publicPingOnce.Do(func() { close(r.publicPing) })
	}
	return r.ResponseRecorder.Write(p)
}

func (r *nianzsProgressResponseRecorder) WriteString(s string) (int, error) {
	return r.Write([]byte(s))
}

func (r *nianzsProgressResponseRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ResponseRecorder.WriteHeader(code)
}

func (r *nianzsProgressResponseRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ResponseRecorder.Flush()
}

func (r *nianzsProgressResponseRecorder) bodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Body.String()
}

type nianzsCancelableTestBody struct {
	ctx            context.Context
	initial        *bytes.Reader
	closed         chan struct{}
	cancelObserved chan struct{}
	closeOnce      sync.Once
	cancelOnce     sync.Once
}

func newNianzsCancelableTestBody(ctx context.Context, initial []byte) *nianzsCancelableTestBody {
	return &nianzsCancelableTestBody{
		ctx:            ctx,
		initial:        bytes.NewReader(initial),
		closed:         make(chan struct{}),
		cancelObserved: make(chan struct{}),
	}
}

func (b *nianzsCancelableTestBody) Read(p []byte) (int, error) {
	if b.initial != nil && b.initial.Len() > 0 {
		return b.initial.Read(p)
	}
	select {
	case <-b.ctx.Done():
		b.cancelOnce.Do(func() { close(b.cancelObserved) })
		return 0, b.ctx.Err()
	case <-b.closed:
		return 0, io.EOF
	}
}

func (b *nianzsCancelableTestBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

type nianzsCancelableTestUpstream struct {
	initial      []byte
	requestCount int
	body         *nianzsCancelableTestBody
	bodyReady    chan *nianzsCancelableTestBody
}

type nianzsHeaderBlockingTestUpstream struct {
	requestStarted chan struct{}
	cancelObserved chan struct{}
	startOnce      sync.Once
	cancelOnce     sync.Once
}

func (u *nianzsHeaderBlockingTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.startOnce.Do(func() { close(u.requestStarted) })
	<-req.Context().Done()
	u.cancelOnce.Do(func() { close(u.cancelObserved) })
	return nil, req.Context().Err()
}

func (u *nianzsHeaderBlockingTestUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *nianzsCancelableTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.requestCount++
	u.body = newNianzsCancelableTestBody(req.Context(), u.initial)
	if u.bodyReady != nil {
		u.bodyReady <- u.body
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       u.body,
	}, nil
}

func (u *nianzsCancelableTestUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func nianzsHiddenThinkingMessagesRequest(t *testing.T) ([]byte, *ParsedRequest, *int64) {
	t.Helper()
	body := []byte(`{
		"model":"claude-opus-5",
		"stream":true,
		"max_tokens":64000,
		"thinking":{"type":"adaptive"},
		"output_config":{"effort":"max"},
		"tools":[{"name":"Read","description":"read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],
		"messages":[{"role":"user","content":"analyze the issue"}]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	groupID := int64(29)
	parsed.GroupID = &groupID
	return body, parsed, &groupID
}

func nianzsHiddenThinkingTestContext(timeout, progressInterval time.Duration) context.Context {
	ctx := withNianzsKiroHiddenThinkingProgress(context.Background(), progressInterval)
	return WithKiroAnthropicFallbackPolicy(ctx, KiroAnthropicFallbackPolicy{
		Enabled:              true,
		FirstSemanticTimeout: timeout,
		MaxAnthropicAttempts: 2,
	})
}

func TestNianzsMessagesLongAdaptiveThinkingKeepsClaudeStreamAliveWithoutReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, parsed, _ := nianzsHiddenThinkingMessagesRequest(t)
	upstreamReader, upstreamWriter := io.Pipe()
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       upstreamReader,
	}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, response)
	svc.cfg.Gateway.KiroStreamKeepaliveInterval = 1

	recorder := newNianzsProgressResponseRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219")

	reasoningFrame := kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "provider-only hidden reasoning"},
	})
	signatureFrame := kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"signature": nianzsXMLInvokeProviderThinkingSignature()},
	})
	answerFrame := kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "final visible answer"},
	})
	stopFrame := kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	})
	writeErr := make(chan error, 1)
	allowFinal := make(chan struct{})
	go func() {
		defer func() { _ = upstreamWriter.Close() }()
		for i := 0; i < 30; i++ {
			if _, err := upstreamWriter.Write(reasoningFrame); err != nil {
				writeErr <- err
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		<-allowFinal
		for _, frame := range [][]byte{signatureFrame, answerFrame, stopFrame} {
			if _, err := upstreamWriter.Write(frame); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- nil
	}()

	type forwardOutcome struct {
		result *ForwardResult
		err    error
	}
	forwardDone := make(chan forwardOutcome, 1)
	started := time.Now()
	go func() {
		result, err := svc.Forward(nianzsHiddenThinkingTestContext(120*time.Millisecond, 20*time.Millisecond), c, account, parsed)
		forwardDone <- forwardOutcome{result: result, err: err}
	}()

	select {
	case <-recorder.messageStart:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Claude message_start was not released when hidden generation began")
	}
	select {
	case <-recorder.publicPing:
		close(allowFinal)
	case <-time.After(2 * time.Second):
		close(allowFinal)
		t.Fatal("Claude stream did not receive a protocol ping during long hidden reasoning")
	}

	outcome := <-forwardDone
	require.NoError(t, outcome.err)
	require.NotNil(t, outcome.result)
	require.NotNil(t, outcome.result.FirstTokenMs)
	require.GreaterOrEqual(t, *outcome.result.FirstTokenMs, 1000, "TTFT must remain tied to visible content, not message_start or ping")
	require.NoError(t, <-writeErr)
	require.Len(t, upstream.requests, 1, "generation progress must prohibit account/body replay")
	require.Less(t, time.Since(started), 3*time.Second)

	wire := recorder.bodyString()
	require.Equal(t, 1, strings.Count(wire, "event: message_start"))
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
	require.Contains(t, wire, "event: ping")
	var visible strings.Builder
	for _, delta := range nianzsSSEPayloadsByType(wire, "content_block_delta") {
		if delta.Get("delta.type").String() == "text_delta" {
			visible.WriteString(delta.Get("delta.text").String())
		}
	}
	require.Equal(t, "final visible answer", visible.String())
	require.NotContains(t, wire, "sub2api_internal_kiro_hidden_thinking_progress")
	require.NotContains(t, wire, "provider-only hidden reasoning")
}

func TestNianzsMessagesSilentFirstSemanticTimeoutCancelsUpstreamAndAllowsFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, parsed, _ := nianzsHiddenThinkingMessagesRequest(t)
	upstream := &nianzsCancelableTestUpstream{}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.httpUpstream = upstream

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219")

	result, forwardErr := svc.Forward(nianzsHiddenThinkingTestContext(60*time.Millisecond, 10*time.Millisecond), c, account, parsed)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, forwardErr, &failoverErr)
	require.True(t, failoverErr.PreSemanticTimeout)
	require.False(t, failoverErr.FailoverProhibited)
	require.NotNil(t, failoverErr.UpstreamDone)
	require.Empty(t, recorder.Body.String())
	require.Equal(t, 1, upstream.requestCount)
	select {
	case <-upstream.body.cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("silent upstream request was not canceled")
	}
	select {
	case <-failoverErr.UpstreamDone:
	case <-time.After(time.Second):
		t.Fatal("physical upstream cleanup did not complete")
	}
}

func TestNianzsMessagesClientCancellationStopsPhysicalUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, parsed, _ := nianzsHiddenThinkingMessagesRequest(t)
	upstream := &nianzsCancelableTestUpstream{bodyReady: make(chan *nianzsCancelableTestBody, 1)}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.httpUpstream = upstream

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219")

	baseCtx, cancel := context.WithCancel(context.Background())
	ctx := withNianzsKiroHiddenThinkingProgress(baseCtx, 10*time.Millisecond)
	ctx = WithKiroAnthropicFallbackPolicy(ctx, KiroAnthropicFallbackPolicy{
		Enabled:              true,
		FirstSemanticTimeout: time.Second,
		MaxAnthropicAttempts: 2,
	})
	type forwardOutcome struct {
		result *ForwardResult
		err    error
	}
	done := make(chan forwardOutcome, 1)
	go func() {
		result, err := svc.Forward(ctx, c, account, parsed)
		done <- forwardOutcome{result: result, err: err}
	}()

	physicalBody := <-upstream.bodyReady
	cancel()
	select {
	case outcome := <-done:
		require.Nil(t, outcome.result)
		require.ErrorIs(t, outcome.err, context.Canceled)
		upstreamDone := UpstreamDoneFromError(outcome.err)
		require.NotNil(t, upstreamDone)
		select {
		case <-upstreamDone:
		case <-time.After(time.Second):
			t.Fatal("client cancellation did not join physical upstream cleanup")
		}
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not return promptly")
	}
	select {
	case <-physicalBody.ctx.Done():
		require.ErrorIs(t, physicalBody.ctx.Err(), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not cancel the AWS request context")
	}
	require.Empty(t, recorder.Body.String())
	require.Equal(t, 1, upstream.requestCount)
}

func TestNianzsMessagesClientCancellationBeforeHeadersStopsPhysicalUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, parsed, _ := nianzsHiddenThinkingMessagesRequest(t)
	upstream := &nianzsHeaderBlockingTestUpstream{
		requestStarted: make(chan struct{}),
		cancelObserved: make(chan struct{}),
	}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.httpUpstream = upstream

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219")

	baseCtx, cancel := context.WithCancel(context.Background())
	ctx := withNianzsKiroHiddenThinkingProgress(baseCtx, 10*time.Millisecond)
	ctx = WithKiroAnthropicFallbackPolicy(ctx, KiroAnthropicFallbackPolicy{
		Enabled:              true,
		FirstSemanticTimeout: time.Second,
		MaxAnthropicAttempts: 2,
	})
	type forwardOutcome struct {
		result *ForwardResult
		err    error
	}
	done := make(chan forwardOutcome, 1)
	go func() {
		result, err := svc.Forward(ctx, c, account, parsed)
		done <- forwardOutcome{result: result, err: err}
	}()

	select {
	case <-upstream.requestStarted:
	case <-time.After(time.Second):
		t.Fatal("AWS request did not start")
	}
	cancel()
	select {
	case <-upstream.cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("client cancellation before response headers did not cancel AWS")
	}
	select {
	case outcome := <-done:
		require.Nil(t, outcome.result)
		require.ErrorIs(t, outcome.err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("forward did not return after pre-header client cancellation")
	}
	require.Empty(t, recorder.Body.String())
}

func TestNianzsMessagesHiddenGenerationStallReturnsInBandErrorWithoutReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, parsed, _ := nianzsHiddenThinkingMessagesRequest(t)
	reasoningFrame := kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "provider-only hidden reasoning"},
	})
	upstream := &nianzsCancelableTestUpstream{initial: reasoningFrame}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.httpUpstream = upstream

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219")

	result, forwardErr := svc.Forward(nianzsHiddenThinkingTestContext(70*time.Millisecond, 10*time.Millisecond), c, account, parsed)
	require.Nil(t, result)
	require.Error(t, forwardErr)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(forwardErr, &failoverErr), "generation that already started must not enter account failover")
	require.True(t, HasGatewaySSEErrorWritten(c))
	require.Equal(t, 1, upstream.requestCount)

	wire := recorder.Body.String()
	require.Contains(t, wire, "event: message_start")
	require.Contains(t, wire, "event: error")
	require.Contains(t, wire, "Kiro upstream generation stalled")
	require.NotContains(t, wire, "sub2api_internal_kiro_hidden_thinking_progress")
	require.NotContains(t, wire, "provider-only hidden reasoning")
	select {
	case <-upstream.body.cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("stalled generated request was not canceled")
	}
}

func TestNianzsMessagesInvalidThinkingSignatureFailsInBandWithoutReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, parsed, _ := nianzsHiddenThinkingMessagesRequest(t)
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "provider-only hidden reasoning"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"signature": "invalid-provider-signature"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "must not be accepted"},
	}))
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219")

	result, forwardErr := svc.Forward(nianzsHiddenThinkingTestContext(500*time.Millisecond, 10*time.Millisecond), c, account, parsed)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(forwardErr, &failoverErr), "an authenticated-thinking failure after generation begins must not enter account failover")
	require.ErrorContains(t, forwardErr, "invalid provider-native Kiro thinking signature")
	require.Len(t, upstream.requests, 1)
	require.True(t, HasGatewaySSEErrorWritten(c))

	wire := recorder.Body.String()
	require.Contains(t, wire, "event: message_start")
	require.Contains(t, wire, "event: error")
	require.NotContains(t, wire, "sub2api_internal_kiro_hidden_thinking_progress")
	require.NotContains(t, wire, "provider-only hidden reasoning")
	require.NotContains(t, wire, "must not be accepted")
}

func TestNianzsMessagesOpus46AdaptiveControlStillCompletesNormally(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, _, groupID := nianzsHiddenThinkingMessagesRequest(t)
	body = bytes.Replace(body, []byte("claude-opus-5"), []byte("claude-opus-4-6"), 1)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	parsed.GroupID = groupID

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "short provider-only reasoning"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"signature": nianzsXMLInvokeProviderThinkingSignature()},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "opus 4.6 answer"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219")
	result, forwardErr := svc.Forward(nianzsHiddenThinkingTestContext(time.Second, 10*time.Millisecond), c, account, parsed)

	require.NoError(t, forwardErr)
	require.NotNil(t, result)
	require.NotNil(t, result.FirstTokenMs)
	require.Len(t, upstream.requests, 1)
	wire := recorder.Body.String()
	var visible strings.Builder
	for _, delta := range nianzsSSEPayloadsByType(wire, "content_block_delta") {
		if delta.Get("delta.type").String() == "text_delta" {
			visible.WriteString(delta.Get("delta.text").String())
		}
	}
	require.Equal(t, "opus 4.6 answer", visible.String())
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
	require.NotContains(t, wire, "sub2api_internal_kiro_hidden_thinking_progress")
	require.NotContains(t, wire, "short provider-only reasoning")
}

func TestFinishNianzsKiroStreamResponseCannotBeReclassifiedForFailover(t *testing.T) {
	done := make(chan struct{})
	close(done)
	resp := &http.Response{Body: &kiroTrackedStreamBody{
		ReadCloser: io.NopCloser(strings.NewReader("")),
		done:       done,
	}}
	svc := &GatewayService{}
	original := &UpstreamFailoverError{
		StatusCode:  http.StatusBadGateway,
		FailureKind: UpstreamFailureIncompleteStream,
		Cause:       errors.New("empty kiro event stream after generation started"),
	}

	err := svc.finishNianzsKiroStreamResponse(context.Background(), resp, nil, original)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Nil(t, svc.kiroStreamErrorToFailover(context.Background(), &Account{ID: 1}, err))
}
