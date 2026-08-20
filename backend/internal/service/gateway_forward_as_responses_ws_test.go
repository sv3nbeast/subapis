package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestIsOpenAIResponsesGenerateFalse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{name: "explicit_false", payload: `{"generate":false}`, want: true},
		{name: "missing_business_default", payload: `{}`},
		{name: "explicit_true", payload: `{"generate":true}`},
		{name: "string_false_is_not_contract", payload: `{"generate":"false"}`},
		{name: "null_is_not_contract", payload: `{"generate":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isOpenAIResponsesGenerateFalse([]byte(tt.payload)))
		})
	}
}

func TestForwardAsResponsesWebSocketTurnRelaysKiroEventsWithoutBuffering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	payload := []byte(`{
		"type":"response.create",
		"generate":true,
		"model":"gpt-5.6-sol",
		"input":"hello",
		"stream":false
	}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(payload), "responses")
	require.NoError(t, err)
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "bridge response complete")

	events := make([][]byte, 0, 8)
	result, err := svc.ForwardAsResponsesWebSocketTurn(
		context.Background(), c, account, payload, parsed, false,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ResponseID)
	require.True(t, result.Stream, "WS bridge must force the internal Responses path to stream")
	require.NotEmpty(t, events)
	terminalCount := 0
	textDeltaSeen := false
	for _, event := range events {
		eventType := gjson.GetBytes(event, "type").String()
		if isOpenAIWSTerminalEvent(eventType) {
			terminalCount++
		}
		if eventType == "response.output_text.delta" && strings.Contains(gjson.GetBytes(event, "delta").String(), "bridge response complete") {
			textDeltaSeen = true
		}
	}
	require.True(t, textDeltaSeen)
	require.Equal(t, 1, terminalCount)
	assertKiroNativeGPTUpstreamRequest(t, upstream)
	require.False(t, gjson.GetBytes(upstream.lastBody, "type").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "generate").Exists())
}

func TestForwardAsResponsesWebSocketTurnCompletesGenerateFalsePrewarmLocally(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	longDescription := strings.Repeat("exec prewarm contract ", 3600)
	payload := []byte(`{
		"type":"response.create",
		"generate":false,
		"model":"gpt-5.6-sol",
		"store":false,
		"input":[
			{"type":"additional_tools","role":"developer","tools":[{"type":"custom","name":"exec","description":` + strconv.Quote(longDescription) + `}]},
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"prewarm system context"}]}
		],
		"stream":true
	}`)
	require.Greater(t, len(payload), 70*1024, "fixture must retain the production-sized prewarm shape")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(payload), "responses")
	require.NoError(t, err)
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "must never be generated")

	events := make([][]byte, 0, 2)
	result, err := svc.ForwardAsResponsesWebSocketTurn(
		context.Background(), c, account, payload, parsed, true,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.SyntheticPrewarm)
	require.NotEmpty(t, result.ResponseID)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Empty(t, upstream.requests, "generate=false must never call Kiro upstream")
	require.Len(t, events, 1)
	require.Equal(t, "response.completed", gjson.GetBytes(events[0], "type").String())
	require.Equal(t, result.ResponseID, gjson.GetBytes(events[0], "response.id").String())
	require.Equal(t, "completed", gjson.GetBytes(events[0], "response.status").String())
	require.Equal(t, int64(0), gjson.GetBytes(events[0], "response.usage.total_tokens").Int())

	history, ok := globalKiroResponsesHistoryStore.load(result.ResponseID)
	require.True(t, ok, "synthetic prewarm must remain a valid continuation anchor")
	require.Contains(t, string(history.Input), "prewarm system context")
	require.NotContains(t, string(history.Input), "additional_tools", "tool declaration carrier is not conversation history")
}

func TestForwardAsResponsesWebSocketTurnSyntheticPrewarmClientDisconnectDoesNotReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	payload := []byte(`{"type":"response.create","generate":false,"model":"gpt-5.6-sol","input":[],"stream":true}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(payload), "responses")
	require.NoError(t, err)
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "must never run")

	result, err := svc.ForwardAsResponsesWebSocketTurn(
		context.Background(), c, account, payload, parsed, true,
		func([]byte) error { return context.Canceled },
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.SyntheticPrewarm)
	require.True(t, result.ClientDisconnect)
	require.Empty(t, upstream.requests, "client disconnect during local prewarm must not trigger upstream or replay")
}

func TestForwardAsResponsesWebSocketTurnContinuesFromSyntheticPrewarmAcrossConnections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "business turn completed")
	prewarmPayload := []byte(`{
		"type":"response.create",
		"generate":false,
		"model":"gpt-5.6-sol",
		"store":false,
		"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"durable prewarm instruction"}]}],
		"stream":true
	}`)
	prewarmContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	prewarmContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(prewarmPayload))
	prewarmParsed, err := ParseGatewayRequest(NewRequestBodyRef(prewarmPayload), "responses")
	require.NoError(t, err)
	prewarmResult, err := svc.ForwardAsResponsesWebSocketTurn(
		context.Background(), prewarmContext, account, prewarmPayload, prewarmParsed, true, func([]byte) error { return nil },
	)
	require.NoError(t, err)
	require.True(t, prewarmResult.SyntheticPrewarm)
	require.Empty(t, upstream.requests)

	businessPayload := []byte(`{
		"type":"response.create",
		"model":"gpt-5.6-sol",
		"previous_response_id":"` + prewarmResult.ResponseID + `",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"run the business turn"}]}],
		"stream":true
	}`)
	businessContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	businessContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(businessPayload))
	businessParsed, err := ParseGatewayRequest(NewRequestBodyRef(businessPayload), "responses")
	require.NoError(t, err)
	events := make([][]byte, 0, 8)
	businessResult, err := svc.ForwardAsResponsesWebSocketTurn(
		context.Background(), businessContext, account, businessPayload, businessParsed, true,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, businessResult)
	require.False(t, businessResult.SyntheticPrewarm)
	require.Len(t, upstream.requests, 1, "only the business turn may reach Kiro")
	require.Equal(t, 1, countWSResponseTerminals(events))
	history, ok := globalKiroResponsesHistoryStore.load(prewarmResult.ResponseID)
	require.True(t, ok)
	require.Contains(t, string(history.Input), "durable prewarm instruction")
	require.Contains(t, string(upstream.lastBody), "run the business turn")
}

func TestProxyResponsesWebSocketKiroSyntheticPrewarmThenBusinessTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	kiroSvc, upstream, account := newKiroNativeGPTTestRuntime(t, "business after prewarm")
	proxySvc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	turnResults := make([]*OpenAIForwardResult, 0, 2)
	turnErrors := make([]error, 0, 2)
	hooks := &OpenAIWSIngressHooks{
		BridgeTurn: func(
			ctx context.Context,
			bridgeContext *gin.Context,
			bridgeAccount *Account,
			payload []byte,
			turn int,
			writeClientMessage func([]byte) error,
		) (*OpenAIForwardResult, error) {
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(payload), "responses")
			if err != nil {
				return nil, err
			}
			result, err := kiroSvc.ForwardAsResponsesWebSocketTurn(
				ctx, bridgeContext, bridgeAccount, payload, parsed, turn == 1, writeClientMessage,
			)
			return openAIForwardResultForKiroProxyTest(result), err
		},
		AfterTurn: func(_ int, result *OpenAIForwardResult, turnErr error) {
			turnResults = append(turnResults, result)
			turnErrors = append(turnErrors, turnErr)
		},
	}

	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, firstMessage, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(r.Context())
		errCh <- proxySvc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "", firstMessage, hooks)
	}))
	defer server.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()

	writeWSMessage(t, client, `{
		"type":"response.create",
		"generate":false,
		"model":"gpt-5.6-sol",
		"store":false,
		"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"prewarm"}]}],
		"stream":true
	}`)
	prewarmEvents := readWSResponsesTurn(t, client)
	require.Equal(t, 1, countWSResponseTerminals(prewarmEvents))
	prewarmID := responseIDFromWSEvents(prewarmEvents)
	require.NotEmpty(t, prewarmID)
	require.Empty(t, upstream.requests, "prewarm must complete before any Kiro request")

	writeWSMessage(t, client, `{
		"type":"response.create",
		"model":"gpt-5.6-sol",
		"previous_response_id":"`+prewarmID+`",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"do business"}]}],
		"stream":true
	}`)
	businessEvents := readWSResponsesTurn(t, client)
	require.Equal(t, 1, countWSResponseTerminals(businessEvents))
	require.True(t, wsEventsContainText(businessEvents, "business after prewarm"))
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case proxyErr := <-errCh:
		require.NoError(t, proxyErr)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Kiro websocket bridge to close")
	}
	require.Len(t, upstream.requests, 1, "one client business turn must produce one upstream request")
	require.Len(t, turnResults, 2)
	require.Len(t, turnErrors, 2)
	require.NoError(t, turnErrors[0])
	require.NoError(t, turnErrors[1])
	require.True(t, turnResults[0].SyntheticPrewarm)
	require.Zero(t, turnResults[0].Usage.InputTokens)
	require.Zero(t, turnResults[0].Usage.OutputTokens)
	require.False(t, turnResults[1].SyntheticPrewarm)
	require.Greater(t, turnResults[1].Usage.InputTokens, 0)
}

func openAIForwardResultForKiroProxyTest(result *ForwardResult) *OpenAIForwardResult {
	if result == nil {
		return nil
	}
	return &OpenAIForwardResult{
		RequestID:        result.RequestID,
		ResponseID:       result.ResponseID,
		Usage:            OpenAIUsage{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens},
		Model:            result.Model,
		Stream:           result.Stream,
		Duration:         result.Duration,
		SyntheticPrewarm: result.SyntheticPrewarm,
	}
}

func TestForwardAsResponsesWebSocketTurnFlushesBeforeKiroTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":"hello","stream":true}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(payload), "responses")
	require.NoError(t, err)
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "")
	streamReader, streamWriter := io.Pipe()
	upstream.resp = &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-request-id": []string{"rid_ws_latency"}},
		Body:       streamReader,
	}

	type bridgeOutcome struct {
		result *ForwardResult
		err    error
	}
	firstClientEvent := make(chan struct{}, 1)
	outcomeCh := make(chan bridgeOutcome, 1)
	go func() {
		result, forwardErr := svc.ForwardAsResponsesWebSocketTurn(
			context.Background(), c, account, payload, parsed, false,
			func([]byte) error {
				select {
				case firstClientEvent <- struct{}{}:
				default:
				}
				return nil
			},
		)
		outcomeCh <- bridgeOutcome{result: result, err: forwardErr}
	}()

	_, err = streamWriter.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "first delta"},
	}))
	require.NoError(t, err)
	select {
	case <-firstClientEvent:
	case <-time.After(2 * time.Second):
		t.Fatal("first websocket event was buffered until Kiro terminal")
	}
	select {
	case outcome := <-outcomeCh:
		t.Fatalf("bridge returned before terminal event: result=%v err=%v", outcome.result, outcome.err)
	default:
	}

	_, err = streamWriter.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 4, "outputTokens": 2}},
	}))
	require.NoError(t, err)
	_, err = streamWriter.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))
	require.NoError(t, err)
	require.NoError(t, streamWriter.Close())
	select {
	case outcome := <-outcomeCh:
		require.NoError(t, outcome.err)
		require.NotNil(t, outcome.result)
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not finish after Kiro terminal")
	}
}

func TestForwardAsResponsesWebSocketTurnDrainsAfterClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":"hello","stream":true}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(payload), "responses")
	require.NoError(t, err)
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "drain to terminal")
	writes := 0

	result, err := svc.ForwardAsResponsesWebSocketTurn(
		context.Background(), c, account, payload, parsed, false,
		func([]byte) error {
			writes++
			return context.Canceled
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 1, writes, "bridge must stop writing after disconnect but continue draining upstream")
	require.Len(t, upstream.requests, 1, "client disconnect must not regenerate on Kiro")
}

func TestForwardAsResponsesWebSocketTurnCoalescesInterleavedTextReasoningAndTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := append([]byte(`{"type":"response.create",`), bytes.TrimSpace(kiroNativeGPTToolRequestBody())[1:]...)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(payload), "responses")
	require.NoError(t, err)

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "read the "},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "choose exec"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "workspace"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_ws_interleaved",
			"name":      "exec",
			"input":     `{"input":"text(\"workspace\")"}`,
			"stop":      true,
		},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 11, "outputTokens": 7}},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
	}))

	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "")
	upstream.resp = &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-request-id": []string{"rid_ws_interleaved"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
	events := make([][]byte, 0, 16)
	result, err := svc.ForwardAsResponsesWebSocketTurn(
		context.Background(), c, account, payload, parsed, false,
		func(message []byte) error {
			events = append(events, append([]byte(nil), message...))
			return nil
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, countWSResponseTerminals(events))
	messageItems := 0
	toolItems := 0
	toolItemLifecycleFields := 0
	toolItemInputDone := 0
	for _, event := range events {
		eventType := gjson.GetBytes(event, "type").String()
		if eventType != "response.output_item.added" && eventType != "response.output_item.done" {
			continue
		}
		itemType := gjson.GetBytes(event, "item.type").String()
		switch itemType {
		case "message":
			if eventType == "response.output_item.added" {
				messageItems++
			}
		case "custom_tool_call":
			if eventType == "response.output_item.added" {
				toolItems++
			}
			if gjson.GetBytes(event, "item.call_id").String() == "toolu_ws_interleaved" &&
				gjson.GetBytes(event, "item.name").String() == "exec" {
				toolItemLifecycleFields++
			}
			if eventType == "response.output_item.done" && strings.TrimSpace(gjson.GetBytes(event, "item.input").String()) != "" {
				toolItemInputDone++
			}
		}
	}
	require.Equal(t, 1, messageItems, "text -> reasoning -> text must remain one assistant message item")
	require.Equal(t, 1, toolItems)
	require.Equal(t, 2, toolItemLifecycleFields, "custom tool call_id/name must be present on added and done items")
	require.Equal(t, 1, toolItemInputDone, "custom tool input must be present on the completed item")
	require.True(t, wsEventsContainText(events, "read the workspace"))
}

func TestProxyResponsesWebSocketFromClientKiroBridgeContinuesCustomToolTurn(t *testing.T) {
	tests := []struct {
		name            string
		firstPayload    string
		kiroToolName    string
		clientNamespace string
	}{
		{
			name:         "legacy custom tool without namespace",
			firstPayload: strings.TrimSpace(string(kiroNativeGPTToolRequestBody())),
			kiroToolName: "exec",
		},
		{
			name: "namespaced custom tool",
			firstPayload: `{
				"model":"gpt-5.6-sol",
				"input":[
					{"role":"user","content":[{"type":"input_text","text":"inspect the workspace"}]},
					{"type":"additional_tools","tools":[
						{"type":"namespace","name":"functions","tools":[
							{"type":"custom","name":"exec","description":"Run JavaScript orchestration"}
						]}
					]}
				],
				"stream":true
			}`,
			kiroToolName:    "functionsExec",
			clientNamespace: "functions",
		},
		{
			name: "namespaced custom tool with pre-fix bare history",
			firstPayload: `{
				"model":"gpt-5.6-sol",
				"input":[
					{"role":"user","content":[{"type":"input_text","text":"inspect the workspace"}]},
					{"type":"function_call","call_id":"call_stale_exec","name":"exec","arguments":"{\"input\":\"text(\\\"stale\\\")\"}"},
					{"type":"function_call_output","call_id":"call_stale_exec","output":"stale ok"},
					{"type":"additional_tools","tools":[
						{"type":"namespace","name":"functions","tools":[
							{"type":"custom","name":"exec","description":"Run JavaScript orchestration"}
						]}
					]}
				],
				"stream":true
			}`,
			kiroToolName:    "functionsExec",
			clientNamespace: "functions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			resetKiroResponsesHistoryStoreForTest()
			kiroSvc, upstream, account := newKiroNativeGPTTestRuntime(t, "")
			upstream.resp = nil
			upstream.responses = []*http.Response{
				kiroCustomToolEventStreamResponse(t, "toolu_ws_exec", tt.kiroToolName, `{"input":"text(\"workspace\")"}`),
				kiroCustomToolEventStreamResponse(t, "toolu_ws_exec_again", tt.kiroToolName, `{"input":"text(\"again\")"}`),
			}

			openAISvc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			hooks := &OpenAIWSIngressHooks{
				BridgeTurn: func(
					ctx context.Context,
					c *gin.Context,
					account *Account,
					payload []byte,
					turn int,
					writeClientMessage func([]byte) error,
				) (*OpenAIForwardResult, error) {
					parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(payload), "responses")
					if parseErr != nil {
						return nil, parseErr
					}
					gatewayResult, forwardErr := kiroSvc.ForwardAsResponsesWebSocketTurn(ctx, c, account, payload, parsed, turn == 1, writeClientMessage)
					if gatewayResult == nil {
						return nil, forwardErr
					}
					return &OpenAIForwardResult{
						RequestID:  gatewayResult.RequestID,
						ResponseID: gatewayResult.ResponseID,
						Model:      gatewayResult.Model,
						Stream:     gatewayResult.Stream,
					}, forwardErr
				},
			}

			errCh := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, acceptErr := coderws.Accept(w, r, nil)
				if acceptErr != nil {
					errCh <- acceptErr
					return
				}
				defer func() { _ = conn.CloseNow() }()
				readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
				_, firstMessage, readErr := conn.Read(readCtx)
				cancelRead()
				if readErr != nil {
					errCh <- readErr
					return
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = r.Clone(r.Context())
				errCh <- openAISvc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "", firstMessage, hooks)
			}))
			defer server.Close()

			dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
			client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			cancelDial()
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()

			writeWSMessage(t, client, tt.firstPayload)
			firstEvents := readWSResponsesTurn(t, client)
			require.Equal(t, 1, countWSResponseTerminals(firstEvents))
			firstResponseID := responseIDFromWSEvents(firstEvents)
			require.NotEmpty(t, firstResponseID)
			requireWSCustomToolDone(t, firstEvents, "toolu_ws_exec", tt.clientNamespace, `text("workspace")`)

			writeWSMessage(t, client, `{
				"type":"response.create",
				"model":"gpt-5.6-sol",
				"previous_response_id":"`+firstResponseID+`",
				"input":[{"type":"custom_tool_call_output","call_id":"toolu_ws_exec","output":"workspace ok"}],
				"stream":true
			}`)
			secondEvents := readWSResponsesTurn(t, client)
			require.Equal(t, 1, countWSResponseTerminals(secondEvents))
			requireWSCustomToolDone(t, secondEvents, "toolu_ws_exec_again", tt.clientNamespace, `text("again")`)

			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case proxyErr := <-errCh:
				require.NoError(t, proxyErr)
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for Kiro websocket bridge to close")
			}

			require.Len(t, upstream.bodies, 2)
			assertKiroCodexToolCycle(t, upstream.bodies[1], "toolu_ws_exec", tt.kiroToolName, `text("workspace")`, "workspace ok")
			tools := gjson.GetBytes(upstream.bodies[1], "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Array()
			require.Len(t, tools, 1, "continuation must not inject a placeholder for the same logical tool")
			require.Equal(t, tt.kiroToolName, tools[0].Get("toolSpecification.name").String())
		})
	}
}

func TestForwardAsResponsesWebSocketTurnPreservesPreviousResponseAcrossConnections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "")
	upstream.resp = nil
	upstream.responses = []*http.Response{
		kiroCustomToolEventStreamResponse(t, "toolu_ws_reconnect", "exec", `{"input":"text(\"reconnect\")"}`),
		kiroEventStreamResponse(t, "reconnected turn complete", 9, 3),
	}

	firstPayload := append([]byte(`{"type":"response.create",`), bytes.TrimSpace(kiroNativeGPTToolRequestBody())[1:]...)
	firstContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	firstContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstPayload))
	firstParsed, err := ParseGatewayRequest(NewRequestBodyRef(firstPayload), "responses")
	require.NoError(t, err)
	firstResult, err := svc.ForwardAsResponsesWebSocketTurn(
		context.Background(), firstContext, account, firstPayload, firstParsed, true, func([]byte) error { return nil },
	)
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.NotEmpty(t, firstResult.ResponseID)

	secondPayload := []byte(`{
		"type":"response.create",
		"model":"gpt-5.6-sol",
		"previous_response_id":"` + firstResult.ResponseID + `",
		"input":[{"type":"custom_tool_call_output","call_id":"toolu_ws_reconnect","output":"reconnect ok"}],
		"stream":true
	}`)
	secondContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondPayload))
	secondParsed, err := ParseGatewayRequest(NewRequestBodyRef(secondPayload), "responses")
	require.NoError(t, err)
	secondEvents := make([][]byte, 0, 8)
	secondResult, err := svc.ForwardAsResponsesWebSocketTurn(
		context.Background(), secondContext, account, secondPayload, secondParsed, true,
		func(message []byte) error {
			secondEvents = append(secondEvents, append([]byte(nil), message...))
			return nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.True(t, wsEventsContainText(secondEvents, "reconnected turn complete"))
	require.Len(t, upstream.bodies, 2)
	assertKiroCodexToolCycle(t, upstream.bodies[1], "toolu_ws_reconnect", "exec", `text("reconnect")`, "reconnect ok")
}

func TestProxyResponsesWebSocketKiroBridgeProhibitsReplayAfterClientOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 77, Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1}
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	failoverErr := &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, FailureKind: UpstreamFailureIncompleteStream}
	hooks := &OpenAIWSIngressHooks{
		BridgeTurn: func(
			_ context.Context,
			_ *gin.Context,
			_ *Account,
			_ []byte,
			_ int,
			writeClientMessage func([]byte) error,
		) (*OpenAIForwardResult, error) {
			require.NoError(t, writeClientMessage([]byte(`{"type":"response.output_text.delta","delta":"partial"}`)))
			return &OpenAIForwardResult{Model: kiroNativeGPTTestModel, Stream: true}, failoverErr
		},
	}

	proxyErr := runSingleTurnBridgeProxy(t, svc, account, hooks)
	require.Error(t, proxyErr)
	var got *UpstreamFailoverError
	require.True(t, errors.As(proxyErr, &got))
	require.True(t, got.FailoverProhibited)
}

func TestProxyResponsesWebSocketKiroBridgeProhibitsReplayFromSecondTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 78, Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1}
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	failoverErr := &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, FailureKind: UpstreamFailureIncompleteStream}
	hooks := &OpenAIWSIngressHooks{
		BridgeTurn: func(
			_ context.Context,
			_ *gin.Context,
			_ *Account,
			_ []byte,
			turn int,
			writeClientMessage func([]byte) error,
		) (*OpenAIForwardResult, error) {
			if turn == 1 {
				require.NoError(t, writeClientMessage([]byte(`{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.6-sol","output":[]}}`)))
				return &OpenAIForwardResult{ResponseID: "resp_first", Model: kiroNativeGPTTestModel, Stream: true}, nil
			}
			return nil, failoverErr
		},
	}

	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, firstMessage, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(r.Context())
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "", firstMessage, hooks)
	}))
	defer server.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	writeWSMessage(t, client, `{"type":"response.create","model":"gpt-5.6-sol","input":"first","stream":true}`)
	firstEvents := readWSResponsesTurn(t, client)
	require.Equal(t, 1, countWSResponseTerminals(firstEvents))
	writeWSMessage(t, client, `{"type":"response.create","model":"gpt-5.6-sol","previous_response_id":"resp_first","input":"second","stream":true}`)

	select {
	case proxyErr := <-errCh:
		require.Error(t, proxyErr)
		var got *UpstreamFailoverError
		require.True(t, errors.As(proxyErr, &got))
		require.True(t, got.FailoverProhibited)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second-turn bridge failure")
	}
}

func TestProxyResponsesWebSocketKiroBridgeAllowsFirstTurnPrewriteFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 79, Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1}
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	failoverErr := &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, FailureKind: UpstreamFailureTransportError}
	hooks := &OpenAIWSIngressHooks{
		BridgeTurn: func(context.Context, *gin.Context, *Account, []byte, int, func([]byte) error) (*OpenAIForwardResult, error) {
			return nil, failoverErr
		},
	}

	proxyErr := runBridgeProxyWithoutClientRead(t, svc, account, hooks)
	require.Error(t, proxyErr)
	var got *UpstreamFailoverError
	require.True(t, errors.As(proxyErr, &got))
	require.False(t, got.FailoverProhibited)
}

func runSingleTurnBridgeProxy(t *testing.T, svc *OpenAIGatewayService, account *Account, hooks *OpenAIWSIngressHooks) error {
	t.Helper()
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, firstMessage, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(r.Context())
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "", firstMessage, hooks)
	}))
	defer server.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	writeWSMessage(t, client, `{"type":"response.create","model":"gpt-5.6-sol","input":"test","stream":true}`)
	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = client.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	_ = client.CloseNow()
	select {
	case proxyErr := <-errCh:
		return proxyErr
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for bridge proxy result")
		return nil
	}
}

func runBridgeProxyWithoutClientRead(t *testing.T, svc *OpenAIGatewayService, account *Account, hooks *OpenAIWSIngressHooks) error {
	t.Helper()
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, firstMessage, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(r.Context())
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "", firstMessage, hooks)
	}))
	defer server.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	writeWSMessage(t, client, `{"type":"response.create","model":"gpt-5.6-sol","input":"test","stream":true}`)
	select {
	case proxyErr := <-errCh:
		return proxyErr
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for bridge proxy result")
		return nil
	}
}

func writeWSMessage(t *testing.T, conn *coderws.Conn, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(payload)))
}

func readWSResponsesTurn(t *testing.T, conn *coderws.Conn) [][]byte {
	t.Helper()
	events := make([][]byte, 0, 16)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		messageType, message, err := conn.Read(ctx)
		cancel()
		require.NoError(t, err)
		require.Equal(t, coderws.MessageText, messageType)
		events = append(events, append([]byte(nil), message...))
		if isOpenAIWSTerminalEvent(gjson.GetBytes(message, "type").String()) {
			return events
		}
	}
}

func countWSResponseTerminals(events [][]byte) int {
	count := 0
	for _, event := range events {
		if isOpenAIWSTerminalEvent(gjson.GetBytes(event, "type").String()) {
			count++
		}
	}
	return count
}

func responseIDFromWSEvents(events [][]byte) string {
	for i := len(events) - 1; i >= 0; i-- {
		if id := strings.TrimSpace(gjson.GetBytes(events[i], "response.id").String()); id != "" {
			return id
		}
	}
	return ""
}

func wsEventsContainType(events [][]byte, eventType string) bool {
	for _, event := range events {
		if gjson.GetBytes(event, "type").String() == eventType {
			return true
		}
	}
	return false
}

func wsEventsContainText(events [][]byte, expected string) bool {
	for _, event := range events {
		if strings.Contains(string(event), expected) {
			return true
		}
	}
	return false
}

func requireWSCustomToolDone(t *testing.T, events [][]byte, callID, namespace, input string) {
	t.Helper()
	for _, event := range events {
		if gjson.GetBytes(event, "type").String() != "response.output_item.done" ||
			gjson.GetBytes(event, "item.call_id").String() != callID {
			continue
		}
		require.Equal(t, "custom_tool_call", gjson.GetBytes(event, "item.type").String())
		require.Equal(t, "exec", gjson.GetBytes(event, "item.name").String())
		require.Equal(t, namespace, gjson.GetBytes(event, "item.namespace").String())
		require.Equal(t, input, gjson.GetBytes(event, "item.input").String())
		return
	}
	t.Fatalf("missing completed custom tool call %s", callID)
}
