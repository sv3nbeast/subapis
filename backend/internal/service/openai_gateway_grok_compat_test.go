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
