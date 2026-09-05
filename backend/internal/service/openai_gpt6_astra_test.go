package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAstraModelContract(t *testing.T) {
	require.True(t, isOpenAIOAuthServableModel("gpt-6-astra"))
	require.Contains(t, openai.DefaultModelIDs(), "gpt-6-astra")
	for _, m := range kiro.DefaultModels {
		require.NotEqual(t, "gpt-6-astra", m.ID)
	}
	require.Equal(t, "gpt-6-astra", normalizeCodexModel("gpt-6-astra"))
	require.Equal(t, "gpt-6-astra", normalizeModelNameForPricing("gpt-6-astra"))
	require.True(t, supportsOpenAIReasoningEffortMax("gpt-6-astra"))
	for _, unknown := range []string{"gpt-6-astra-low", "gpt-6-astra-medium", "gpt-6-astra-high", "gpt-6-astra-xhigh", "gpt-6-astra-max", "gpt6-astra", "gpt6-astra-high", "gpt_6_astra_medium", "gpt-6-astra-pro", "gpt-6-astra-ultra", "gpt-6-astra-2026-09-03", "gpt-6-astra2", "gpt-6-other"} {
		require.False(t, isOpenAIGPT6AstraModel(unknown))
		require.Equal(t, unknown, normalizeCodexModel(unknown))
		require.NotEqual(t, "gpt-6-astra", normalizeModelNameForPricing(unknown))
		_, _, supported := splitOpenAICompatReasoningModel(unknown)
		require.False(t, supported, unknown)
	}
	require.Equal(t, "max", normalizeOpenAIReasoningEffortForModel("max", "gpt-6-astra"))
	require.Equal(t, "xhigh", normalizeOpenAIReasoningEffortForModel("max", "gpt-5.5"))
	d := newConfiguredCodexModelDescriptor("gpt-6-astra")
	require.Equal(t, "GPT-6 Astra", d.DisplayName)
	require.EqualValues(t, 922000, d.MaxContextWindow)
	var efforts []string
	for _, level := range d.SupportedReasoningLevels {
		efforts = append(efforts, level.Effort)
	}
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, efforts)
	require.True(t, isOpenAICodexImageInputModel("gpt-6-astra"))
}

func TestAstraIndependentReasoningEffort(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		body := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"` + effort + `"},"input":"hello","prompt_cache_key":"stable"}`)
		normalized, changed, err := normalizeOpenAIAstraRequest(account, body)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, normalized)
		require.Equal(t, effort, normalizeOpenAIReasoningEffortForModel(effort, "gpt-6-astra"))
	}
	// Existing GPT-5 compatibility is unrelated to this Astra-only cleanup.
	require.Equal(t, "gpt-5.4", normalizeCodexModel("gpt-5.4-high"))
}

func TestAstraWireNormalizationPreservesConversation(t *testing.T) {
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	body := []byte(`{"model":"gpt-6-astra","temperature":0.7,"top_p":0.9,"top_logprobs":2,"logprobs":true,"reasoning":{"effort":"none"},"prompt_cache_retention":"24h","prompt_cache_key":"stable","include":["message.output_text.logprobs","reasoning.encrypted_content"],"tools":[{"type":"function","name":"echo","async":true}],"input":[{"type":"configuration_update","reasoning":{"effort":"high"}},{"type":"message","role":"user","content":"prefix"}]}`)
	normalized, changed, err := normalizeOpenAIAstraRequest(a, body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "low", gjson.GetBytes(normalized, "reasoning.effort").String())
	for _, f := range []string{"temperature", "top_p", "top_logprobs", "logprobs", "prompt_cache_retention"} {
		require.False(t, gjson.GetBytes(normalized, f).Exists(), f)
	}
	for _, f := range []string{"tools", "input", "prompt_cache_key"} {
		require.JSONEq(t, gjson.GetBytes(body, f).Raw, gjson.GetBytes(normalized, f).Raw)
	}
	require.False(t, gjson.GetBytes(normalized, "prompt_cache_options").Exists())
	apiBody, _, err := normalizeOpenAIAstraRequest(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, body)
	require.NoError(t, err)
	require.Equal(t, "30m", gjson.GetBytes(apiBody, "prompt_cache_options.ttl").String())
	again, changed, err := normalizeOpenAIAstraRequest(a, normalized)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, normalized, again)
	other, changed, err := normalizeOpenAIAstraRequest(&Account{Platform: PlatformKiro}, body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, other)
	ws, _, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(body, a, false)
	require.NoError(t, err)
	require.Equal(t, "low", gjson.GetBytes(ws, "reasoning.effort").String())
	require.Equal(t, "configuration_update", gjson.GetBytes(ws, "input.0.type").String())
	require.True(t, gjson.GetBytes(ws, "tools.0.async").Bool())
}

func astraSSE() string {
	items := []any{
		map[string]any{"id": "msg_astra", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "hello world"}}},
		map[string]any{"id": "fc_astra", "type": "function_call", "call_id": "call_astra", "name": "probe_echo", "arguments": "{\"value\":\"OK\"}", "status": "completed"},
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_astra", "model": "gpt-6-astra", "status": "in_progress"}},
		{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_astra", "type": "message", "role": "assistant", "content": []any{}}},
		{"type": "response.content_part.added", "output_index": 0, "content_index": 0, "item_id": "msg_astra", "part": map[string]any{"type": "output_text", "text": ""}},
		{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": "msg_astra", "delta": "hello "},
		{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": "msg_astra", "delta": "world"},
		{"type": "response.output_item.done", "output_index": 0, "item": items[0]},
		{"type": "response.output_item.added", "output_index": 1, "item": map[string]any{"id": "fc_astra", "type": "function_call", "call_id": "call_astra", "name": "probe_echo", "arguments": ""}},
		{"type": "response.function_call_arguments.delta", "output_index": 1, "item_id": "fc_astra", "delta": "{\"value\":\"OK\"}"},
		{"type": "response.function_call_arguments.done", "output_index": 1, "item_id": "fc_astra", "arguments": "{\"value\":\"OK\"}"},
		{"type": "response.output_item.done", "output_index": 1, "item": items[1]},
		// Real Astra Codex responses omit output from the terminal envelope.
		{"type": "response.completed", "response": map[string]any{"id": "resp_astra", "model": "gpt-6-astra", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 40, "output_tokens": 15, "input_tokens_details": map[string]any{"cached_tokens": 8, "cache_write_tokens": 4}}}},
	}
	var wire strings.Builder
	for _, e := range events {
		b, _ := json.Marshal(e)
		wire.WriteString("data: " + string(b) + "\n\n")
	}
	return wire.String()
}

func TestAstraProtocolForwarders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"responses", "chat", "messages"} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/nonstream"
			if stream {
				name = protocol + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(astraSSE()))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}}
				payload := map[string]any{"model": "gpt-6-astra", "stream": stream, "temperature": 0.5, "reasoning": map[string]any{"effort": "max"}}
				path := "/v1/responses"
				if protocol == "responses" {
					payload["input"] = "hello"
					payload["prompt_cache_retention"] = "24h"
				} else {
					payload["messages"] = []any{map[string]any{"role": "user", "content": "hello"}}
					payload["reasoning_effort"] = "max"
					path = "/v1/chat/completions"
					if protocol == "messages" {
						payload["max_tokens"] = 128
						payload["output_config"] = map[string]any{"effort": "max"}
						path = "/v1/messages"
					}
				}
				body, _ := json.Marshal(payload)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", path, strings.NewReader(string(body)))
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				var result *OpenAIForwardResult
				var err error
				switch protocol {
				case "responses":
					result, err = svc.Forward(context.Background(), c, a, body)
				case "chat":
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, a, body, "", "")
				case "messages":
					result, err = svc.ForwardAsAnthropic(context.Background(), c, a, body, "", "")
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.lastBody, "model").String())
				require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
				if protocol == "responses" {
					require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_options").Exists())
				}
				require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
				require.Contains(t, rec.Body.String(), "world")
				require.Contains(t, rec.Body.String(), "probe_echo")
				if stream {
					terminal := "\"type\":\"response.completed\""
					if protocol == "chat" {
						terminal = "data: [DONE]"
					}
					if protocol == "messages" {
						terminal = "event: message_stop"
					}
					require.Equal(t, 1, strings.Count(rec.Body.String(), terminal), rec.Body.String())
				}
			})
		}
	}
}

func TestAstraBuildersLegacyEffortAndSampling(t *testing.T) {
	for _, typ := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, passthrough := range []bool{false, true} {
			a := &Account{ID: 7, Platform: PlatformOpenAI, Type: typ, Credentials: map[string]any{"base_url": "https://example.com"}}
			s := &OpenAIGatewayService{cfg: &config.Config{}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			body := []byte(`{"model":"gpt-6-astra","temperature":0.7,"reasoning":{"effort":"minimal"},"include":["message.output_text.logprobs"],"prompt_cache_options":{"ttl":"30m"},"prompt_cache_key":"stable","input":[]}`)
			var r *http.Request
			var err error
			if passthrough {
				r, err = s.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, a, body, "test")
			} else {
				r, err = s.buildUpstreamRequest(context.Background(), c, a, body, "test", true, "stable", false)
			}
			require.NoError(t, err)
			sent, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Equal(t, "low", gjson.GetBytes(sent, "reasoning.effort").String())
			require.False(t, gjson.GetBytes(sent, "temperature").Exists())
			if typ == AccountTypeOAuth {
				require.False(t, gjson.GetBytes(sent, "prompt_cache_options").Exists())
			} else {
				require.Equal(t, "30m", gjson.GetBytes(sent, "prompt_cache_options.ttl").String())
			}
			require.Equal(t, "stable", gjson.GetBytes(sent, "prompt_cache_key").String())
		}
	}
}

func TestAstraCostComponents(t *testing.T) {
	s := NewBillingService(&config.Config{}, nil)
	for _, long := range []bool{false, true} {
		for _, tier := range []string{"default", "priority", "flex"} {
			tokens := UsageTokens{InputTokens: 100, OutputTokens: 30, CacheReadTokens: 1000, CacheCreationTokens: 50}
			if long {
				tokens.CacheReadTokens = 300000
			}
			cost, err := s.CalculateCostWithServiceTier("gpt-6-astra", tokens, 1, tier)
			require.NoError(t, err)
			inputMult, outputMult, rate := 1.0, 1.0, 1.0
			if long {
				inputMult = 2
				outputMult = 1.5
			}
			if tier == "priority" {
				rate = 2
			}
			if tier == "flex" {
				rate = 0.5
			}
			require.InDelta(t, float64(tokens.InputTokens)*10e-6*inputMult*rate, cost.InputCost, 1e-10)
			require.InDelta(t, float64(tokens.OutputTokens)*50e-6*outputMult*rate, cost.OutputCost, 1e-10)
			require.InDelta(t, float64(tokens.CacheReadTokens)*1e-6*inputMult*rate, cost.CacheReadCost, 1e-10)
			require.InDelta(t, float64(tokens.CacheCreationTokens)*12.5e-6*inputMult*rate, cost.CacheCreationCost, 1e-10)
		}
	}
}

func BenchmarkAstraNativeRequestUnchanged(b *testing.B) {
	a := &Account{Platform: PlatformOpenAI}
	body := []byte(`{"model":"gpt-6-astra","input":"` + strings.Repeat("x", 600000) + `","reasoning":{"effort":"high"},"prompt_cache_key":"stable"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, changed, err := normalizeOpenAIAstraRequest(a, body); changed || err != nil {
			b.Fatal(changed, err)
		}
	}
}

func TestAstraPricingFallbackAndCatalog(t *testing.T) {
	s := NewBillingService(&config.Config{}, nil)
	body, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := &PricingService{}
	catalog.pricingData, err = catalog.parsePricingData(body)
	require.NoError(t, err)
	for _, source := range []*PricingService{nil, catalog, &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}} {
		s.pricingService = source
		price, err := s.GetModelPricing("gpt-6-astra")
		require.NoError(t, err)
		require.InDelta(t, 10e-6, price.InputPricePerToken, 1e-12)
		require.InDelta(t, 50e-6, price.OutputPricePerToken, 1e-12)
		require.InDelta(t, 12.5e-6, price.CacheCreationPricePerToken, 1e-12)
		require.InDelta(t, 1e-6, price.CacheReadPricePerToken, 1e-12)
		require.Equal(t, 272000, price.LongContextInputThreshold)
		require.False(t, s.shouldApplySessionLongContextPricing(UsageTokens{InputTokens: 272000}, price))
		require.True(t, s.shouldApplySessionLongContextPricing(UsageTokens{InputTokens: 272001}, price))
		require.InDelta(t, 25e-6, price.CacheCreationPricePerTokenPriority, 1e-12)
	}
}
