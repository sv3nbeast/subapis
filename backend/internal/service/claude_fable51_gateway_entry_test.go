package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeFable51_AnthropicGatewayEntriesNormalizeRequestAndTerminate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		path           string
		body           string
		stream         bool
		forward        func(*GatewayService, *gin.Context, *Account, []byte, bool) (*ForwardResult, error)
		terminalMarker string
	}{
		{
			name:   "messages stream",
			path:   "/v1/messages",
			stream: true,
			body:   `{"model":"claude-fable-5-1","stream":true,"max_tokens":128000,"temperature":0.2,"top_p":0.9,"top_k":40,"thinking":{"type":"enabled","budget_tokens":24576},"messages":[{"role":"user","content":"hello"}]}`,
			forward: func(svc *GatewayService, c *gin.Context, account *Account, body []byte, stream bool) (*ForwardResult, error) {
				parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-fable-5-1", Stream: stream}
				return svc.Forward(context.Background(), c, account, parsed)
			},
			terminalMarker: `"type":"message_stop"`,
		},
		{
			name:   "messages buffered",
			path:   "/v1/messages",
			stream: false,
			body:   `{"model":"claude-fable-5-1","stream":false,"max_tokens":128000,"temperature":0.2,"top_p":0.9,"top_k":40,"thinking":{"type":"enabled","budget_tokens":24576},"messages":[{"role":"user","content":"hello"}]}`,
			forward: func(svc *GatewayService, c *gin.Context, account *Account, body []byte, stream bool) (*ForwardResult, error) {
				parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-fable-5-1", Stream: stream}
				return svc.Forward(context.Background(), c, account, parsed)
			},
			terminalMarker: `"stop_reason":"end_turn"`,
		},
		{
			name:   "chat completions stream",
			path:   "/v1/chat/completions",
			stream: true,
			body:   `{"model":"claude-fable-5-1","stream":true,"max_completion_tokens":128000,"temperature":0.2,"top_p":0.9,"reasoning_effort":"high","messages":[{"role":"user","content":"hello"}]}`,
			forward: func(svc *GatewayService, c *gin.Context, account *Account, body []byte, _ bool) (*ForwardResult, error) {
				return svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
			},
			terminalMarker: "data: [DONE]",
		},
		{
			name:   "chat completions buffered",
			path:   "/v1/chat/completions",
			stream: false,
			body:   `{"model":"claude-fable-5-1","stream":false,"max_completion_tokens":128000,"temperature":0.2,"top_p":0.9,"reasoning_effort":"high","messages":[{"role":"user","content":"hello"}]}`,
			forward: func(svc *GatewayService, c *gin.Context, account *Account, body []byte, _ bool) (*ForwardResult, error) {
				return svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
			},
			terminalMarker: `"finish_reason":"stop"`,
		},
		{
			name:   "responses stream",
			path:   "/v1/responses",
			stream: true,
			body:   `{"model":"claude-fable-5-1","stream":true,"max_output_tokens":128000,"temperature":0.2,"top_p":0.9,"reasoning":{"effort":"high"},"input":"hello"}`,
			forward: func(svc *GatewayService, c *gin.Context, account *Account, body []byte, _ bool) (*ForwardResult, error) {
				return svc.ForwardAsResponses(context.Background(), c, account, body, nil)
			},
			terminalMarker: `"type":"response.completed"`,
		},
		{
			name:   "responses buffered",
			path:   "/v1/responses",
			stream: false,
			body:   `{"model":"claude-fable-5-1","stream":false,"max_output_tokens":128000,"temperature":0.2,"top_p":0.9,"reasoning":{"effort":"high"},"input":"hello"}`,
			forward: func(svc *GatewayService, c *gin.Context, account *Account, body []byte, _ bool) (*ForwardResult, error) {
				return svc.ForwardAsResponses(context.Background(), c, account, body, nil)
			},
			terminalMarker: `"object":"response"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader([]byte(tt.body)))
			c.Request.Header.Set("Content-Type", "application/json")

			upstreamBody := anthropicMinimalSSEResponse
			contentType := "text/event-stream"
			if tt.name == "messages buffered" {
				contentType = "application/json"
				upstreamBody = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-fable-5-1","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":7}}`
			}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{contentType},
					"X-Request-Id": []string{"rid-fable51-entry"},
				},
				Body: io.NopCloser(strings.NewReader(upstreamBody)),
			}}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
			svc := &GatewayService{
				cfg:                  cfg,
				httpUpstream:         upstream,
				responseHeaderFilter: compileResponseHeaderFilter(cfg),
				rateLimitService:     &RateLimitService{},
				deferredService:      &DeferredService{},
				tlsFPProfileService:  &TLSFingerprintProfileService{},
			}
			account := newAnthropicAPIKeyAccountForTest()

			result, err := tt.forward(svc, c, account, []byte(tt.body), tt.stream)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.stream, result.Stream)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "claude-fable-5-1", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, int64(32000), gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
			require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "top_p").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "top_k").Exists())
			require.NotContains(t, string(upstream.lastBody), `"budget_tokens"`)
			require.Equal(t, 1, strings.Count(recorder.Body.String(), tt.terminalMarker), recorder.Body.String())
		})
	}
}
