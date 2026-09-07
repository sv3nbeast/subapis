package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type nativeCompactTestConn struct {
	*stagedPassthroughConn
	reads       atomic.Int32
	writesCount atomic.Int32
	inRead      atomic.Int32
	writeErr    error
}

func (c *nativeCompactTestConn) ReadMessage(ctx context.Context) ([]byte, error) {
	c.reads.Add(1)
	c.inRead.Add(1)
	defer c.inRead.Add(-1)
	return c.stagedPassthroughConn.ReadMessage(ctx)
}
func (c *nativeCompactTestConn) WriteJSON(ctx context.Context, v any) error {
	c.writesCount.Add(1)
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := c.stagedPassthroughConn.WriteFrame(ctx, 1, b); err != nil {
		return err
	}
	return c.writeErr
}

type nativeCompactTestWriter struct {
	gin.ResponseWriter
	chunks     chan string
	failWrites atomic.Bool
}

func (w *nativeCompactTestWriter) Write(b []byte) (int, error) {
	if w.failWrites.Load() {
		return 0, io.ErrClosedPipe
	}
	n, err := w.ResponseWriter.Write(b)
	if n > 0 {
		w.chunks <- string(b[:n])
	}
	return n, err
}

type nativeCompactTestResult struct {
	result *OpenAIForwardResult
	err    error
}
type nativeCompactFixture struct {
	c      *gin.Context
	rec    *httptest.ResponseRecorder
	w      *nativeCompactTestWriter
	up     *nativeCompactTestConn
	done   chan nativeCompactTestResult
	cancel context.CancelFunc
}

func newNativeCompactFixture(t *testing.T, model string, native bool, heartbeat, readTimeout int, writeErr ...error) *nativeCompactFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.StreamKeepaliveInterval = heartbeat
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.HTTPIngressMode = OpenAIHTTPIngressModeResponses
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	cfg.Gateway.OpenAIWS.HTTPIngressRolloutPercent = 100
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = readTimeout
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	up := &nativeCompactTestConn{stagedPassthroughConn: newStagedPassthroughConn()}
	if len(writeErr) > 0 {
		up.writeErr = writeErr[0]
	}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&stagedPassthroughDialer{conn: up})
	t.Cleanup(pool.Close)
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), openaiWSPool: pool}
	a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	input := []any{map[string]any{"type": "message", "role": "user", "content": "synthetic prefix"}}
	if native {
		input = append(input, map[string]any{"type": "compaction_trigger"})
	}
	body, _ := json.Marshal(map[string]any{"model": model, "stream": true, "store": false, "prompt_cache_key": "stable-prefix", "reasoning": map[string]any{"effort": "max"}, "input": input})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body))).WithContext(ctx)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	if native {
		MarkOpenAINativeCompactionV2(c)
	}
	w := &nativeCompactTestWriter{ResponseWriter: c.Writer, chunks: make(chan string, 64)}
	c.Writer = w
	f := &nativeCompactFixture{c: c, rec: rec, w: w, up: up, done: make(chan nativeCompactTestResult, 1), cancel: cancel}
	go func() { result, err := svc.Forward(ctx, c, a, body); f.done <- nativeCompactTestResult{result, err} }()
	select {
	case sent := <-up.writes:
		require.Equal(t, model, gjson.GetBytes(sent, "model").String())
		require.NotEmpty(t, gjson.GetBytes(sent, "prompt_cache_key").String(), "existing account fingerprint policy may canonicalize the key")
		require.Equal(t, "max", gjson.GetBytes(sent, "reasoning.effort").String())
		if native {
			require.Equal(t, "compaction_trigger", gjson.GetBytes(sent, "input.1.type").String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request was not sent")
	}
	return f
}
func (f *nativeCompactFixture) chunk(t *testing.T, contains string) {
	t.Helper()
	deadline := time.After(4 * time.Second)
	for {
		select {
		case b := <-f.w.chunks:
			if strings.Contains(b, contains) {
				return
			}
		case <-deadline:
			t.Fatalf("missing client chunk %q", contains)
		}
	}
}
func (f *nativeCompactFixture) finish(t *testing.T) nativeCompactTestResult {
	t.Helper()
	select {
	case r := <-f.done:
		require.Zero(t, f.up.inRead.Load(), "reader must be joined before return")
		return r
	case <-time.After(5 * time.Second):
		f.cancel()
		t.Fatal("forward did not stop")
		return nativeCompactTestResult{}
	}
}

const nativeCompactCreated = `{"type":"response.created","response":{"id":"resp_compact","status":"in_progress"}}`
const nativeCompactItemDone = `{"type":"response.output_item.done","output_index":0,"item":{"type":"compaction","id":"cmp_1","encrypted_content":"opaque"}}`
const nativeCompactCompleted = `{"type":"response.completed","response":{"id":"resp_compact","status":"completed","usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":80,"cache_write_tokens":10}}}}`

func TestNativeCompactionWSProgressAndIdleKeepalive(t *testing.T) {
	f := newNativeCompactFixture(t, "gpt-6-astra", true, 1, 10)
	f.chunk(t, ": keepalive") // no upstream frames yet
	f.up.Send(nativeCompactCreated)
	f.chunk(t, "response.created")
	f.up.Send(`{"type":"response.in_progress","response":{"id":"resp_compact"}}`)
	f.chunk(t, "response.in_progress")
	f.chunk(t, ": keepalive") // heartbeat remains active AFTER a progress write
	f.up.Send(nativeCompactItemDone)
	f.chunk(t, "cmp_1")       // item must not wait for terminal
	f.chunk(t, ": keepalive") // nor stop after the item
	f.up.Send(nativeCompactCompleted)
	r := f.finish(t)
	require.NoError(t, r.err)
	require.NotNil(t, r.result)
	require.Nil(t, r.result.FirstTokenMs, "transport progress must not invent a text-token timestamp")
	require.Equal(t, 80, r.result.Usage.CacheReadInputTokens)
	require.Equal(t, 1, strings.Count(f.rec.Body.String(), `"type":"response.completed"`))
	require.Equal(t, int32(4), f.up.reads.Load(), "no read-ahead after terminal")
	require.Equal(t, int32(1), f.up.writesCount.Load())
	require.False(t, openAICompactClientWantsStream(f.c), "must not activate legacy unary-to-SSE bridge")
	select {
	case <-f.up.closed:
		t.Fatal("healthy connection was closed after terminal")
	default:
	}
}

func TestNativeCompactionWSAllModelsAndStableCache(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-5.6-sol", "future-coding-model"} {
		t.Run(model, func(t *testing.T) {
			f := newNativeCompactFixture(t, model, true, 0, 5)
			f.up.Send(nativeCompactCreated)
			f.chunk(t, "response.created")
			f.up.Send(nativeCompactItemDone)
			f.chunk(t, "cmp_1")
			f.up.Send(nativeCompactCompleted)
			r := f.finish(t)
			require.NoError(t, r.err)
			require.Nil(t, r.result.FirstTokenMs)
			require.Equal(t, 5, r.result.Usage.OutputTokens)
			require.Equal(t, 80, r.result.Usage.CacheReadInputTokens)
			require.Equal(t, 10, r.result.Usage.CacheCreationInputTokens)
		})
	}
}

func TestNativeCompactionWSFailureDoesNotReplay(t *testing.T) {
	for _, phase := range []string{"zero_frame_eof", "after_progress_eof", "malformed", "bare_error", "rate_limit", "missing_item", "partial_item", "read_timeout", "client_cancel"} {
		t.Run(phase, func(t *testing.T) {
			readTimeout := 5
			if phase == "read_timeout" {
				readTimeout = 1
			}
			f := newNativeCompactFixture(t, "gpt-6-astra", true, 1, readTimeout)
			if phase != "zero_frame_eof" {
				f.up.Send(nativeCompactCreated)
				f.chunk(t, "response.created")
			}
			switch phase {
			case "zero_frame_eof", "after_progress_eof":
				f.up.Fail(io.EOF)
			case "malformed":
				f.up.Send(`{"type":`)
			case "bare_error":
				f.up.Send(`{"type":"error","error":{"code":"server_error","message":"synthetic failure"}}`)
			case "rate_limit":
				f.up.Send(`{"type":"error","error":{"code":"rate_limit_exceeded","type":"rate_limit_error","message":"synthetic limit"}}`)
			case "missing_item":
				f.up.Send(nativeCompactCompleted)
			case "partial_item":
				f.up.Send(`{"type":"response.output_item.done","item":{"type":"compaction","id":"cmp_partial"}}`)
			case "client_cancel":
				f.cancel()
			}
			r := f.finish(t)
			require.Error(t, r.err)
			if phase == "missing_item" {
				require.NotNil(t, r.result, "retain usage actually reported before semantic validation failed")
				require.Equal(t, 5, r.result.Usage.OutputTokens)
			} else {
				require.Nil(t, r.result)
			}
			require.Equal(t, int32(1), f.up.writesCount.Load(), "never replay an ambiguous sent compaction")
			require.NotContains(t, f.rec.Body.String(), `"type":"response.completed"`)
			if phase == "client_cancel" {
				_, marked := GetOpsStreamError(f.c)
				require.True(t, marked, "committed 200 must not be logged as compact success after cancellation")
				require.True(t, errors.Is(r.err, context.Canceled))
				require.NotContains(t, f.rec.Body.String(), "response.failed")
			} else {
				var finalized *OpenAIStreamAlreadyFinalizedError
				require.ErrorAs(t, r.err, &finalized)
				require.Equal(t, 1, strings.Count(f.rec.Body.String(), `"type":"response.failed"`))
				require.True(t, HasGatewaySSEErrorWritten(f.c))
				if phase == "rate_limit" {
					mark, ok := GetOpsStreamError(f.c)
					require.True(t, ok)
					require.Equal(t, 429, mark.IntendedStatus)
				}
				if phase != "zero_frame_eof" {
					require.Contains(t, f.rec.Body.String(), `"id":"resp_compact"`)
				}
			}
		})
	}
}

func TestNativeCompactionWSTerminalVariants(t *testing.T) {
	for _, typ := range []string{"response.failed", "response.incomplete", "terminal_only_item", "added_only_item"} {
		t.Run(typ, func(t *testing.T) {
			f := newNativeCompactFixture(t, "gpt-6-astra", true, 0, 5)
			f.up.Send(nativeCompactCreated)
			f.chunk(t, "response.created")
			if typ == "terminal_only_item" {
				f.up.Send(`{"type":"response.completed","response":{"id":"resp_compact","status":"completed","output":[{"type":"compaction","id":"cmp_2","encrypted_content":"opaque"}],"usage":{"input_tokens":100,"output_tokens":5}}}`)
			} else if typ == "added_only_item" {
				f.up.Send(`{"type":"response.output_item.added","output_index":0,"item":{"type":"compaction","id":"cmp_2","encrypted_content":"opaque"}}`)
				f.up.Send(nativeCompactCompleted)
			} else {
				b, _ := json.Marshal(map[string]any{"type": typ, "response": map[string]any{"id": "resp_compact", "status": strings.TrimPrefix(typ, "response."), "usage": map[string]int{"input_tokens": 20, "output_tokens": 3}, "error": map[string]string{"message": "synthetic"}}})
				f.up.Send(string(b))
			}
			r := f.finish(t)
			if typ == "terminal_only_item" || typ == "added_only_item" {
				require.NoError(t, r.err)
				require.Equal(t, 1, strings.Count(f.rec.Body.String(), `"type":"response.output_item.done"`))
				require.Contains(t, f.rec.Body.String(), "cmp_2")
			} else {
				var finalized *OpenAIStreamAlreadyFinalizedError
				require.ErrorAs(t, r.err, &finalized)
				require.NotNil(t, r.result)
				require.Equal(t, 3, r.result.Usage.OutputTokens)
				_, ok := GetOpsStreamError(f.c)
				require.True(t, ok)
				require.Equal(t, 1, strings.Count(f.rec.Body.String(), `"type":"`+typ+`"`))
			}
		})
	}
}

func TestNativeCompactionClientWriteFailureStopsReader(t *testing.T) {
	f := newNativeCompactFixture(t, "gpt-6-astra", true, 1, 5)
	f.up.Send(nativeCompactCreated)
	f.chunk(t, "response.created")
	f.w.failWrites.Store(true)
	r := f.finish(t)
	require.Error(t, r.err)
	require.Nil(t, r.result)
	require.Equal(t, int32(1), f.up.writesCount.Load())
}

func TestNativeCompactionAmbiguousSubmissionNeverReplays(t *testing.T) {
	f := newNativeCompactFixture(t, "gpt-6-astra", true, 1, 5, io.ErrUnexpectedEOF)
	r := f.finish(t)
	var finalized *OpenAIStreamAlreadyFinalizedError
	require.ErrorAs(t, r.err, &finalized)
	require.Nil(t, r.result)
	require.Equal(t, int32(1), f.up.writesCount.Load())
	require.Equal(t, 1, strings.Count(f.rec.Body.String(), `"type":"response.failed"`))
}

func TestNativeCompactionFailedProgressWriteIsNotSuccess(t *testing.T) {
	f := newNativeCompactFixture(t, "gpt-6-astra", true, 1, 5)
	f.up.Send(nativeCompactCreated)
	f.chunk(t, "response.created")
	f.w.failWrites.Store(true)
	f.up.Send(nativeCompactItemDone)
	f.up.Send(nativeCompactCompleted)
	r := f.finish(t)
	require.NoError(t, r.err, "upstream completed; preserve reported usage despite dead client")
	require.Equal(t, 5, r.result.Usage.OutputTokens)
	mark, ok := GetOpsStreamError(f.c)
	require.True(t, ok)
	require.Equal(t, 499, mark.IntendedStatus)
	require.NotContains(t, f.rec.Body.String(), "response.completed")
}

func TestNativeCompactionDoesNotChangeOrdinaryBuffering(t *testing.T) {
	f := newNativeCompactFixture(t, "gpt-6-astra", false, 0, 5)
	f.up.Send(nativeCompactCreated)
	require.Eventually(t, func() bool { return f.up.reads.Load() >= 2 }, time.Second, time.Millisecond)
	select {
	case b := <-f.w.chunks:
		t.Fatalf("ordinary pre-token event unexpectedly released: %s", b)
	default:
	}
	f.up.Send(`{"type":"response.output_text.delta","delta":"OK"}`)
	f.chunk(t, "response.output_text.delta")
	f.up.Send(nativeCompactCompleted)
	r := f.finish(t)
	require.NoError(t, r.err)
	require.NotNil(t, r.result.FirstTokenMs)
}
