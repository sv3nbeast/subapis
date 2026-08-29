package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/encoding/protowire"
)

const nianzsXMLInvokeBridgeUA = "claude-cli/2.1.246 (external, claude-desktop-3p, agent-sdk/0.3.246)"

func nianzsXMLInvokeSSEPayloads(wire string) []gjson.Result {
	var payloads []gjson.Result
	for _, line := range strings.Split(wire, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload != "" && payload != "[DONE]" && gjson.Valid(payload) {
			payloads = append(payloads, gjson.Parse(payload))
		}
	}
	return payloads
}

func nianzsXMLInvokeEventStreamResponse(t *testing.T) *http.Response {
	t.Helper()
	stream := bytes.NewBuffer(nil)
	for _, fragment := range []string{
		"commit 3 did not complete.\n\ncall\n<in",
		`voke name="Bash"><parameter name="description">Commit admin refactor</parameter>`,
		`<parameter name="command">git status --short &amp;&amp; git commit -m &quot;refactor&quot;</parameter></invoke>`,
	} {
		_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": fragment},
		}))
	}
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{"uncachedInputTokens": 64, "outputTokens": 32},
		},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
}

func nianzsXMLInvokeMessagesBody(stream bool) []byte {
	return []byte(fmt.Sprintf(`{
		"model":"claude-sonnet-5",
		"max_tokens":1024,
		"stream":%t,
		"tools":[{
			"name":"Bash",
			"description":"Run a shell command",
			"input_schema":{
				"type":"object",
				"properties":{
					"command":{"type":"string"},
					"description":{"type":"string"}
				},
				"required":["command"]
			}
		}],
		"messages":[{"role":"user","content":"Commit the current changes."}]
	}`, stream))
}

func nianzsXMLInvokeThinkingMessagesBody(stream bool) []byte {
	body := nianzsXMLInvokeMessagesBody(stream)
	body = bytes.Replace(body, []byte(`"max_tokens":1024,`), []byte(`"max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":4096},"output_config":{"effort":"max"},`), 1)
	return body
}

func nianzsXMLInvokeProviderThinkingSignature() string {
	appendVarint := func(dst []byte, field protowire.Number, value uint64) []byte {
		dst = protowire.AppendTag(dst, field, protowire.VarintType)
		return protowire.AppendVarint(dst, value)
	}
	appendBytes := func(dst []byte, field protowire.Number, value []byte) []byte {
		dst = protowire.AppendTag(dst, field, protowire.BytesType)
		return protowire.AppendBytes(dst, value)
	}
	repeated := func(value byte, count int) []byte {
		return bytes.Repeat([]byte{value}, count)
	}

	channel := appendVarint(nil, 1, 16)
	channel = appendVarint(channel, 2, 1)
	channel = appendVarint(channel, 3, 2)
	channel = appendBytes(channel, 5, repeated(0x51, 64))
	channel = appendBytes(channel, 6, []byte("claude-quince"))
	channel = appendVarint(channel, 7, 0)
	channel = appendBytes(channel, 8, []byte("thinking"))
	channel = appendBytes(channel, 11, []byte("015911059195"))

	inner := appendBytes(nil, 1, channel)
	inner = appendBytes(inner, 2, repeated(0x52, 12))
	inner = appendBytes(inner, 3, repeated(0x53, 12))
	inner = appendBytes(inner, 4, repeated(0x54, 48))
	inner = appendBytes(inner, 5, repeated(0x55, 128))
	body := appendBytes(nil, 2, inner)
	body = appendVarint(body, 3, 1)
	return base64.StdEncoding.EncodeToString(body)
}

func nianzsXMLInvokeThinkingEventStreamResponse(t *testing.T) *http.Response {
	t.Helper()
	stream := bytes.NewBuffer(nil)
	for _, fragment := range []string{
		"commit 3 did not complete. Need inspect status.\n\ncount\n<in",
		`voke name="Bash"><parameter name="description">Commit admin refactor</parameter>`,
		`<parameter name="command">git status --short &amp;&amp; git commit -m &quot;refactor&quot;</parameter></invoke>`,
	} {
		_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
			"reasoningContentEvent": map[string]any{"text": fragment},
		}))
	}
	_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"signature": nianzsXMLInvokeProviderThinkingSignature()},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{"uncachedInputTokens": 64, "outputTokens": 32},
		},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
}

func TestNianzsMessagesBridgesXMLInvokeForDesktopAgentClients(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			body := nianzsXMLInvokeMessagesBody(stream)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
			require.NoError(t, err)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
			c.Request.Header.Set("User-Agent", nianzsXMLInvokeBridgeUA)
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nianzsXMLInvokeEventStreamResponse(t))
			ctx := SetClaudeCodeUserAgent(SetClaudeCodeClient(context.Background(), true), nianzsXMLInvokeBridgeUA)

			result, forwardErr := svc.Forward(ctx, c, account, parsed)

			require.NoError(t, forwardErr)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1, "XML normalization must not replay or switch accounts")
			if stream {
				wire := recorder.Body.String()
				var visible strings.Builder
				toolBlocks := make([]gjson.Result, 0)
				for _, start := range nianzsSSEPayloadsByType(wire, "content_block_start") {
					if start.Get("content_block.type").String() == "tool_use" {
						toolBlocks = append(toolBlocks, start)
					}
				}
				for _, delta := range nianzsSSEPayloadsByType(wire, "content_block_delta") {
					if delta.Get("delta.type").String() == "text_delta" {
						visible.WriteString(delta.Get("delta.text").String())
					}
				}
				require.NotContains(t, visible.String(), "<invoke")
				require.NotContains(t, visible.String(), "<parameter")
				require.Len(t, toolBlocks, 1)
				require.Equal(t, "Bash", toolBlocks[0].Get("content_block.name").String())
				require.Contains(t, wire, `"stop_reason":"tool_use"`)
				require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
				var input strings.Builder
				for _, delta := range nianzsSSEPayloadsByType(wire, "content_block_delta") {
					if delta.Get("delta.type").String() == "input_json_delta" {
						input.WriteString(delta.Get("delta.partial_json").String())
					}
				}
				require.JSONEq(t, `{"command":"git status --short && git commit -m \"refactor\"","description":"Commit admin refactor"}`, input.String())
				return
			}

			content := gjson.Get(recorder.Body.String(), "content").Array()
			require.Len(t, content, 2)
			require.NotContains(t, content[0].Get("text").String(), "<invoke")
			require.NotContains(t, content[0].Get("text").String(), "<parameter")
			require.Equal(t, "text", content[0].Get("type").String())
			require.Contains(t, content[0].Get("text").String(), "commit 3 did not complete")
			require.NotContains(t, content[0].Get("text").String(), "call")
			require.Equal(t, "tool_use", content[1].Get("type").String())
			require.Equal(t, "Bash", content[1].Get("name").String())
			require.Equal(t, "git status --short && git commit -m \"refactor\"", content[1].Get("input.command").String())
			require.Equal(t, "tool_use", gjson.Get(recorder.Body.String(), "stop_reason").String())
		})
	}
}

func TestNianzsMessagesDoesNotBridgeXMLInvokeForPlainClaudeCLI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const plainUA = "claude-cli/2.1.251 (external, cli)"
	body := nianzsXMLInvokeMessagesBody(false)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("User-Agent", plainUA)
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nianzsXMLInvokeEventStreamResponse(t))
	ctx := SetClaudeCodeUserAgent(SetClaudeCodeClient(context.Background(), true), plainUA)

	result, forwardErr := svc.Forward(ctx, c, account, parsed)

	require.NoError(t, forwardErr)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 1)
	require.Contains(t, gjson.Get(recorder.Body.String(), "content.0.text").String(), "<invoke")
	require.Empty(t, gjson.Get(recorder.Body.String(), "content.#(type==\"tool_use\")").Array())
	require.Equal(t, "end_turn", gjson.Get(recorder.Body.String(), "stop_reason").String())
}

func TestNianzsMessagesBridgesXMLInvokeOutsideProviderThinking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			body := nianzsXMLInvokeThinkingMessagesBody(stream)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
			c.Request.Header.Set("User-Agent", nianzsXMLInvokeBridgeUA)
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nianzsXMLInvokeThinkingEventStreamResponse(t))
			ctx := SetClaudeCodeUserAgent(SetClaudeCodeClient(context.Background(), true), nianzsXMLInvokeBridgeUA)

			result, forwardErr := svc.Forward(ctx, c, account, parsed)

			require.NoError(t, forwardErr)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1, "thinking XML normalization must not replay or switch accounts")
			if stream {
				wire := recorder.Body.String()
				var thinking strings.Builder
				toolBlocks := make([]gjson.Result, 0)
				for _, start := range nianzsSSEPayloadsByType(wire, "content_block_start") {
					if start.Get("content_block.type").String() == "tool_use" {
						toolBlocks = append(toolBlocks, start)
					}
				}
				for _, delta := range nianzsSSEPayloadsByType(wire, "content_block_delta") {
					if delta.Get("delta.type").String() == "thinking_delta" {
						thinking.WriteString(delta.Get("delta.thinking").String())
					}
				}
				require.NotContains(t, thinking.String(), "<invoke")
				require.NotContains(t, thinking.String(), "<parameter")
				require.Len(t, toolBlocks, 1)
				require.Equal(t, "Bash", toolBlocks[0].Get("content_block.name").String())
				require.Contains(t, wire, `"stop_reason":"tool_use"`)
				require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
				var signature string
				signatureIndex, thinkingStopIndex, toolStartIndex := -1, -1, -1
				thinkingBlockIndex := int64(-1)
				for eventIndex, event := range nianzsXMLInvokeSSEPayloads(wire) {
					if event.Get("type").String() == "content_block_start" && event.Get("content_block.type").String() == "thinking" {
						thinkingBlockIndex = event.Get("index").Int()
					}
					if event.Get("type").String() == "content_block_start" && event.Get("content_block.type").String() == "tool_use" {
						toolStartIndex = eventIndex
					}
					if event.Get("type").String() == "content_block_stop" && event.Get("index").Int() == thinkingBlockIndex {
						thinkingStopIndex = eventIndex
					}
					delta := event
					if delta.Get("delta.type").String() == "signature_delta" {
						signature = delta.Get("delta.signature").String()
						signatureIndex = eventIndex
					}
				}
				require.Equal(t, nianzsXMLInvokeProviderThinkingSignature(), signature)
				require.NotEqual(t, -1, signatureIndex)
				require.Less(t, signatureIndex, thinkingStopIndex)
				require.Less(t, thinkingStopIndex, toolStartIndex)
				return
			}

			response := gjson.Parse(recorder.Body.String())
			var thinking string
			var toolUses []gjson.Result
			for _, block := range response.Get("content").Array() {
				switch block.Get("type").String() {
				case "thinking":
					thinking += block.Get("thinking").String()
				case "tool_use":
					toolUses = append(toolUses, block)
				}
			}
			require.NotContains(t, thinking, "<invoke")
			require.NotContains(t, thinking, "<parameter")
			require.Len(t, toolUses, 1)
			require.Equal(t, nianzsXMLInvokeProviderThinkingSignature(), response.Get("content.0.signature").String())
			require.Equal(t, "Bash", toolUses[0].Get("name").String())
			require.Equal(t, "git status --short && git commit -m \"refactor\"", toolUses[0].Get("input.command").String())
			require.Equal(t, "tool_use", response.Get("stop_reason").String())
		})
	}
}

func TestNianzsMessagesThinkingXMLInvokeBridgeIsModelAndEndpointAgnostic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"claude-sonnet-5", "claude-opus-5", "claude-opus-4-8"} {
		model := model
		for _, endpointMode := range []string{KiroEndpointModeQ, KiroEndpointModeKRS} {
			endpointMode := endpointMode
			t.Run(model+"/"+endpointMode, func(t *testing.T) {
				body := bytes.Replace(nianzsXMLInvokeThinkingMessagesBody(true), []byte(`"model":"claude-sonnet-5"`), []byte(`"model":"`+model+`"`), 1)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
				require.NoError(t, err)
				groupID := int64(29)
				parsed.GroupID = &groupID
				parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: endpointMode}

				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
				c.Request.Header.Set("User-Agent", nianzsXMLInvokeBridgeUA)
				svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nianzsXMLInvokeThinkingEventStreamResponse(t))
				ctx := SetClaudeCodeUserAgent(SetClaudeCodeClient(context.Background(), true), nianzsXMLInvokeBridgeUA)

				result, forwardErr := svc.Forward(ctx, c, account, parsed)

				require.NoError(t, forwardErr)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				wire := recorder.Body.String()
				var thinking strings.Builder
				var toolUses int
				for _, event := range nianzsXMLInvokeSSEPayloads(wire) {
					if event.Get("delta.type").String() == "thinking_delta" {
						thinking.WriteString(event.Get("delta.thinking").String())
					}
					if event.Get("content_block.type").String() == "tool_use" {
						toolUses++
					}
				}
				require.NotContains(t, thinking.String(), "<invoke")
				require.Equal(t, 1, toolUses)
				require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
			})
		}
	}
}
