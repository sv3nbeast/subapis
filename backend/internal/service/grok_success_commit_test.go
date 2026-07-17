package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type grokSuccessCommitRepo struct {
	AccountRepository
	calls         atomic.Int32
	updateStarted chan struct{}
	releaseUpdate chan struct{}
	startOnce     sync.Once
}

type grokSuccessCounter struct {
	resets atomic.Int32
}

func (c *grokSuccessCounter) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	return 0, nil
}

func (c *grokSuccessCounter) ResetOpenAI403Count(context.Context, int64) error {
	c.resets.Add(1)
	return nil
}

func (r *grokSuccessCommitRepo) UpdateExtra(ctx context.Context, _ int64, _ map[string]any) error {
	if r.updateStarted != nil {
		r.startOnce.Do(func() { close(r.updateStarted) })
	}
	if r.releaseUpdate != nil {
		select {
		case <-r.releaseUpdate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.calls.Add(1)
	return nil
}

type grokFirstWriteGate struct {
	gin.ResponseWriter
	firstWrite   chan struct{}
	releaseWrite chan struct{}
	once         sync.Once
}

func (w *grokFirstWriteGate) waitBeforeFirstWrite() {
	w.once.Do(func() {
		close(w.firstWrite)
		<-w.releaseWrite
	})
}

func (w *grokFirstWriteGate) Write(data []byte) (int, error) {
	w.waitBeforeFirstWrite()
	return w.ResponseWriter.Write(data)
}

func (w *grokFirstWriteGate) WriteString(data string) (int, error) {
	w.waitBeforeFirstWrite()
	return w.ResponseWriter.WriteString(data)
}

func newGrokSuccessTestAccount(id int64) *Account {
	return &Account{
		ID:          id,
		Name:        "grok-success-test",
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			"base_url":     xai.DefaultCLIBaseURL,
		},
	}
}

func newGrokSuccessTestService(repo AccountRepository, resp *http.Response) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		httpUpstream:      &httpUpstreamRecorder{resp: resp},
		grokTokenProvider: NewGrokTokenProvider(repo, nil),
		accountRepo:       repo,
	}
}

func attachGrokSuccessCounter(svc *OpenAIGatewayService) *grokSuccessCounter {
	counter := &grokSuccessCounter{}
	rateLimitService := NewRateLimitService(nil, nil, nil, nil, nil)
	rateLimitService.SetOpenAI403CounterCache(counter)
	svc.rateLimitService = rateLimitService
	return counter
}

func grokSuccessTestHeaders(contentType string) http.Header {
	return http.Header{
		"Content-Type":                   []string{contentType},
		"Xai-Request-Id":                 []string{"grok-success-request"},
		"X-Ratelimit-Limit-Requests":     []string{"10"},
		"X-Ratelimit-Remaining-Requests": []string{"9"},
		"X-Ratelimit-Limit-Tokens":       []string{"1000"},
		"X-Ratelimit-Remaining-Tokens":   []string{"990"},
	}
}

func TestGrokSuccessCommitDoesNotDelayFirstStreamingWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":"hi","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	firstWrite := make(chan struct{})
	releaseWrite := make(chan struct{})
	c.Writer = &grokFirstWriteGate{
		ResponseWriter: c.Writer,
		firstWrite:     firstWrite,
		releaseWrite:   releaseWrite,
	}
	repo := &grokSuccessCommitRepo{
		updateStarted: make(chan struct{}),
		releaseUpdate: make(chan struct{}),
	}
	upstreamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_success","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		"",
	}, "\n")
	svc := newGrokSuccessTestService(repo, &http.Response{
		StatusCode: http.StatusOK,
		Header:     grokSuccessTestHeaders("text/event-stream"),
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	})
	counter := attachGrokSuccessCounter(svc)
	account := newGrokSuccessTestAccount(801)

	type forwardOutcome struct {
		result *OpenAIForwardResult
		err    error
	}
	outcome := make(chan forwardOutcome, 1)
	go func() {
		result, err := svc.forwardGrokResponses(context.Background(), c, account, body, "grok", true, time.Now())
		outcome <- forwardOutcome{result: result, err: err}
	}()

	select {
	case <-firstWrite:
	case <-repo.updateStarted:
		t.Fatal("Grok success state update started before the first downstream write")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first downstream write")
	}
	select {
	case <-repo.updateStarted:
		t.Fatal("Grok success state update started while the first downstream write was blocked")
	default:
	}
	require.Zero(t, counter.resets.Load())

	close(releaseWrite)
	select {
	case <-repo.updateStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the post-stream success state update")
	}
	close(repo.releaseUpdate)

	select {
	case got := <-outcome:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Grok forwarding to finish")
	}
	require.Equal(t, int32(1), repo.calls.Load())
	require.Equal(t, int32(1), counter.resets.Load())
}

func TestGrokSuccessCommitRequiresCompleteUpstreamResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("responses stream missing terminal", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("text/event-stream"),
			Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(802)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","input":"hi","stream":true}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

		result, err := svc.forwardGrokResponses(context.Background(), c, account, body, "grok", true, time.Now())
		require.ErrorContains(t, err, "missing terminal event")
		require.Nil(t, result)
		require.Zero(t, repo.calls.Load())
		require.Zero(t, counter.resets.Load())
	})

	t.Run("raw chat stream missing done", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("text/event-stream"),
			Body:       io.NopCloser(strings.NewReader("data: {\"id\":\"chatcmpl_partial\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(803)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

		result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
		require.ErrorContains(t, err, "before [DONE]")
		require.Nil(t, result)
		require.Zero(t, repo.calls.Load())
		require.Zero(t, counter.resets.Load())
	})

	t.Run("responses chat stream missing terminal", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("text/event-stream"),
			Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(804)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		c.Set("api_key", &APIKey{ID: 1, Group: &Group{Platform: PlatformGrok, GrokChatUpstreamMode: GrokChatUpstreamModeResponses}})

		result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
		require.ErrorContains(t, err, "missing terminal event")
		require.NotNil(t, result)
		require.Zero(t, repo.calls.Load())
		require.Zero(t, counter.resets.Load())
	})

	t.Run("messages stream missing terminal", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("text/event-stream"),
			Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(805)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

		result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
		require.ErrorContains(t, err, "missing terminal event")
		require.NotNil(t, result)
		require.Zero(t, repo.calls.Load())
		require.Zero(t, counter.resets.Load())
	})

	t.Run("raw chat non-stream malformed json", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("application/json"),
			Body:       io.NopCloser(strings.NewReader(`{"id":"truncated"`)),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(806)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

		result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
		require.ErrorContains(t, err, "parse grok chat_completions response")
		require.Nil(t, result)
		require.Zero(t, repo.calls.Load())
		require.Zero(t, counter.resets.Load())
	})

	t.Run("media body read error", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("application/json"),
			Body:       &grokErrorReadCloser{err: io.ErrUnexpectedEOF},
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(807)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok-imagine-image","prompt":"draw"}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))

		result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointImagesGenerations, "", body, "application/json")
		require.Error(t, err)
		require.Nil(t, result)
		require.Zero(t, repo.calls.Load())
		require.Zero(t, counter.resets.Load())
	})
}

func TestGrokSuccessCommitCoversAllResponsePaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("responses non-stream", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("application/json"),
			Body: io.NopCloser(strings.NewReader(
				`{"id":"resp_json","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
			)),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(808)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","input":"hi","stream":false}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

		result, err := svc.forwardGrokResponses(context.Background(), c, account, body, "grok", false, time.Now())
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, int32(1), repo.calls.Load())
		require.Equal(t, int32(1), counter.resets.Load())
	})

	t.Run("raw chat non-stream", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("application/json"),
			Body: io.NopCloser(strings.NewReader(
				`{"id":"chatcmpl_json","object":"chat.completion","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			)),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(809)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

		result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, int32(1), repo.calls.Load())
		require.Equal(t, int32(1), counter.resets.Load())
	})

	t.Run("raw chat stream", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("text/event-stream"),
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","choices":[{"delta":{"content":"ok"}}]}`,
				"",
				"data: [DONE]",
				"",
			}, "\n"))),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(810)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

		result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, int32(1), repo.calls.Load())
		require.Equal(t, int32(1), counter.resets.Load())
	})

	t.Run("raw chat terminal survives trailing read error", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("text/event-stream"),
			Body: &grokDataThenErrorReadCloser{
				data: []byte("data: [DONE]\n\n"),
				err:  io.ErrUnexpectedEOF,
			},
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(814)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

		result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, int32(1), repo.calls.Load())
		require.Equal(t, int32(1), counter.resets.Load())
	})

	t.Run("responses chat stream", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("text/event-stream"),
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"type":"response.output_text.delta","delta":"ok"}`,
				"",
				`data: {"type":"response.completed","response":{"id":"resp_chat_stream","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
				"",
			}, "\n"))),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(811)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		c.Set("api_key", &APIKey{ID: 2, Group: &Group{Platform: PlatformGrok, GrokChatUpstreamMode: GrokChatUpstreamModeResponses}})

		result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, int32(1), repo.calls.Load())
		require.Equal(t, int32(1), counter.resets.Load())
	})

	t.Run("messages buffered stream", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("text/event-stream"),
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"type":"response.output_text.delta","delta":"ok"}`,
				"",
				`data: {"type":"response.completed","response":{"id":"resp_messages","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
				"",
			}, "\n"))),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(812)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok","max_tokens":32,"stream":false,"messages":[{"role":"user","content":"hi"}]}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

		result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, int32(1), repo.calls.Load())
		require.Equal(t, int32(1), counter.resets.Load())
	})

	t.Run("media", func(t *testing.T) {
		repo := &grokSuccessCommitRepo{}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     grokSuccessTestHeaders("application/json"),
			Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
		}
		svc := newGrokSuccessTestService(repo, resp)
		counter := attachGrokSuccessCounter(svc)
		account := newGrokSuccessTestAccount(813)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"grok-imagine-image","prompt":"draw"}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))

		result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointImagesGenerations, "", body, "application/json")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, int32(1), repo.calls.Load())
		require.Equal(t, int32(1), counter.resets.Load())
	})
}

type grokErrorReadCloser struct {
	err error
}

func (r *grokErrorReadCloser) Read(_ []byte) (int, error) {
	return 0, r.err
}

func (r *grokErrorReadCloser) Close() error {
	return nil
}

type grokDataThenErrorReadCloser struct {
	data []byte
	err  error
}

func (r *grokDataThenErrorReadCloser) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func (r *grokDataThenErrorReadCloser) Close() error {
	return nil
}
