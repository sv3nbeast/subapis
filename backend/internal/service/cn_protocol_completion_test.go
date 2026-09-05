//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCNAdaptiveProtocolCompletionMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformDeepseek} {
		for _, inbound := range []string{"chat_completions", "messages", "responses"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%v", platform, inbound, stream), func(t *testing.T) {
					upstreamProtocol := APIProtocolChatCompletions
					if inbound == "messages" {
						upstreamProtocol = APIProtocolAnthropic
					}
					if inbound == "responses" && platform != PlatformZhipu {
						upstreamProtocol = APIProtocolResponses
					}
					responseBody := cnCompletionFixture(upstreamProtocol, stream)
					contentType := "application/json"
					if stream {
						contentType = "text/event-stream"
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}},
						Body: io.NopCloser(strings.NewReader(responseBody)),
					}}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					account := adaptiveProtocolTestAccount(platform, map[string]any{
						APIProtocolChatCompletions: "http://chat.example",
						APIProtocolAnthropic:       "http://anthropic.example",
						APIProtocolResponses:       "http://responses.example",
					})
					content := `"messages":[{"role":"user","content":"hello"}]`
					if inbound == "responses" {
						content = `"input":"hello"`
					}
					body := []byte(fmt.Sprintf(`{"model":"public-cn","stream":%v,"max_tokens":64,%s}`, stream, content))
					path := "/v1/" + inbound
					if inbound == "chat_completions" {
						path = "/v1/chat/completions"
					}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
					c.Request.Header.Set("Content-Type", "application/json")
					var result *OpenAIForwardResult
					var err error
					switch inbound {
					case "chat_completions":
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
					case "messages":
						result, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
					case "responses":
						result, err = svc.Forward(context.Background(), c, account, body)
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, http.StatusOK, rec.Code)
					require.Contains(t, rec.Body.String(), "hello")
					if stream {
						terminals := 0
						if inbound == "chat_completions" {
							for _, line := range strings.Split(rec.Body.String(), "\n") {
								if strings.TrimSpace(line) == "data: [DONE]" {
									terminals++
								}
							}
						}
						forEachOpenAISSEFrame(rec.Body.String(), func(eventType string, data []byte) {
							typ := effectiveOpenAISSEEventType(data, eventType)
							if inbound == "messages" && typ == "message_stop" ||
								inbound == "responses" && typ == "response.completed" {
								terminals++
							}
						})
						require.Equal(t, 1, terminals, rec.Body.String())
					} else {
						require.True(t, gjson.ValidBytes(rec.Body.Bytes()))
					}
				})
			}
		}
	}
}

func TestCNNativeAnthropicAliasPreservesMaxEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for platform, model := range map[string]string{PlatformKimi: "kimi-k3", PlatformZhipu: "glm-5.2", PlatformDeepseek: "deepseek-v4-flash"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", platform, stream), func(t *testing.T) {
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
					Header: http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:   io.NopCloser(strings.NewReader(cnCompletionFixture(APIProtocolAnthropic, true)))}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				account := adaptiveProtocolTestAccount(platform, nil)
				account.Credentials["api_protocol"] = APIProtocolAnthropic
				account.Credentials["base_url"] = "http://anthropic.example"
				account.Credentials["model_mapping"] = map[string]any{"public-alias": model}
				body := []byte(fmt.Sprintf(`{"model":"public-alias","reasoning_effort":"max","stream":%v,"messages":[{"role":"user","content":"hello"}]}`, stream))
				c := adaptiveProtocolTestContext("/v1/chat/completions", body)
				result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "max", *result.ReasoningEffort)
				require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
			})
		}
	}
}

func cnCompletionFixture(protocol string, stream bool) string {
	switch protocol {
	case APIProtocolAnthropic:
		if !stream {
			return `{"id":"msg_cn","type":"message","role":"assistant","model":"public-cn","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`
		}
		return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_cn\",\"role\":\"assistant\",\"model\":\"public-cn\",\"content\":[],\"usage\":{\"input_tokens\":10}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	case APIProtocolResponses:
		response := `{"id":"resp_cn","object":"response","status":"completed","model":"public-cn","output":[{"id":"msg_cn","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`
		if !stream {
			return response
		}
		return "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"item_id\":\"msg_cn\",\"output_index\":0,\"content_index\":0}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
	default:
		if !stream {
			return `{"id":"chat_cn","object":"chat.completion","model":"public-cn","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
		}
		return "data: {\"id\":\"chat_cn\",\"object\":\"chat.completion.chunk\",\"model\":\"public-cn\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"chat_cn\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n" +
			"data: [DONE]\n\n"
	}
}
