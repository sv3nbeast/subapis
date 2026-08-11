package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const nianzsStrictOutputTestBody = `{
  "model":"claude-opus-5",
  "max_tokens":256,
  "stream":true,
  "messages":[{"role":"user","content":"return the structured answer"}],
  "output_config":{"format":{"type":"json_schema","schema":{
    "type":"object",
    "properties":{"desc":{"type":"string"},"meta":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}},
    "required":["desc","meta"],
    "additionalProperties":false
  }}}
}`

func TestNianzsNormalizeStructuredOutputJSONStripsUndeclaredFieldsRecursively(t *testing.T) {
	schema, ok := nianzsKiroStructuredOutputSchema([]byte(nianzsStrictOutputTestBody))
	require.True(t, ok)

	normalized, ok := nianzsNormalizeStructuredOutputJSON(
		`{"desc":"ok","description":"extra","meta":{"ok":true,"leak":"remove"}}`,
		schema,
	)
	require.True(t, ok)
	require.JSONEq(t, `{"desc":"ok","meta":{"ok":true}}`, normalized)
}

func TestNianzsNormalizeStructuredOutputJSONFailsClosedWhenRequiredFieldMissing(t *testing.T) {
	schema, ok := nianzsKiroStructuredOutputSchema([]byte(nianzsStrictOutputTestBody))
	require.True(t, ok)

	_, ok = nianzsNormalizeStructuredOutputJSON(`{"desc":"ok","extra":true}`, schema)
	require.False(t, ok)
}

func TestNianzsNormalizeStructuredOutputSSEJoinsAndProjectsTextDeltas(t *testing.T) {
	schema, ok := nianzsKiroStructuredOutputSchema([]byte(nianzsStrictOutputTestBody))
	require.True(t, ok)
	wire := []byte(strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"{\\\"desc\\\":\\\"ok\\\",\"}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"\\\"description\\\":\\\"extra\\\",\\\"meta\\\":{\\\"ok\\\":true,\\\"leak\\\":1}}\"}}",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}",
	}, "\n\n") + "\n\n")

	normalized := string(nianzsNormalizeStructuredOutputSSE(wire, schema))
	require.Equal(t, 2, strings.Count(normalized, `"type":"text_delta"`))
	require.Contains(t, normalized, `\"desc\":\"ok\"`)
	require.Contains(t, normalized, `\"meta\":{\"ok\":true}`)
	require.NotContains(t, normalized, "description")
	require.NotContains(t, normalized, "leak")
	require.Equal(t, 1, strings.Count(normalized, "event: message_stop"))
}

func TestNianzsMessagesStrictStructuredOutputProjectsPlainTextEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(nianzsStrictOutputTestBody)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": `{"desc":"ok","description":"extra","meta":{"ok":true,"leak":"remove"}}`,
		},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 15, "outputTokens": 10}},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	wire := recorder.Body.String()
	require.Contains(t, wire, `\"desc\":\"ok\"`)
	require.Contains(t, wire, `\"meta\":{\"ok\":true}`)
	require.NotContains(t, wire, "description")
	require.NotContains(t, wire, "leak")
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
}

func TestNianzsNormalizeStructuredOutputResponseJSONProjectsNonStream(t *testing.T) {
	schema, ok := nianzsKiroStructuredOutputSchema([]byte(nianzsStrictOutputTestBody))
	require.True(t, ok)
	response := []byte(`{"id":"msg_1","type":"message","content":[{"type":"text","text":"{\"desc\":\"ok\",\"description\":\"extra\",\"meta\":{\"ok\":true,\"leak\":1}}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":5}}`)

	normalized := nianzsNormalizeStructuredOutputResponseJSON(response, schema)
	text := gjson.GetBytes(normalized, "content.0.text").String()
	require.JSONEq(t, `{"desc":"ok","meta":{"ok":true}}`, text)
}

func TestNianzsStructuredObjectProjectorEmitsBeforeObjectTerminal(t *testing.T) {
	schema, ok := nianzsKiroStructuredOutputSchema([]byte(nianzsStrictOutputTestBody))
	require.True(t, ok)
	projector, ok := newNianzsStructuredObjectProjector(schema)
	require.True(t, ok)

	first, err := projector.Feed(`{"desc":"ready",`)
	require.NoError(t, err)
	require.Equal(t, `{"desc":"ready"`, first)
	require.False(t, projector.done)

	second, err := projector.Feed(`"description":"drop","meta":{"ok":true,"leak":1}}`)
	require.NoError(t, err)
	require.Equal(t, `,"meta":{"ok":true}}`, second)
	require.True(t, projector.done)
}

func TestNianzsStructuredOutputSSEWriterWritesFirstMemberBeforeTerminalFrame(t *testing.T) {
	schema, ok := nianzsKiroStructuredOutputSchema([]byte(nianzsStrictOutputTestBody))
	require.True(t, ok)
	var out bytes.Buffer
	writer, ok := newNianzsStructuredOutputSSEWriter(&out, schema)
	require.True(t, ok)

	firstFrames := strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"content\":[]}}",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"{\\\"desc\\\":\\\"ready\\\",\"}}",
	}, "\n\n") + "\n\n"
	_, err := writer.Write([]byte(firstFrames))
	require.NoError(t, err)
	require.Contains(t, out.String(), `"text":"{\"desc\":\"ready\""`)
	require.NotContains(t, out.String(), "message_stop")

	terminalFrames := strings.Join([]string{
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"\\\"meta\\\":{\\\"ok\\\":true}}\"}}",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}",
	}, "\n\n") + "\n\n"
	_, err = writer.Write([]byte(terminalFrames))
	require.NoError(t, err)
	require.NoError(t, writer.Finish())
	require.Contains(t, out.String(), "message_stop")
}

type nianzsSemanticWriteGate struct {
	gin.ResponseWriter
	marker  []byte
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *nianzsSemanticWriteGate) Write(payload []byte) (int, error) {
	if bytes.Contains(payload, w.marker) {
		w.once.Do(func() { close(w.reached) })
		<-w.release
	}
	return w.ResponseWriter.Write(payload)
}

func TestNianzsMessagesStrictStructuredOutputFlushesCompletedMemberBeforeUpstreamTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(nianzsStrictOutputTestBody)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	upstreamBody, upstreamWriter := io.Pipe()
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       upstreamBody,
	}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	semanticReached := make(chan struct{})
	releaseSemantic := make(chan struct{})
	c.Writer = &nianzsSemanticWriteGate{
		ResponseWriter: c.Writer,
		marker:         []byte(`desc`),
		reached:        semanticReached,
		release:        releaseSemantic,
	}

	allowTerminal := make(chan struct{})
	go func() {
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": `{"desc":"ready","description":"extra-field-long-enough-for-thinking-tag-lookbehind",`},
		}))
		<-allowTerminal
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": `"description":"drop","meta":{"ok":true,"leak":1}}`},
		}))
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
			"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 15, "outputTokens": 10}},
		}))
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
			"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
		}))
		_ = upstreamWriter.Close()
	}()

	type outcome struct {
		result *ForwardResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, forwardErr := svc.Forward(context.Background(), c, account, parsed)
		done <- outcome{result: result, err: forwardErr}
	}()

	select {
	case <-semanticReached:
		// A schema-valid completed member reached the client while the Kiro
		// terminal event was still gated upstream.
	case <-time.After(2 * time.Second):
		t.Fatal("strict structured output waited for the upstream terminal event")
	}
	close(releaseSemantic)
	close(allowTerminal)

	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for strict structured output completion")
	}
	wire := recorder.Body.String()
	require.Contains(t, wire, `\"desc\":\"ready\"`)
	require.Contains(t, wire, `\"meta\":{\"ok\":true}`)
	require.NotContains(t, wire, "description")
	require.NotContains(t, wire, "leak")
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
}
