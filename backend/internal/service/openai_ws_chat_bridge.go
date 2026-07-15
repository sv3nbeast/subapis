package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type openAIWSPipeResponseWriter struct {
	header http.Header
	pipe   *io.PipeWriter

	mu     sync.Mutex
	status int
	size   int
	closed chan bool
}

func newOpenAIWSPipeResponseWriter(pipe *io.PipeWriter) *openAIWSPipeResponseWriter {
	return &openAIWSPipeResponseWriter{
		header: make(http.Header),
		pipe:   pipe,
		status: http.StatusOK,
		size:   -1,
		closed: make(chan bool),
	}
}

func (w *openAIWSPipeResponseWriter) Header() http.Header { return w.header }

func (w *openAIWSPipeResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size >= 0 || code <= 0 {
		return
	}
	w.status = code
}

func (w *openAIWSPipeResponseWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size < 0 {
		w.size = 0
	}
}

func (w *openAIWSPipeResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	n, err := w.pipe.Write(data)
	w.mu.Lock()
	w.size += n
	w.mu.Unlock()
	return n, err
}

func (w *openAIWSPipeResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *openAIWSPipeResponseWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *openAIWSPipeResponseWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

func (w *openAIWSPipeResponseWriter) Written() bool { return w.Size() >= 0 }
func (w *openAIWSPipeResponseWriter) Flush()        { w.WriteHeaderNow() }
func (w *openAIWSPipeResponseWriter) CloseNotify() <-chan bool {
	return w.closed
}
func (w *openAIWSPipeResponseWriter) Pusher() http.Pusher { return nil }
func (w *openAIWSPipeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("websocket chat bridge does not support hijacking")
}

type openAIWSChatBridgeOutcome struct {
	result *OpenAIForwardResult
	err    error
}

func (s *OpenAIGatewayService) forwardChatCompletionsViaResponsesWS(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	responsesBody []byte,
	token string,
	decision OpenAIWSProtocolDecision,
	clientStream bool,
	originalModel string,
	billingModel string,
	upstreamModel string,
	requestBodyLen int,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	var reqBody map[string]any
	if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
		return nil, fmt.Errorf("decode chat responses body for ws: %w", err)
	}

	pipeReader, pipeWriter := io.Pipe()
	bridgeWriter := newOpenAIWSPipeResponseWriter(pipeWriter)
	bridgeContext := c.Copy()
	bridgeContext.Writer = bridgeWriter
	outcomeCh := make(chan openAIWSChatBridgeOutcome, 1)
	correctToolCalls := shouldCorrectCodexToolCallsForClient(c, responsesBody, true)

	go func() {
		result, err := s.forwardOpenAIWSV2(
			ctx,
			bridgeContext,
			account,
			reqBody,
			token,
			decision,
			false,
			correctToolCalls,
			true,
			originalModel,
			upstreamModel,
			startTime,
			1,
			"",
		)
		if err != nil {
			_ = pipeWriter.CloseWithError(err)
		} else {
			_ = pipeWriter.Close()
		}
		outcomeCh <- openAIWSChatBridgeOutcome{result: result, err: err}
	}()

	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		// The WS executor owns bridgeWriter.Header in another goroutine. Chat
		// conversion only needs the event stream, so keep its synthetic response
		// headers isolated and take the final upstream headers from outcome.result.
		Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:   pipeReader,
	}
	var (
		chatResult *OpenAIForwardResult
		chatErr    error
	)
	if clientStream {
		chatResult, chatErr = s.handleChatStreamingResponse(
			upstreamResponse, c, account, originalModel, billingModel, upstreamModel, startTime, requestBodyLen,
		)
	} else {
		chatResult, chatErr = s.handleChatBufferedStreamingResponse(
			upstreamResponse, c, account, originalModel, billingModel, upstreamModel, startTime,
		)
	}
	_ = pipeReader.Close()
	outcome := <-outcomeCh
	if outcome.err != nil {
		return nil, outcome.err
	}
	if chatErr != nil {
		return chatResult, chatErr
	}
	if outcome.result == nil {
		return nil, errors.New("openai ws chat bridge result is nil")
	}
	if chatResult == nil {
		chatResult = &OpenAIForwardResult{}
	}
	chatResult.RequestID = outcome.result.RequestID
	chatResult.ResponseID = outcome.result.ResponseID
	chatResult.Usage = outcome.result.Usage
	chatResult.Model = originalModel
	chatResult.BillingModel = billingModel
	chatResult.UpstreamModel = upstreamModel
	chatResult.ServiceTier = outcome.result.ServiceTier
	chatResult.ReasoningEffort = outcome.result.ReasoningEffort
	chatResult.Stream = clientStream
	chatResult.OpenAIWSMode = true
	chatResult.ResponseHeaders = outcome.result.ResponseHeaders
	chatResult.Duration = outcome.result.Duration
	chatResult.FirstTokenMs = outcome.result.FirstTokenMs
	return chatResult, nil
}
