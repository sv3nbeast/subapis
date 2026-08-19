package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardAsResponsesKiroCompactReturnsOpaqueTokenAndPersistsSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStore := globalKiroResponsesHistoryStore
	globalKiroResponsesHistoryStore = newKiroResponsesHistoryStoreForDir(t.TempDir())
	t.Cleanup(func() { globalKiroResponsesHistoryStore = oldStore })

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"fix request d7c0"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"root cause found"}]},
			{"type":"additional_tools","tools":[{"type":"custom","name":"exec","description":"Run JavaScript"}]},
			{"type":"compaction_trigger"}
		]
	}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "summary keeps request d7c0 and its root cause")
	enableKiroNativeGPTEnforceMode(svc)
	groupID := int64(33)
	parsed := &ParsedRequest{
		Body:           NewRequestBodyRef(body),
		Model:          kiroNativeGPTTestModel,
		GroupID:        &groupID,
		SessionContext: &SessionContext{APIKeyID: 1068},
	}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.ResponsesOutput, 1)
	require.Equal(t, "compaction", result.ResponsesOutput[0].Type)
	token := result.ResponsesOutput[0].EncryptedContent
	require.True(t, strings.HasPrefix(token, "kiro_cmp_"))
	require.NotContains(t, token, "summary keeps")
	require.Equal(t, token, gjson.GetBytes(rec.Body.Bytes(), "output.0.encrypted_content").String())
	require.Equal(t, int64(1), gjson.GetBytes(rec.Body.Bytes(), "output.#").Int())

	summary, ok := globalKiroResponsesHistoryStore.loadCompact(token, kiroResponsesScope{APIKeyID: 1068, GroupID: 33})
	require.True(t, ok)
	require.Equal(t, "summary keeps request d7c0 and its root cause", summary)

	upstreamText := string(upstream.lastBody)
	require.Contains(t, upstreamText, "fix request d7c0")
	require.Contains(t, upstreamText, "Create a compact, lossless continuation summary")
	require.NotContains(t, upstreamText, "compaction_trigger")
	require.Zero(t, gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.#").Int())
	require.Len(t, upstream.requests, 1, "compact summarization must not enter the native tool-progress retry loop")

	reloaded := newKiroResponsesHistoryStoreForDir(globalKiroResponsesHistoryStore.dir)
	reloadedSummary, ok := reloaded.loadCompact(token, kiroResponsesScope{APIKeyID: 1068, GroupID: 33})
	require.True(t, ok)
	require.Equal(t, summary, reloadedSummary)
}

func TestForwardAsResponsesKiroCompactBodySignalReturnsCompleteSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStore := globalKiroResponsesHistoryStore
	globalKiroResponsesHistoryStore = newKiroResponsesHistoryStoreForDir("")
	t.Cleanup(func() { globalKiroResponsesHistoryStore = oldStore })

	body := []byte(`{"model":"gpt-5.6-terra","input":[{"role":"user","content":"long history"},{"type":"compaction_trigger"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	MarkOpenAICompactClientStream(c)
	svc, _, account := newKiroNativeGPTTestRuntime(t, "compact SSE summary")
	enableKiroNativeGPTEnforceMode(svc)
	account.Credentials["model_mapping"] = map[string]any{"gpt-5.6-terra": "gpt-5.6-terra"}
	groupID := int64(33)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "gpt-5.6-terra", GroupID: &groupID, SessionContext: &SessionContext{APIKeyID: 1068}}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	wire := rec.Body.String()
	require.Equal(t, 1, strings.Count(wire, "event: response.output_item.done"))
	require.Equal(t, 2, strings.Count(wire, `"type":"compaction"`), "the one item appears in output_item.done and the completed response")
	require.Equal(t, 1, strings.Count(wire, "event: response.completed"))
	require.NotContains(t, wire, "response.failed")
}

func TestForwardAsResponsesKiroRemoteCompactionV2ReturnsExactlyOneCompactionItem(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStore := globalKiroResponsesHistoryStore
	globalKiroResponsesHistoryStore = newKiroResponsesHistoryStoreForDir("")
	t.Cleanup(func() { globalKiroResponsesHistoryStore = oldStore })

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":true,
		"input":[
			{"role":"user","content":"preserve request 28e5 and the pending production checks"},
			{"type":"compaction_trigger"}
		]
	}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Add("X-Codex-Beta-Features", "another_feature")
	c.Request.Header.Add("X-Codex-Beta-Features", "tool_search, remote_compaction_v2")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "compact request 28e5; production checks remain pending")
	enableKiroNativeGPTEnforceMode(svc)
	groupID := int64(33)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: kiroNativeGPTTestModel, GroupID: &groupID, SessionContext: &SessionContext{APIKeyID: 1068}}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.ResponsesOutput, 1)
	require.Equal(t, "compaction", result.ResponsesOutput[0].Type)
	require.True(t, strings.HasPrefix(result.ResponsesOutput[0].EncryptedContent, "kiro_cmp_"))

	wire := rec.Body.String()
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.Equal(t, 1, strings.Count(wire, "event: response.output_item.done"))
	require.Equal(t, 1, strings.Count(wire, "event: response.completed"))
	require.Equal(t, 2, strings.Count(wire, `"type":"compaction"`), "the same single item appears once in output_item.done and once inside response.completed")
	require.NotContains(t, wire, `"type":"message"`)
	require.NotContains(t, wire, `"type":"reasoning"`)
	require.NotContains(t, wire, "response.failed")

	upstreamText := string(upstream.lastBody)
	require.Contains(t, upstreamText, "preserve request 28e5")
	require.Contains(t, upstreamText, "Create a compact, lossless continuation summary")
	require.NotContains(t, upstreamText, "compaction_trigger")
	require.Zero(t, gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.#").Int())
	require.Len(t, upstream.requests, 1)
}

func TestIsKiroRemoteCompactionV2RequestRequiresCompleteNativeWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	compactBody := []byte(`{"model":"gpt-5.6-sol","stream":true,"input":[{"type":"compaction_trigger"}]}`)
	ordinaryBody := []byte(`{"model":"gpt-5.6-sol","stream":true,"input":[{"role":"user","content":"hello"}]}`)

	tests := []struct {
		name         string
		path         string
		body         []byte
		clientStream bool
		headers      []string
		want         bool
	}{
		{name: "exact token", path: "/v1/responses", body: compactBody, clientStream: true, headers: []string{"remote_compaction_v2"}, want: true},
		{name: "comma and repeated headers", path: "/v1/responses/", body: compactBody, clientStream: true, headers: []string{"tool_search", "other, remote_compaction_v2"}, want: true},
		{name: "wrong token case", path: "/v1/responses", body: compactBody, clientStream: true, headers: []string{"REMOTE_COMPACTION_V2"}},
		{name: "missing trigger", path: "/v1/responses", body: ordinaryBody, clientStream: true, headers: []string{"remote_compaction_v2"}},
		{name: "non streaming", path: "/v1/responses", body: compactBody, clientStream: false, headers: []string{"remote_compaction_v2"}},
		{name: "legacy compact path", path: "/v1/responses/compact", body: compactBody, clientStream: true, headers: []string{"remote_compaction_v2"}},
		{name: "responses subpath", path: "/v1/responses/resp_1", body: compactBody, clientStream: true, headers: []string{"remote_compaction_v2"}},
		{name: "missing header", path: "/v1/responses", body: compactBody, clientStream: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader(tt.body))
			for _, value := range tt.headers {
				c.Request.Header.Add("X-Codex-Beta-Features", value)
			}
			require.Equal(t, tt.want, isKiroRemoteCompactionV2Request(c, tt.body, tt.clientStream))
		})
	}
}

func TestForwardAsResponsesKiroRemoteCompactionV2InvalidTokenTerminatesSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStore := globalKiroResponsesHistoryStore
	globalKiroResponsesHistoryStore = newKiroResponsesHistoryStoreForDir("")
	t.Cleanup(func() { globalKiroResponsesHistoryStore = oldStore })

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":true,
		"input":[
			{"type":"compaction","encrypted_content":"kiro_cmp_missing"},
			{"type":"compaction_trigger"}
		]
	}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("X-Codex-Beta-Features", "remote_compaction_v2")
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "must not run")
	groupID := int64(33)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: kiroNativeGPTTestModel, GroupID: &groupID, SessionContext: &SessionContext{APIKeyID: 1068}}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.ErrorIs(t, err, errKiroCompactTokenNotFound)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusOK, rec.Code, "streaming protocol reports the original 404 as an in-band terminal error")
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	wire := rec.Body.String()
	require.Equal(t, 1, strings.Count(wire, "event: response.failed"))
	require.Contains(t, wire, `"code":"invalid_request_error"`)
	require.Contains(t, wire, "Compact token was not found")
	require.NotContains(t, wire, "Kiro")
	require.NotContains(t, wire, "response.completed")
}

func TestForwardAsResponsesKiroExpandsCompactTokenWithScopeIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStore := globalKiroResponsesHistoryStore
	globalKiroResponsesHistoryStore = newKiroResponsesHistoryStoreForDir("")
	t.Cleanup(func() { globalKiroResponsesHistoryStore = oldStore })

	scope := kiroResponsesScope{APIKeyID: 1068, GroupID: 33}
	token, err := globalKiroResponsesHistoryStore.saveCompact(scope, "request d7c0 was fixed; continue production verification")
	require.NoError(t, err)
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"type":"compaction","encrypted_content":"` + token + `"},{"role":"user","content":"continue"}]}`)

	t.Run("same api key and group", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		svc, upstream, account := newKiroNativeGPTTestRuntime(t, "continued")
		groupID := int64(33)
		parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: kiroNativeGPTTestModel, GroupID: &groupID, SessionContext: &SessionContext{APIKeyID: 1068}}

		result, forwardErr := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

		require.NoError(t, forwardErr)
		require.NotNil(t, result)
		require.Contains(t, string(upstream.lastBody), "request d7c0 was fixed")
		require.NotContains(t, string(upstream.lastBody), token)
	})

	t.Run("different api key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		svc, upstream, account := newKiroNativeGPTTestRuntime(t, "must not run")
		groupID := int64(33)
		parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: kiroNativeGPTTestModel, GroupID: &groupID, SessionContext: &SessionContext{APIKeyID: 9999}}

		result, forwardErr := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

		require.ErrorIs(t, forwardErr, errKiroCompactTokenNotFound)
		require.Nil(t, result)
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Nil(t, upstream.lastReq)
	})
}

func TestExpandKiroCompactionInputKeepsOrdinaryInputByteIdentical(t *testing.T) {
	input := []byte(`[{"role":"user","content":"` + strings.Repeat("ordinary request ", 64*1024) + `"}]`)
	expanded, err := expandKiroCompactionInput(input, kiroResponsesScope{APIKeyID: 1, GroupID: 2})
	require.NoError(t, err)
	require.Equal(t, input, []byte(expanded))
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = expandKiroCompactionInput(input, kiroResponsesScope{APIKeyID: 1, GroupID: 2})
	})
	require.Zero(t, allocs, "ordinary Kiro GPT input must stay on the allocation-free compact fast path")
}

func TestForwardAsResponsesKiroCompactMissingTerminalDoesNotMintToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStore := globalKiroResponsesHistoryStore
	globalKiroResponsesHistoryStore = newKiroResponsesHistoryStoreForDir("")
	t.Cleanup(func() { globalKiroResponsesHistoryStore = oldStore })

	body := []byte(`{"model":"gpt-5.6-sol","input":[{"role":"user","content":"history"},{"type":"compaction_trigger"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader(body))
	svc, upstream, account := newKiroNativeGPTTestRuntime(t, "")
	enableKiroNativeGPTEnforceMode(svc)
	upstream.resp = &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body: io.NopCloser(bytes.NewReader(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": "partial summary"},
		}))),
	}
	groupID := int64(33)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: kiroNativeGPTTestModel, GroupID: &groupID, SessionContext: &SessionContext{APIKeyID: 1068}}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, UpstreamFailureIncompleteStream, failoverErr.FailureKind)
	require.True(t, failoverErr.FailoverProhibited)
	require.Empty(t, rec.Body.String())
	require.Empty(t, globalKiroResponsesHistoryStore.items)
}
