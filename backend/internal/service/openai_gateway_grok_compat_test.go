package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeGrokResponsesModelInputCodexHistoryDefault(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"grok-4.5","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"},{"id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done","annotations":[],"logprobs":[]}]},{"type":"function_call_output","call_id":"call_1","output":"ok"},{"type":"custom_tool_call","id":"ct_1","call_id":"call_2","name":"apply_patch","input":"*** Begin Patch"},{"type":"custom_tool_call_output","call_id":"call_2","output":"Done"}],"stream":true}`)

	normalized, changed, err := normalizeGrokResponsesModelInput(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, json.Valid(normalized))
	require.Len(t, gjson.GetBytes(normalized, "input").Array(), 4)
	require.Equal(t, "message", gjson.GetBytes(normalized, "input.0.type").String())
	require.Equal(t, "input_text", gjson.GetBytes(normalized, "input.0.content.0.type").String())
	require.False(t, gjson.GetBytes(normalized, "input.0.id").Exists())
	require.False(t, gjson.GetBytes(normalized, "input.0.status").Exists())
	require.False(t, gjson.GetBytes(normalized, "input.0.content.0.annotations").Exists())
	require.Equal(t, "function_call_output", gjson.GetBytes(normalized, "input.1.type").String())
	require.Equal(t, "function_call", gjson.GetBytes(normalized, "input.2.type").String())
	require.Equal(t, "apply_patch", gjson.GetBytes(normalized, "input.2.name").String())
	require.Equal(t, "*** Begin Patch", gjson.GetBytes(normalized, "input.2.arguments").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(normalized, "input.3.type").String())
	require.Equal(t, "Done", gjson.GetBytes(normalized, "input.3.output").String())
}

func TestNormalizeGrokResponsesModelInputWorkingInputDefault(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"grok-4.5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":true}`)
	normalized, changed, err := normalizeGrokResponsesModelInput(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, normalized)
}

func TestPatchGrokResponsesBodyNormalizesLegacyResponseFormat(t *testing.T) {
	body := []byte(`{"model":"grok","input":"json please","response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}}}}}}`)

	patched, err := patchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "response_format").Exists())
	require.Equal(t, "json_schema", gjson.GetBytes(patched, "text.format.type").String())
	require.Equal(t, "answer", gjson.GetBytes(patched, "text.format.name").String())
	require.True(t, gjson.GetBytes(patched, "text.format.strict").Bool())
	require.Equal(t, "object", gjson.GetBytes(patched, "text.format.schema.type").String())
}

func TestInjectGrokPromptCacheIdentityIsStableAndTenantIsolated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	first, _ := gin.CreateTestContext(httptest.NewRecorder())
	first.Set("api_key", &APIKey{ID: 7})
	second, _ := gin.CreateTestContext(httptest.NewRecorder())
	second.Set("api_key", &APIKey{ID: 8})
	body := []byte(`{"model":"grok-4.5","prompt_cache_key":"client-session","input":"hi"}`)

	firstBody, firstKey, err := injectGrokPromptCacheIdentity(first, body, "grok-4.5", "responses", "")
	require.NoError(t, err)
	repeatBody, repeatKey, err := injectGrokPromptCacheIdentity(first, body, "grok-4.5", "responses", "")
	require.NoError(t, err)
	_, secondKey, err := injectGrokPromptCacheIdentity(second, body, "grok-4.5", "responses", "")
	require.NoError(t, err)

	require.Equal(t, firstKey, repeatKey)
	require.Equal(t, firstBody, repeatBody)
	require.NotEqual(t, firstKey, secondKey)
	require.Equal(t, firstKey, gjson.GetBytes(firstBody, "prompt_cache_key").String())
	require.Len(t, firstKey, 36)
	require.Equal(t, byte('-'), firstKey[8])
	require.Equal(t, byte('-'), firstKey[13])
	require.Equal(t, byte('-'), firstKey[18])
	require.Equal(t, byte('-'), firstKey[23])
}

func TestGrokPromptCacheSeedUsesStableClaudeSessionAcrossTurns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Set("api_key", &APIKey{ID: 7})
	firstBody := []byte(`{"model":"grok-latest","metadata":{"user_id":"{\"device_id\":\"device-1\",\"account_uuid\":\"\",\"session_id\":\"session-1\"}"},"messages":[{"role":"user","content":"hello"}]}`)
	secondBody := []byte(`{"model":"grok-latest","metadata":{"user_id":"{\"device_id\":\"device-1\",\"account_uuid\":\"\",\"session_id\":\"session-1\"}"},"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"continue"}]}`)

	firstSeed := grokPromptCacheSeedFromRequest(c, firstBody)
	secondSeed := grokPromptCacheSeedFromRequest(c, secondBody)
	require.Equal(t, "session-1", firstSeed)
	require.Equal(t, firstSeed, secondSeed)

	responsesBody := []byte(`{"model":"grok-4.5","input":"hello","stream":true}`)
	firstUpstreamBody, firstKey, err := injectGrokPromptCacheIdentity(c, responsesBody, "grok-4.5", "messages", firstSeed)
	require.NoError(t, err)
	secondUpstreamBody, secondKey, err := injectGrokPromptCacheIdentity(c, responsesBody, "grok-4.5", "messages", secondSeed)
	require.NoError(t, err)
	require.Equal(t, firstKey, secondKey)
	require.Equal(t, firstUpstreamBody, secondUpstreamBody)

	account := &Account{ID: 54, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL}}
	request, err := buildGrokResponsesRequest(context.Background(), c, account, firstUpstreamBody, "token")
	require.NoError(t, err)
	require.Equal(t, firstKey, request.Header.Get("x-grok-conv-id"))
	require.Equal(t, firstKey, request.Header.Get("x-grok-conversation-id"))
	require.Equal(t, firstKey, gjson.GetBytes(firstUpstreamBody, "prompt_cache_key").String())
}

func TestGrokPromptCacheSeedUsesStableContentFallbackForOpenCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("api_key", &APIKey{ID: 8})
	firstBody := []byte(`{"model":"grok-4.5","messages":[{"role":"system","content":"rules"},{"role":"user","content":"hello"}]}`)
	secondBody := []byte(`{"model":"grok-4.5","messages":[{"role":"system","content":"rules"},{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"continue"}]}`)

	firstSeed := grokPromptCacheSeedFromRequest(c, firstBody)
	secondSeed := grokPromptCacheSeedFromRequest(c, secondBody)
	require.NotEmpty(t, firstSeed)
	require.Equal(t, firstSeed, secondSeed)
	require.True(t, strings.HasPrefix(firstSeed, "grok-content-"))

	account := &Account{ID: 55, Platform: PlatformGrok, Type: AccountTypeOAuth}
	firstMetadata := grokCLIRequestMetadata(c, account, firstBody, "grok-4.5")
	secondMetadata := grokCLIRequestMetadata(c, account, secondBody, "grok-4.5")
	require.NotEmpty(t, firstMetadata.ConversationID)
	require.Equal(t, firstMetadata.ConversationID, secondMetadata.ConversationID)
}

func TestGrokPromptCacheSeedPrefersClaudeSessionHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "header-session")
	body := []byte(`{"prompt_cache_key":"body-session","metadata":{"session_id":"metadata-session"}}`)

	require.Equal(t, "header-session", grokPromptCacheSeedFromRequest(c, body))
}

func TestGrokCompactPromptCacheIdentityPreservesPreNormalizedSeed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	compactContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	compactContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	compactContext.Set("api_key", &APIKey{ID: 7})
	compactContext.Set(openAICompactSessionSeedKey, "client-session")
	compactInput := []byte(`{"model":"grok-4.5","input":[{"type":"compaction_trigger"}]}`)

	continuationContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	continuationContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	continuationContext.Set("api_key", &APIKey{ID: 7})
	continuationInput := []byte(`{"model":"grok-4.5","prompt_cache_key":"client-session","input":[{"type":"compaction","encrypted_content":"opaque"}]}`)

	compactBody, compactIdentity, err := injectGrokPromptCacheIdentity(compactContext, compactInput, "grok-4.5", "responses", "")
	require.NoError(t, err)
	continuationBody, continuationIdentity, err := injectGrokPromptCacheIdentity(continuationContext, continuationInput, "grok-4.5", "responses", "")
	require.NoError(t, err)
	require.NotEmpty(t, compactIdentity)
	require.Equal(t, compactIdentity, continuationIdentity)
	require.Equal(t, compactIdentity, gjson.GetBytes(compactBody, "prompt_cache_key").String())
	require.Equal(t, continuationIdentity, gjson.GetBytes(continuationBody, "prompt_cache_key").String())

	account := &Account{ID: 54, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"base_url": xai.DefaultCLIBaseURL}}
	compactRequest, err := buildGrokResponsesRequest(context.Background(), compactContext, account, compactBody, "token")
	require.NoError(t, err)
	continuationRequest, err := buildGrokResponsesRequest(context.Background(), continuationContext, account, continuationBody, "token")
	require.NoError(t, err)
	require.Equal(t, compactIdentity, compactRequest.Header.Get("x-grok-conv-id"))
	require.Equal(t, compactRequest.Header.Get("x-grok-conv-id"), continuationRequest.Header.Get("x-grok-conv-id"))
}

func TestForwardGrokResponsesCodexModelInputCompatRetryDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"},{"type":"custom_tool_call","id":"ct_1","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch"},{"type":"custom_tool_call_output","call_id":"call_1","output":"Done"},{"role":"user","content":[{"type":"input_text","text":"continue"}]}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "Codex Desktop/test")

	account := &Account{
		ID: 520, Name: "grok", Platform: PlatformGrok, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"base_url":     xai.DefaultCLIBaseURL,
		},
	}
	modelInputError := `{"error":"Failed to deserialize the JSON body into the target type: data did not match any variant of untagged enum ModelInput"}`
	completed := strings.Join([]string{
		`data: {"type":"response.output_text.delta","sequence_number":0,"delta":"ok"}`,
		"",
		`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_retry","model":"grok-4.5","usage":{"input_tokens":7,"output_tokens":2}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(modelInputError))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "Xai-Request-Id": []string{"xai-retry"}}, Body: io.NopCloser(strings.NewReader(completed))},
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.forwardGrokResponses(context.Background(), c, account, body, "grok", true, time.Now())
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "reasoning", gjson.GetBytes(upstream.bodies[0], "input.0.type").String())
	require.Len(t, gjson.GetBytes(upstream.bodies[1], "input").Array(), 3)
	require.Equal(t, "function_call", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
	require.Equal(t, "apply_patch", gjson.GetBytes(upstream.bodies[1], "input.0.name").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.bodies[1], "input.1.type").String())
	require.Equal(t, "message", gjson.GetBytes(upstream.bodies[1], "input.2.type").String())
	require.Equal(t, "resp_retry", result.ResponseID)
	require.Equal(t, "xai-retry", result.RequestID)
	require.Contains(t, recorder.Body.String(), "response.output_text.delta")
}

func TestForwardGrokResponsesDoesNotRetryUnrelated422Default(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"grok","input":[{"type":"reasoning","id":"rs_1"},{"role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	account := &Account{ID: 521, Name: "grok", Platform: PlatformGrok, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"access_token": "access-token", "base_url": xai.DefaultCLIBaseURL}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"invalid tool schema"}`))}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	_, err := svc.forwardGrokResponses(context.Background(), c, account, body, "grok", true, time.Now())
	require.Error(t, err)
	require.Len(t, upstream.bodies, 1)
}
