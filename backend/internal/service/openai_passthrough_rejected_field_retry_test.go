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

// Regression for production request 3e565cde-a245-4c53-aa42-1b1bddb48998:
// a non-Codex client (the channel monitor, Go-http-client) sent a Responses
// request with max_output_tokens to an OAuth account with openai_passthrough
// enabled. Passthrough forwarded the field verbatim, the ChatGPT backend
// answered 400 {"detail":"Unsupported parameter: max_output_tokens"}, and the
// gateway returned that 400 instead of applying the same bounded rejected-field
// retry the non-passthrough path already performs.

const passthroughRejectedFieldCompletedSSE = "event: response.created\n" +
	`data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_pt_1","object":"response","status":"in_progress","model":"gpt-5.6-terra","output":[]}}` + "\n\n" +
	"event: response.output_item.added\n" +
	`data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_pt_1","type":"message","status":"in_progress","role":"assistant","content":[]}}` + "\n\n" +
	"event: response.output_text.delta\n" +
	`data: {"type":"response.output_text.delta","sequence_number":2,"item_id":"msg_pt_1","output_index":0,"content_index":0,"delta":"4"}` + "\n\n" +
	"event: response.output_item.done\n" +
	`data: {"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"msg_pt_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"4","annotations":[]}]}}` + "\n\n" +
	"event: response.completed\n" +
	`data: {"type":"response.completed","sequence_number":4,"response":{"id":"resp_pt_1","object":"response","status":"completed","model":"gpt-5.6-terra","output":[{"id":"msg_pt_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"4","annotations":[]}]}],"usage":{"input_tokens":76,"input_tokens_details":{"cached_tokens":0},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":81}}}` + "\n\n"

const passthroughMonitorProbeBody = `{"model":"gpt-5.6-terra","instructions":"You are a channel health-check endpoint. Answer the arithmetic challenge exactly and briefly.","input":"What is 2+2? Reply with the number only.","max_output_tokens":64,"stream":false}`

func newPassthroughRejectedFieldAccount() *Account {
	return &Account{
		ID: 2613, Name: "openai-oauth-passthrough", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 10,
		Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-account"},
		Extra:       map[string]any{"openai_passthrough": true, "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff},
		Status:      StatusActive, Schedulable: true,
	}
}

func newPassthroughRejectedFieldContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "Go-http-client/1.1")
	return c, rec
}

func newPassthroughRejectedFieldService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
}

// newPassthroughUpstreamResponse builds a response with canonical header keys,
// matching what net/http hands the gateway in production.
func newPassthroughUpstreamResponse(status int, contentType, requestID, body string) *http.Response {
	header := http.Header{}
	header.Set("Content-Type", contentType)
	header.Set("x-request-id", requestID)
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func passthroughMaxOutputTokensRejection() *http.Response {
	return newPassthroughUpstreamResponse(http.StatusBadRequest, "application/json", "rid_pt_reject",
		`{"detail":"Unsupported parameter: max_output_tokens"}`)
}

func passthroughCompletedSSEResponse() *http.Response {
	return newPassthroughUpstreamResponse(http.StatusOK, "text/event-stream", "rid_pt_ok", passthroughRejectedFieldCompletedSSE)
}

func passthroughOpsEvents(t *testing.T, c *gin.Context) []*OpsUpstreamErrorEvent {
	t.Helper()
	raw, ok := c.Get(OpsUpstreamErrorsKey)
	if !ok {
		return nil
	}
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	return events
}

// The exact production shape: non-stream monitor probe, passthrough account,
// upstream rejects max_output_tokens. The gateway must drop only that field,
// retry once on the same account, and hand the client one JSON document whose
// output text the monitor can validate.
func TestOpenAIPassthroughRetriesExplicitlyRejectedMaxOutputTokensForNonStreamClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(passthroughMonitorProbeBody)
	c, rec := newPassthroughRejectedFieldContext(t, body)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		passthroughMaxOutputTokensRejection(),
		passthroughCompletedSSEResponse(),
	}}
	svc := newPassthroughRejectedFieldService(upstream)

	result, err := svc.Forward(context.Background(), c, newPassthroughRejectedFieldAccount(), body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2, "exactly one same-account retry")
	require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstream.requests[0].URL.String())
	require.Equal(t, upstream.requests[0].URL.String(), upstream.requests[1].URL.String())

	first, second := upstream.bodies[0], upstream.bodies[1]
	require.Equal(t, int64(64), gjson.GetBytes(first, "max_output_tokens").Int(), "passthrough forwards the client field verbatim first")
	require.True(t, gjson.GetBytes(first, "stream").Bool(), "upstream transport is always SSE for the ChatGPT backend")
	require.False(t, gjson.GetBytes(second, "max_output_tokens").Exists(), "retry drops only the rejected field")
	require.Equal(t, "gpt-5.6-terra", gjson.GetBytes(second, "model").String())
	require.Equal(t, gjson.GetBytes(first, "instructions").String(), gjson.GetBytes(second, "instructions").String())
	require.Equal(t, gjson.GetBytes(first, "input").Raw, gjson.GetBytes(second, "input").Raw)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "completed", gjson.Get(rec.Body.String(), "status").String(), "body=%s", rec.Body.String())
	require.Equal(t, "4", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
	require.NotContains(t, rec.Body.String(), "event: response.completed")
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json", "non-stream client receives aggregated JSON, not SSE")
	require.False(t, result.Stream)
	require.Equal(t, 76, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)

	events := passthroughOpsEvents(t, c)
	require.Len(t, events, 1, "the rejected attempt stays visible in ops telemetry")
	require.Equal(t, "retry", events[0].Kind)
	require.Equal(t, "rejected_field", events[0].Reason)
	require.True(t, events[0].Passthrough)
	require.Equal(t, http.StatusBadRequest, events[0].UpstreamStatusCode)
	require.Equal(t, "rid_pt_reject", events[0].UpstreamRequestID)
	require.Contains(t, events[0].Message, "max_output_tokens")
	require.Equal(t, int64(2613), events[0].AccountID)
}

// A streaming client hits the same rejection: retry before any client bytes,
// then stream the second attempt through untouched.
func TestOpenAIPassthroughRetriesExplicitlyRejectedFieldForStreamingClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-terra","instructions":"stream test","input":"What is 2+2?","max_output_tokens":64,"stream":true}`)
	c, rec := newPassthroughRejectedFieldContext(t, body)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		passthroughMaxOutputTokensRejection(),
		passthroughCompletedSSEResponse(),
	}}
	svc := newPassthroughRejectedFieldService(upstream)

	result, err := svc.Forward(context.Background(), c, newPassthroughRejectedFieldAccount(), body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.True(t, gjson.GetBytes(upstream.bodies[0], "max_output_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "max_output_tokens").Exists())

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, rec.Body.String(), "event: response.completed")
	require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.completed"`), "exactly one terminal event reaches the client")
	require.Contains(t, rec.Body.String(), `"text":"4"`)
	require.True(t, result.Stream)

	events := passthroughOpsEvents(t, c)
	require.Len(t, events, 1)
	require.Equal(t, "retry", events[0].Kind)
	require.Equal(t, "rejected_field", events[0].Reason)
	require.True(t, events[0].Passthrough)
}

// Control: a 400 that is not an explicit field rejection must keep the existing
// passthrough behavior (no retry, upstream status surfaced, http_error event).
func TestOpenAIPassthroughDoesNotRetryUnrelatedBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(passthroughMonitorProbeBody)
	c, rec := newPassthroughRejectedFieldContext(t, body)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newPassthroughUpstreamResponse(http.StatusBadRequest, "application/json", "rid_pt_system",
			`{"detail":"System messages are not allowed"}`),
	}}
	svc := newPassthroughRejectedFieldService(upstream)

	result, err := svc.Forward(context.Background(), c, newPassthroughRejectedFieldAccount(), body)

	require.Error(t, err)
	require.Nil(t, result)
	require.Len(t, upstream.bodies, 1, "unrelated 400 is not retried")
	require.Equal(t, http.StatusBadRequest, rec.Code)

	events := passthroughOpsEvents(t, c)
	require.Len(t, events, 1)
	require.Equal(t, "http_error", events[0].Kind)
	require.True(t, events[0].Passthrough)
	require.Contains(t, events[0].Message, "System messages are not allowed")
}

// The retry is bounded by the request body actually changing: once the rejected
// field is gone a repeated identical rejection cannot loop, and the final 400
// is surfaced with both attempts recorded.
func TestOpenAIPassthroughRejectedFieldRetryIsBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(passthroughMonitorProbeBody)
	c, rec := newPassthroughRejectedFieldContext(t, body)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		passthroughMaxOutputTokensRejection(),
		passthroughMaxOutputTokensRejection(),
	}}
	svc := newPassthroughRejectedFieldService(upstream)

	result, err := svc.Forward(context.Background(), c, newPassthroughRejectedFieldAccount(), body)

	require.Error(t, err)
	require.Nil(t, result)
	require.Len(t, upstream.bodies, 2, "one retry, then stop")
	require.False(t, gjson.GetBytes(upstream.bodies[1], "max_output_tokens").Exists())
	require.Equal(t, http.StatusBadRequest, rec.Code)

	events := passthroughOpsEvents(t, c)
	require.Len(t, events, 2)
	require.Equal(t, "retry", events[0].Kind)
	require.Equal(t, "rejected_field", events[0].Reason)
	require.Equal(t, "http_error", events[1].Kind)
	require.True(t, events[1].Passthrough)
}

// Independent of any rejection: a non-stream client on a passthrough account
// must receive one JSON document even though the ChatGPT backend is always
// driven over SSE.
func TestOpenAIPassthroughAggregatesSSEForNonStreamClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-terra","instructions":"json test","input":"What is 2+2?","stream":false}`)
	c, rec := newPassthroughRejectedFieldContext(t, body)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{passthroughCompletedSSEResponse()}}
	svc := newPassthroughRejectedFieldService(upstream)

	result, err := svc.Forward(context.Background(), c, newPassthroughRejectedFieldAccount(), body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.True(t, gjson.GetBytes(upstream.bodies[0], "stream").Bool())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "4", gjson.Get(rec.Body.String(), "output.0.content.0.text").String(), "body=%s", rec.Body.String())
	require.Equal(t, "resp_pt_1", gjson.Get(rec.Body.String(), "id").String())
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.False(t, result.Stream)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Empty(t, passthroughOpsEvents(t, c))
}

// Guard against over-aggregation: a streaming client keeps raw SSE passthrough.
func TestOpenAIPassthroughKeepsSSEForStreamingClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-terra","instructions":"sse test","input":"What is 2+2?","stream":true}`)
	c, rec := newPassthroughRejectedFieldContext(t, body)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{passthroughCompletedSSEResponse()}}
	svc := newPassthroughRejectedFieldService(upstream)

	result, err := svc.Forward(context.Background(), c, newPassthroughRejectedFieldAccount(), body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, rec.Body.String(), "event: response.completed")
	require.True(t, result.Stream)
}

// Sibling-path probe: a non-stream client on a regular (non-passthrough) OAuth
// account also has its SSE folded into JSON; the document must be labelled as
// JSON even though the upstream header said text/event-stream.
func TestOpenAIOAuthNonStreamClientReceivesJSONContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-terra","instructions":"json test","input":"What is 2+2?","stream":false}`)
	c, rec := newPassthroughRejectedFieldContext(t, body)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{passthroughCompletedSSEResponse()}}
	svc := newPassthroughRejectedFieldService(upstream)
	account := newPassthroughRejectedFieldAccount()
	account.Extra["openai_passthrough"] = false

	result, err := svc.Forward(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "4", gjson.Get(rec.Body.String(), "output.0.content.0.text").String(), "body=%s", rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.False(t, result.Stream)
}
