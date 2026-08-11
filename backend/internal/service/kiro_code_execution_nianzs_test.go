package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type stubNianzsKiroCodeExecutionRunner struct {
	result nianzskiro.CodeExecutionResult
	err    error
	codes  []string
}

func (r *stubNianzsKiroCodeExecutionRunner) Execute(_ context.Context, code string) (nianzskiro.CodeExecutionResult, error) {
	r.codes = append(r.codes, code)
	return r.result, r.err
}

func nianzsCodeExecutionRequestBody(stream bool) []byte {
	return []byte(`{
      "model":"claude-opus-5",
      "max_tokens":64000,
      "stream":` + map[bool]string{true: "true", false: "false"}[stream] + `,
      "system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."}],
      "messages":[{"role":"user","content":[{"type":"text","text":"Write and execute a Python script that prints 'HELLO_CHECK'. Only use the code execution tool, nothing else."}]}],
      "tools":[{"type":"code_execution_20250522","name":"code_execution"}]
    }`)
}

func TestNianzsMessagesLegacyCodeExecutionStreamingClosesServerToolLoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := nianzsCodeExecutionRequestBody(true)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	runner := &stubNianzsKiroCodeExecutionRunner{result: nianzskiro.CodeExecutionResult{
		Stdout: "HELLO_CHECK\n", ReturnCode: 0,
	}}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.kiroCodeExecutionRunner = runner
	upstream.responses = []*http.Response{
		kiroCustomToolEventStreamResponse(t, "toolu_code_test", "code_execution", `{"code":"print('HELLO_CHECK')"}`),
		kiroEventStreamResponse(t, "HELLO_CHECK", 14, 3),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 25, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, []string{"print('HELLO_CHECK')"}, runner.codes)
	require.Len(t, upstream.requests, 2)
	require.Contains(t, string(upstream.bodies[1]), "HELLO_CHECK")

	wire := recorder.Body.String()
	require.Equal(t, 1, strings.Count(wire, "event: message_start"))
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
	require.Contains(t, wire, `"type":"server_tool_use"`)
	require.Contains(t, wire, `"type":"code_execution_tool_result"`)
	require.Contains(t, wire, `"type":"code_execution_result"`)
	require.Contains(t, wire, `HELLO_CHECK`)
	require.NotContains(t, wire, `"type":"tool_use"`)

	starts := nianzsSSEPayloadsByType(wire, "content_block_start")
	blockTypes := make([]string, 0, len(starts))
	for _, start := range starts {
		blockTypes = append(blockTypes, start.Get("content_block.type").String())
	}
	require.Contains(t, blockTypes, "server_tool_use")
	require.Contains(t, blockTypes, "code_execution_tool_result")
	deltas := nianzsSSEPayloadsByType(wire, "message_delta")
	require.Len(t, deltas, 1)
	require.Equal(t, "end_turn", deltas[0].Get("delta.stop_reason").String())
	require.Equal(t, int64(25), deltas[0].Get("usage.input_tokens").Int())
	require.Equal(t, int64(8), deltas[0].Get("usage.output_tokens").Int())
	require.Equal(t, int64(1), deltas[0].Get("usage.server_tool_use.code_execution_requests").Int())
}

func TestNianzsMessagesLegacyCodeExecutionNonStreamingClosesServerToolLoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := nianzsCodeExecutionRequestBody(false)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	runner := &stubNianzsKiroCodeExecutionRunner{result: nianzskiro.CodeExecutionResult{
		Stdout: "HELLO_CHECK\n", ReturnCode: 0,
	}}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.kiroCodeExecutionRunner = runner
	upstream.responses = []*http.Response{
		kiroCustomToolEventStreamResponse(t, "toolu_code_test", "code_execution", `{"code":"print('HELLO_CHECK')"}`),
		kiroEventStreamResponse(t, "HELLO_CHECK", 14, 3),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, 25, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, []string{"print('HELLO_CHECK')"}, runner.codes)

	response := recorder.Body.String()
	require.Equal(t, "server_tool_use", gjson.Get(response, "content.0.type").String())
	require.Equal(t, "code_execution", gjson.Get(response, "content.0.name").String())
	require.Equal(t, "code_execution_tool_result", gjson.Get(response, "content.1.type").String())
	require.Equal(t, "HELLO_CHECK\n", gjson.Get(response, "content.1.content.stdout").String())
	require.Equal(t, "HELLO_CHECK", gjson.Get(response, "content.2.text").String())
	require.Equal(t, "end_turn", gjson.Get(response, "stop_reason").String())
	require.Equal(t, int64(25), gjson.Get(response, "usage.input_tokens").Int())
	require.Equal(t, int64(8), gjson.Get(response, "usage.output_tokens").Int())
	require.Equal(t, int64(1), gjson.Get(response, "usage.server_tool_use.code_execution_requests").Int())
}

func TestNianzsMessagesLegacyCodeExecutionStreamsPreludeBeforeToolTurnCompletes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := nianzsCodeExecutionRequestBody(true)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	upstreamBody, upstreamWriter := io.Pipe()
	firstResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       upstreamBody,
	}
	runner := &stubNianzsKiroCodeExecutionRunner{result: nianzskiro.CodeExecutionResult{Stdout: "HELLO_CHECK\n"}}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.kiroCodeExecutionRunner = runner
	upstream.responses = []*http.Response{firstResponse, kiroEventStreamResponse(t, "HELLO_CHECK", 14, 3)}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	semanticReached := make(chan struct{})
	releaseSemantic := make(chan struct{})
	c.Writer = &nianzsSemanticWriteGate{
		ResponseWriter: c.Writer,
		marker:         []byte("PRELUDE_VISIBLE"),
		reached:        semanticReached,
		release:        releaseSemantic,
	}
	allowToolTurn := make(chan struct{})
	go func() {
		prelude := "PRELUDE_VISIBLE " + strings.Repeat("stream this before the tool completes; ", 20)
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": prelude},
		}))
		<-allowToolTurn
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "toolUseEvent", map[string]any{
			"toolUseEvent": map[string]any{
				"toolUseId": "toolu_incremental", "name": "code_execution",
				"input": `{"code":"print('HELLO_CHECK')"}`, "stop": true,
			},
		}))
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
			"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 11, "outputTokens": 5}},
		}))
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
			"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
		}))
		_ = upstreamWriter.Close()
	}()

	type forwardOutcome struct {
		result *ForwardResult
		err    error
	}
	outcome := make(chan forwardOutcome, 1)
	go func() {
		result, err := svc.Forward(context.Background(), c, account, parsed)
		outcome <- forwardOutcome{result: result, err: err}
	}()

	select {
	case <-semanticReached:
		// The upstream tool event is still gated, so reaching this write proves
		// the response is not buffered until the first Kiro turn terminates.
	case <-time.After(3 * time.Second):
		t.Fatal("prelude was not streamed before the upstream tool turn completed")
	}
	close(releaseSemantic)
	close(allowToolTurn)

	select {
	case got := <-outcome:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
	case <-time.After(5 * time.Second):
		t.Fatal("code execution stream did not complete")
	}
	require.Contains(t, recorder.Body.String(), "PRELUDE_VISIBLE")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
}

func TestNianzsMessagesLegacyCodeExecutionFirstHTTPErrorStaysPreOutput(t *testing.T) {
	body := nianzsCodeExecutionRequestBody(true)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	runner := &stubNianzsKiroCodeExecutionRunner{}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.kiroCodeExecutionRunner = runner
	upstream.responses = []*http.Response{{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited"}`)),
	}}

	resp, _, err := svc.openKiroAnthropicStreamResponseNianzs(
		context.Background(), account, parsed, body, "claude-opus-5", "claude-opus-5", nil, nil, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	require.Empty(t, runner.codes)
	require.Len(t, upstream.requests, 1)
	responseBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.JSONEq(t, `{"message":"rate limited"}`, string(responseBody))
}

func TestNianzsMessagesLegacyCodeExecutionFirstBodyErrorStaysPreOutput(t *testing.T) {
	body := nianzsCodeExecutionRequestBody(true)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	runner := &stubNianzsKiroCodeExecutionRunner{}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.kiroCodeExecutionRunner = runner
	cacheKey := uint64(time.Now().UnixNano())
	plan := nianzsCodeExecutionCachePlanForTest(cacheKey)
	defer nianzsDeleteCodeExecutionCachePlanForTest(cacheKey)
	upstream.responses = []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       passthroughErrReadCloser{err: io.ErrUnexpectedEOF},
	}}

	resp, _, err := svc.openKiroAnthropicStreamResponseNianzs(
		context.Background(), account, parsed, body, "claude-opus-5", "claude-opus-5", nil, nil, plan,
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer func() { _ = resp.Body.Close() }()
	responseBody, readErr := io.ReadAll(resp.Body)
	require.Error(t, readErr)
	require.Empty(t, responseBody)
	require.Empty(t, runner.codes)
	require.Len(t, upstream.requests, 1)
	require.False(t, nianzsCodeExecutionCachePlanCommittedForTest(cacheKey))
}

func TestNianzsMessagesLegacyCodeExecutionCommitsCacheOnlyAfterCompleteFirstTurn(t *testing.T) {
	body := nianzsCodeExecutionRequestBody(true)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	runner := &stubNianzsKiroCodeExecutionRunner{}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.kiroCodeExecutionRunner = runner
	upstream.responses = []*http.Response{kiroEventStreamResponse(t, "completed without a tool", 9, 3)}
	cacheKey := uint64(time.Now().UnixNano())
	plan := nianzsCodeExecutionCachePlanForTest(cacheKey)
	defer nianzsDeleteCodeExecutionCachePlanForTest(cacheKey)

	resp, _, err := svc.openKiroAnthropicStreamResponseNianzs(
		context.Background(), account, parsed, body, "claude-opus-5", "claude-opus-5", nil, nil, plan,
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer func() { _ = resp.Body.Close() }()
	_, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.True(t, nianzsCodeExecutionCachePlanCommittedForTest(cacheKey))
}

func TestNianzsMessagesLegacyCodeExecutionTruncatedTurnNeverEmitsSuccessTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := nianzsCodeExecutionRequestBody(true)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	truncated := bytes.NewBuffer(nil)
	_, _ = truncated.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "PARTIAL_ONLY"},
	}))
	firstResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(truncated.Bytes())),
	}
	runner := &stubNianzsKiroCodeExecutionRunner{}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.kiroCodeExecutionRunner = runner
	upstream.responses = []*http.Response{firstResponse}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.Error(t, err)
	require.Nil(t, result)
	require.Empty(t, runner.codes)
	require.Len(t, upstream.requests, 1)
	wire := recorder.Body.String()
	require.Contains(t, wire, "event: content_block_delta")
	require.Contains(t, wire, `"type":"stream_read_error"`)
	require.Equal(t, 0, strings.Count(wire, "event: message_stop"))
}

func nianzsCodeExecutionCachePlanForTest(cacheKey uint64) *nianzsKiroCacheEmulationPlan {
	return &nianzsKiroCacheEmulationPlan{
		usage:    &nianzsKiroCacheEmulationUsage{InputTokens: 8, CacheCreationInputTokens: 8, CacheCreation5mInputTokens: 8},
		cacheKey: cacheKey,
		profile: &nianzsKiroCacheProfile{
			totalInputTokens: 8,
			blocks: []nianzsKiroCacheBlock{{
				prefixFingerprint: [32]byte{1, 2, 3},
				cumulativeTokens:  8,
			}},
			breakpoints: []nianzsKiroCacheBreakpoint{{blockIndex: 0, ttl: time.Minute}},
		},
	}
}

func nianzsCodeExecutionCachePlanCommittedForTest(cacheKey uint64) bool {
	nianzsGlobalKiroCacheTracker.mu.Lock()
	defer nianzsGlobalKiroCacheTracker.mu.Unlock()
	return len(nianzsGlobalKiroCacheTracker.entries[cacheKey]) > 0
}

func nianzsDeleteCodeExecutionCachePlanForTest(cacheKey uint64) {
	nianzsGlobalKiroCacheTracker.mu.Lock()
	delete(nianzsGlobalKiroCacheTracker.entries, cacheKey)
	nianzsGlobalKiroCacheTracker.mu.Unlock()
}
