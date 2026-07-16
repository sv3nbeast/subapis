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

const kiroNativeGPTTestModel = "gpt-5.6-sol"

func newKiroNativeGPTTestRuntime(t *testing.T, responseText string) (*GatewayService, *httpUpstreamRecorder, *Account) {
	t.Helper()
	upstream := &httpUpstreamRecorder{resp: kiroEventStreamResponse(t, responseText, 11, 5)}
	svc := &GatewayService{
		cfg:                 &config.Config{},
		httpUpstream:        upstream,
		kiroCooldownStore:   &testKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          1701,
		Name:        "kiro-native-gpt",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/KIRO",
			"model_mapping": map[string]any{
				kiroNativeGPTTestModel: kiroNativeGPTTestModel,
			},
		},
	}
	return svc, upstream, account
}

func assertKiroNativeGPTUpstreamRequest(t *testing.T, upstream *httpUpstreamRecorder) {
	t.Helper()
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
	require.Equal(t, kiroNativeGPTTestModel, gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
}

func TestForwardAsChatCompletionsKiroUsesNativeGPTModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello chat"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "native gpt chat ok")

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: kiroNativeGPTTestModel,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assertKiroNativeGPTUpstreamRequest(t, upstream)
	require.Equal(t, "native gpt chat ok", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
	require.Equal(t, kiroNativeGPTTestModel, result.Model)
	require.Equal(t, kiroNativeGPTTestModel, result.UpstreamModel)
}

func TestForwardAsResponsesKiroUsesNativeGPTModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"type":"input_text","text":"hello responses"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "native gpt responses ok")

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: kiroNativeGPTTestModel,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assertKiroNativeGPTUpstreamRequest(t, upstream)
	require.Equal(t, "native gpt responses ok", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.Equal(t, kiroNativeGPTTestModel, result.Model)
	require.Equal(t, kiroNativeGPTTestModel, result.UpstreamModel)
}

func TestForwardAsResponsesKiroCarriesCodexCustomToolAndRestoresCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"inspect the workspace"}]},
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"custom","name":"exec","description":"Run JavaScript orchestration","format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}}
			]}
		],
		"stream":true
	}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_exec",
			"name":      "exec",
			"input":     `{"input":"const result = await tools.exec_command({cmd: \"pwd\"}); text(result.output);"}`,
			"stop":      true,
		},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 11, "outputTokens": 5}},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
	}))

	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "")
	upstream.resp = &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-request-id": []string{"rid_kiro_exec"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: kiroNativeGPTTestModel,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(1), gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.#").Int())
	require.Equal(t, "exec", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.name").String())
	require.Equal(t, "string", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.inputSchema.json.properties.input.type").String())
	require.Contains(t, rec.Body.String(), `"type":"custom_tool_call"`)
	require.Contains(t, rec.Body.String(), `event: response.custom_tool_call_input.delta`)
	require.Contains(t, rec.Body.String(), `tools.exec_command`)
	require.NotContains(t, rec.Body.String(), `"type":"function_call","id"`)
}

func TestForwardAsResponsesKiroContinuesCodexCustomToolWithPreviousResponseID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "")
	upstream.resp = nil
	upstream.responses = []*http.Response{
		kiroCustomToolEventStreamResponse(t, "toolu_exec", "exec", `{"input":"text(\"hello\")"}`),
		kiroCustomToolEventStreamResponse(t, "toolu_exec_again", "exec", `{"input":"text(\"again\")"}`),
	}

	firstBody := []byte(`{
		"model":"gpt-5.6-sol",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"inspect the workspace"}]},
			{"type":"additional_tools","tools":[{"type":"custom","name":"exec","description":"Run JavaScript"}]}
		],
		"stream":true
	}`)
	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(firstBody))
	firstCtx.Request.Header.Set("Content-Type", "application/json")

	firstResult, err := svc.ForwardAsResponses(context.Background(), firstCtx, account, firstBody, &ParsedRequest{
		Body:  NewRequestBodyRef(firstBody),
		Model: kiroNativeGPTTestModel,
	})
	require.NoError(t, err)
	require.NotNil(t, firstResult)
	require.NotEmpty(t, firstResult.ResponseID)
	require.Contains(t, firstRec.Body.String(), `"type":"custom_tool_call"`)
	storedFirst, stored := globalKiroResponsesHistoryStore.load(firstResult.ResponseID)
	require.True(t, stored)
	require.Len(t, storedFirst.Output, 1)
	require.Equal(t, "custom_tool_call", storedFirst.Output[0].Type)
	require.Contains(t, globalKiroResponsesHistoryStore.customToolNames(firstResult.ResponseID), "exec")

	// Codex may send only the custom tool result on the next stored turn. This
	// intentionally omits tool declarations to verify history-item conversion
	// is not discarded when DeclaredToolCount is zero.
	secondBody := []byte(`{
		"model":"gpt-5.6-sol",
		"previous_response_id":"` + firstResult.ResponseID + `",
		"input":[{"type":"custom_tool_call_output","call_id":"toolu_exec","output":"hello"}],
		"stream":false
	}`)
	secondRec := httptest.NewRecorder()
	secondCtx, _ := gin.CreateTestContext(secondRec)
	secondCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(secondBody))
	secondCtx.Request.Header.Set("Content-Type", "application/json")

	secondResult, err := svc.ForwardAsResponses(context.Background(), secondCtx, account, secondBody, &ParsedRequest{
		Body:  NewRequestBodyRef(secondBody),
		Model: kiroNativeGPTTestModel,
	})
	require.NoError(t, err)
	require.NotNil(t, secondResult)
	require.Equal(t, "custom_tool_call", gjson.Get(secondRec.Body.String(), "output.0.type").String())
	require.Equal(t, "exec", gjson.Get(secondRec.Body.String(), "output.0.name").String())
	require.Equal(t, `text("again")`, gjson.Get(secondRec.Body.String(), "output.0.input").String())
	require.Len(t, upstream.bodies, 2)
	assertKiroCodexToolCycle(t, upstream.bodies[1], "toolu_exec", "exec", `text("hello")`, "hello")
}

func TestForwardAsResponsesKiroCarriesCodexCustomToolInStoreFalseHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"store":false,
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"inspect the workspace"}]},
			{"type":"custom_tool_call","call_id":"toolu_exec","name":"exec","input":"text(\"hello\")"},
			{"type":"custom_tool_call_output","call_id":"toolu_exec","output":"hello"},
			{"type":"additional_tools","tools":[{"type":"custom","name":"exec","description":"Run JavaScript"}]}
		],
		"stream":false
	}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "continued without storage")

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: kiroNativeGPTTestModel,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "continued without storage", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	assertKiroCodexToolCycle(t, upstream.lastBody, "toolu_exec", "exec", `text("hello")`, "hello")
	_, stored := globalKiroResponsesHistoryStore.load(result.ResponseID)
	require.False(t, stored)
}

func TestForwardAsResponsesKiroCoalescesTextAroundLateReasoning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"type":"input_text","text":"continue"}],"reasoning":{"effort":"xhigh","summary":"auto"},"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "安全风"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "late summary"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "险清单。"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 8, "outputTokens": 4}},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))

	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "")
	upstream.resp = &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-request-id": []string{"rid_kiro_coalesce"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: kiroNativeGPTTestModel,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	wire := rec.Body.String()
	require.Equal(t, 1, strings.Count(wire, `"role":"assistant","status":"in_progress","type":"message"`))
	require.Contains(t, wire, `"text":"安全风险清单。"`)
	require.Contains(t, wire, `"status":"completed"`)
}

func TestForwardMessagesKiroUsesNativeGPTModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","max_tokens":256,"messages":[{"role":"user","content":"hello messages"}],"stream":false}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "native gpt messages ok")

	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	assertKiroNativeGPTUpstreamRequest(t, upstream)
	require.Equal(t, "native gpt messages ok", gjson.Get(rec.Body.String(), "content.0.text").String())
	require.Equal(t, kiroNativeGPTTestModel, result.Model)
	require.Equal(t, kiroNativeGPTTestModel, result.UpstreamModel)
}

func kiroCustomToolEventStreamResponse(t *testing.T, toolUseID, name, input string) *http.Response {
	t.Helper()
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": toolUseID,
			"name":      name,
			"input":     input,
			"stop":      true,
		},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 11, "outputTokens": 5}},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
	}))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-request-id": []string{"rid_kiro_custom_tool"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
}

func assertKiroCodexToolCycle(t *testing.T, payload []byte, toolUseID, name, input, output string) {
	t.Helper()
	foundToolUse := false
	for _, historyItem := range gjson.GetBytes(payload, "conversationState.history").Array() {
		for _, toolUse := range historyItem.Get("assistantResponseMessage.toolUses").Array() {
			if toolUse.Get("toolUseId").String() != toolUseID {
				continue
			}
			foundToolUse = true
			require.Equal(t, name, toolUse.Get("name").String())
			require.Equal(t, input, toolUse.Get("input.input").String())
		}
	}
	require.True(t, foundToolUse, "Kiro payload must retain the custom tool_use in history")

	results := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.toolResults").Array()
	require.Len(t, results, 1)
	require.Equal(t, toolUseID, results[0].Get("toolUseId").String())
	require.Equal(t, "success", results[0].Get("status").String())
	require.Equal(t, output, results[0].Get("content.0.text").String())
}
