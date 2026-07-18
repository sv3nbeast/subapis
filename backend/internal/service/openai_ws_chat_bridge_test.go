package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayService_ForwardAsChatCompletions_HTTPIngressUsesWS(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "streaming"}[stream], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
			requestCh := make(chan []byte, 1)
			wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				require.NoError(t, err)
				defer conn.Close()
				_, request, err := conn.ReadMessage()
				require.NoError(t, err)
				requestCh <- request

				events := []map[string]any{
					{
						"type":     "response.created",
						"response": map[string]any{"id": "resp_chat_ws", "model": "gpt-5.6-sol", "status": "in_progress"},
					},
					{
						"type":         "response.output_item.added",
						"output_index": 0,
						"item": map[string]any{
							"id": "msg_ws", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{},
						},
					},
					{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": "hello from ws"},
					{
						"type": "response.completed",
						"response": map[string]any{
							"id": "resp_chat_ws", "object": "response", "status": "completed", "model": "gpt-5.6-sol",
							"output": []any{map[string]any{
								"id": "msg_ws", "type": "message", "role": "assistant", "status": "completed",
								"content": []any{map[string]any{"type": "output_text", "text": "hello from ws"}},
							}},
							"usage": map[string]any{
								"input_tokens": 11, "output_tokens": 4,
								"input_tokens_details": map[string]any{"cached_tokens": 3},
							},
						},
					},
				}
				for _, event := range events {
					require.NoError(t, conn.WriteJSON(event))
				}
			}))
			defer wsServer.Close()

			cfg := openAIHTTPIngressWSTestConfig(OpenAIHTTPIngressModeResponsesChat)
			httpUpstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{
				cfg: cfg, httpUpstream: httpUpstream,
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
			}
			account := openAIHTTPIngressWSTestAccount(301, wsServer.URL)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			body := []byte(`{"model":"gpt-5.6-sol","stream":` + map[bool]string{false: "false", true: "true"}[stream] + `,"messages":[{"role":"user","content":"hello"}]}`)
			result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.OpenAIWSMode)
			require.Equal(t, 11, result.Usage.InputTokens)
			require.Equal(t, 4, result.Usage.OutputTokens)
			require.Equal(t, 3, result.Usage.CacheReadInputTokens)
			require.Nil(t, httpUpstream.lastReq)
			require.Equal(t, "response.create", gjson.GetBytes(<-requestCh, "type").String())
			metrics := svc.SnapshotOpenAIWSRetryMetrics()
			require.Equal(t, int64(1), metrics.HTTPIngressSelectedTotal)
			require.Equal(t, int64(1), metrics.HTTPIngressSuccessTotal)
			require.Zero(t, metrics.HTTPIngressPrewriteFallback)
			if stream {
				require.Contains(t, rec.Body.String(), "hello from ws")
				require.Contains(t, rec.Body.String(), "data: [DONE]")
			} else {
				require.Equal(t, "chat.completion", gjson.GetBytes(rec.Body.Bytes(), "object").String())
				require.Equal(t, "hello from ws", gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.content").String())
			}
		})
	}
}

func TestOpenAIGatewayService_ForwardAsChatCompletions_WSPrewriteFailureFallsBackHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUpgradeRequired)
	}))
	defer wsServer.Close()

	completed := `{"type":"response.completed","response":{"id":"resp_http_fallback","object":"response","status":"completed","model":"gpt-5.6-sol","output":[{"id":"msg_http","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello from http"}]}],"usage":{"input_tokens":2,"output_tokens":3}}}`
	httpUpstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: " + completed + "\n\n")),
	}}
	cfg := openAIHTTPIngressWSTestConfig(OpenAIHTTPIngressModeResponsesChat)
	cfg.Gateway.OpenAIWS.FallbackCooldownSeconds = 30
	svc := &OpenAIGatewayService{
		cfg: cfg, httpUpstream: httpUpstream,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
	}
	account := openAIHTTPIngressWSTestAccount(302, wsServer.URL)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.OpenAIWSMode)
	require.NotNil(t, httpUpstream.lastReq)
	require.True(t, svc.isOpenAIWSFallbackCooling(account.ID))
	require.Equal(t, "hello from http", gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.content").String())
	metrics := svc.SnapshotOpenAIWSRetryMetrics()
	require.Equal(t, int64(1), metrics.HTTPIngressSelectedTotal)
	require.Zero(t, metrics.HTTPIngressSuccessTotal)
	require.Equal(t, int64(1), metrics.HTTPIngressPrewriteFallback)
}

func TestOpenAIGatewayService_Forward_HTTPIngressUsesWS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	requestCh := make(chan []byte, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		_, request, err := conn.ReadMessage()
		require.NoError(t, err)
		requestCh <- request
		require.NoError(t, conn.WriteJSON(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_http_ingress_ws", "object": "response", "status": "completed", "model": "gpt-5.6-sol",
				"output": []any{map[string]any{
					"id": "msg_ws", "type": "message", "role": "assistant", "status": "completed",
					"content": []any{map[string]any{"type": "output_text", "text": "responses over ws"}},
				}},
				"usage": map[string]any{"input_tokens": 7, "output_tokens": 5, "input_tokens_details": map[string]any{"cached_tokens": 2}},
			},
		}))
	}))
	defer wsServer.Close()

	cfg := openAIHTTPIngressWSTestConfig(OpenAIHTTPIngressModeResponses)
	httpUpstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{
		cfg: cfg, httpUpstream: httpUpstream,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
	}
	account := openAIHTTPIngressWSTestAccount(303, wsServer.URL)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.6-sol","stream":false,"input":"hello"}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.OpenAIWSMode)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Nil(t, httpUpstream.lastReq)
	require.Equal(t, "response.create", gjson.GetBytes(<-requestCh, "type").String())
	require.Equal(t, "responses over ws", gjson.GetBytes(rec.Body.Bytes(), "output.0.content.0.text").String())
	metrics := svc.SnapshotOpenAIWSRetryMetrics()
	require.Equal(t, int64(1), metrics.HTTPIngressSelectedTotal)
	require.Equal(t, int64(1), metrics.HTTPIngressSuccessTotal)
	require.Zero(t, metrics.HTTPIngressPrewriteFallback)
}

func TestOpenAIGatewayService_Forward_HTTPIngressWSPrewriteFailureFallsBackHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUpgradeRequired)
	}))
	defer wsServer.Close()

	httpUpstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_http_fallback","usage":{"input_tokens":3,"output_tokens":4}}`)),
	}}
	cfg := openAIHTTPIngressWSTestConfig(OpenAIHTTPIngressModeResponses)
	cfg.Gateway.OpenAIWS.FallbackCooldownSeconds = 30
	svc := &OpenAIGatewayService{
		cfg: cfg, httpUpstream: httpUpstream,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
	}
	account := openAIHTTPIngressWSTestAccount(304, wsServer.URL)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.6-sol","stream":false,"input":"hello"}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.OpenAIWSMode)
	require.NotNil(t, httpUpstream.lastReq)
	require.True(t, svc.isOpenAIWSFallbackCooling(account.ID))
	decision, _ := c.Get("openai_ws_transport_decision")
	require.Equal(t, string(OpenAIUpstreamTransportHTTPSSE), decision)
	metrics := svc.SnapshotOpenAIWSRetryMetrics()
	require.Equal(t, int64(1), metrics.HTTPIngressSelectedTotal)
	require.Zero(t, metrics.HTTPIngressSuccessTotal)
	require.Equal(t, int64(1), metrics.HTTPIngressPrewriteFallback)
}

func TestOpenAIGatewayService_Forward_HTTPIngressWSPostwriteFailureDoesNotFallbackHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		require.NoError(t, err)
		require.NoError(t, conn.WriteJSON(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type": "invalid_request_error", "code": "invalid_request", "message": "postwrite failure",
			},
		}))
	}))
	defer wsServer.Close()

	cfg := openAIHTTPIngressWSTestConfig(OpenAIHTTPIngressModeResponses)
	httpUpstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{
		cfg: cfg, httpUpstream: httpUpstream,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
	}
	account := openAIHTTPIngressWSTestAccount(305, wsServer.URL)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.6-sol","stream":false,"input":"hello"}`))
	require.Error(t, err)
	require.Nil(t, result)
	require.Nil(t, httpUpstream.lastReq)
	metrics := svc.SnapshotOpenAIWSRetryMetrics()
	require.Equal(t, int64(1), metrics.HTTPIngressSelectedTotal)
	require.Zero(t, metrics.HTTPIngressPrewriteFallback)
}

func TestOpenAIGatewayService_Forward_HTTPIngressWSAccountModelUnsupportedTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		require.NoError(t, err)
		require.NoError(t, conn.WriteJSON(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "Requested model is not supported by this API key/group",
			},
		}))
	}))
	defer wsServer.Close()

	cfg := openAIHTTPIngressWSTestConfig(OpenAIHTTPIngressModeResponses)
	upstream := &httpUpstreamRecorder{}
	repo := &modelCapabilityAccountRepoStub{}
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{accountRepo: repo},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
	}
	account := openAIHTTPIngressWSTestAccount(306, wsServer.URL)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.6-sol","stream":false,"input":"hello"}`))

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
	require.False(t, c.Writer.Written())
	require.Nil(t, upstream.lastReq, "account capability error must not fall back to HTTP on the same account")
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.6-sol", repo.modelRateLimitCalls[0].model)
}

func TestOpenAIGatewayService_Forward_HTTPIngressWSAccountModelUnsupportedAfterOutputDoesNotFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		require.NoError(t, err)
		require.NoError(t, conn.WriteJSON(map[string]any{
			"type":          "response.output_text.delta",
			"output_index":  0,
			"content_index": 0,
			"delta":         "already visible",
		}))
		require.NoError(t, conn.WriteJSON(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "Requested model is not supported by this API key/group",
			},
		}))
	}))
	defer wsServer.Close()

	cfg := openAIHTTPIngressWSTestConfig(OpenAIHTTPIngressModeResponses)
	repo := &modelCapabilityAccountRepoStub{}
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		rateLimitService: &RateLimitService{accountRepo: repo},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
	}
	account := openAIHTTPIngressWSTestAccount(307, wsServer.URL)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.6-sol","stream":true,"input":"hello"}`))

	var failoverErr *UpstreamFailoverError
	require.Error(t, err)
	require.False(t, errors.As(err, &failoverErr))
	require.Nil(t, result)
	require.True(t, c.Writer.Written())
	require.Contains(t, recorder.Body.String(), "already visible")
	require.Empty(t, repo.modelRateLimitCalls)
}

func openAIHTTPIngressWSTestConfig(mode string) *config.Config {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	cfg.Gateway.OpenAIWS.HTTPIngressMode = mode
	cfg.Gateway.OpenAIWS.HTTPIngressRolloutPercent = 0
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 30
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 10
	cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = 3600
	return cfg
}

func openAIHTTPIngressWSTestAccount(id int64, baseURL string) *Account {
	return &Account{
		ID: id, Name: "openai-http-ingress-ws", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 2,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": baseURL},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool,
			"openai_http_ingress_ws_override":            OpenAIHTTPIngressWSOverrideOn,
			"openai_responses_supported":                 true,
		},
	}
}
