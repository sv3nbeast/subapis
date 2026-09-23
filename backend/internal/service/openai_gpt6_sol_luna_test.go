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
	kironianzs "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// gpt6SolLunaModels are the two official IDs verified against ChatGPT OAuth
// Responses on 2026-09-23. gpt-6-terra and every synthetic gpt-6-* spelling were
// rejected upstream with HTTP 400, so no alias family is introduced.
var gpt6SolLunaModels = []string{"gpt-6-sol", "gpt-6-luna"}

func TestGPT6SolLunaModelContract(t *testing.T) {
	for _, model := range gpt6SolLunaModels {
		require.True(t, isOpenAIOAuthServableModel(model), model)
		require.Contains(t, openai.DefaultModelIDs(), model)
		require.Equal(t, model, normalizeCodexModel(model))
		require.Equal(t, model, normalizeKnownOpenAICodexModel(model))
		require.Equal(t, model, normalizeModelNameForPricing(model))
		require.True(t, isOpenAIGPT6Model(model), model)
		require.False(t, isOpenAIGPT6AstraModel(model), "Sol/Luna must not collapse into the Astra price family")
		require.True(t, supportsOpenAIReasoningEffortMax(model), model)
		require.True(t, isOpenAICodexReasoningGPTModel(model), model)
		require.True(t, isOpenAICodexImageInputModel(model), model)
		require.True(t, configuredCodexSupportsPriorityServiceTier(model), model)
		require.True(t, shouldAutoInjectPromptCacheKeyForCompat(model), model)

		// Kiro returned 400 INVALID_MODEL_ID for both models while gpt-5.6-sol
		// succeeded on the same account, so neither Kiro catalog may list them.
		for _, m := range kiro.DefaultModels {
			require.NotEqual(t, model, m.ID)
		}
		for _, m := range kironianzs.DefaultModels {
			require.NotEqual(t, model, m.ID)
		}
	}

	// Each model owns exactly one ID: no effort/dated/pro suffix is mapped.
	for _, unknown := range []string{
		"gpt-6-terra", "gpt-6-sol-low", "gpt-6-sol-max", "gpt-6-luna-high",
		"gpt-6-sol-pro", "gpt-6-luna-2026-09-22", "gpt6-sol", "gpt-6-sol2",
	} {
		require.False(t, isOpenAIGPT6Model(unknown), unknown)
		require.Equal(t, unknown, normalizeCodexModel(unknown), unknown)
		for _, model := range gpt6SolLunaModels {
			require.NotEqual(t, model, normalizeModelNameForPricing(unknown), unknown)
		}
	}

	// Upstream Codex manifest (2026-09-23): Sol advertises the Ultra workflow, Luna does not.
	sol := newConfiguredCodexModelDescriptor("gpt-6-sol")
	require.EqualValues(t, 922000, sol.MaxContextWindow)
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max", "ultra"}, codexEffortList(sol))
	require.Nil(t, sol.MultiAgentReasoningEffort, "only Astra ships a multi-agent effort upstream")
	require.Equal(t, "v2", sol.MultiAgentVersion)

	luna := newConfiguredCodexModelDescriptor("gpt-6-luna")
	require.EqualValues(t, 922000, luna.MaxContextWindow)
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, codexEffortList(luna))
	require.Nil(t, luna.MultiAgentReasoningEffort)
}

func codexEffortList(d configuredCodexModelDescriptor) []string {
	efforts := make([]string, 0, len(d.SupportedReasoningLevels))
	for _, level := range d.SupportedReasoningLevels {
		efforts = append(efforts, level.Effort)
	}
	return efforts
}

func TestGPT6SolLunaKeepsNoneEffortAndDropsMinimal(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, model := range gpt6SolLunaModels {
		// Official migration guide: "GPT-6 Astra does not support the none
		// reasoning effort; GPT-6 Sol and Luna do." Live probe: none => HTTP 200.
		for _, effort := range []string{"none", "low", "medium", "high", "xhigh", "max"} {
			body := []byte(`{"model":"` + model + `","reasoning":{"effort":"` + effort + `"},"input":"hello","prompt_cache_key":"stable"}`)
			normalized, changed, err := normalizeOpenAIGPT6Request(oauth, body)
			require.NoError(t, err)
			require.False(t, changed, "%s/%s must stay byte-identical", model, effort)
			require.Equal(t, body, normalized)
		}
		// "minimal" is rejected upstream for the whole GPT-6 family (HTTP 400).
		body := []byte(`{"model":"` + model + `","reasoning":{"effort":"minimal"},"input":"hello"}`)
		normalized, changed, err := normalizeOpenAIGPT6Request(oauth, body)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "low", gjson.GetBytes(normalized, "reasoning.effort").String())
	}
	// Astra's existing behavior is unchanged: it still downgrades none to low.
	astraBody := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"none"},"input":"hello"}`)
	normalized, changed, err := normalizeOpenAIGPT6Request(oauth, astraBody)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "low", gjson.GetBytes(normalized, "reasoning.effort").String())
}

func TestGPT6SolLunaWireNormalizationPreservesConversation(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, model := range gpt6SolLunaModels {
		body := []byte(`{"model":"` + model + `","temperature":0.7,"top_p":0.9,"top_logprobs":2,"logprobs":true,` +
			`"reasoning":{"effort":"none"},"prompt_cache_retention":"24h","prompt_cache_key":"stable",` +
			`"include":["message.output_text.logprobs","reasoning.encrypted_content"],` +
			`"tools":[{"type":"function","name":"echo","async":true}],` +
			`"input":[{"type":"configuration_update","reasoning":{"effort":"high"}},{"type":"message","role":"user","content":"prefix"}]}`)

		normalized, changed, err := normalizeOpenAIGPT6Request(oauth, body)
		require.NoError(t, err)
		require.True(t, changed)
		// none survives for Sol/Luna even though the rest of the body is repaired.
		require.Equal(t, "none", gjson.GetBytes(normalized, "reasoning.effort").String())
		for _, field := range []string{"temperature", "top_p", "top_logprobs", "logprobs", "prompt_cache_retention", "prompt_cache_options"} {
			require.False(t, gjson.GetBytes(normalized, field).Exists(), "%s/%s", model, field)
		}
		require.JSONEq(t, `["reasoning.encrypted_content"]`, gjson.GetBytes(normalized, "include").Raw)
		for _, field := range []string{"tools", "input", "prompt_cache_key"} {
			require.JSONEq(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(normalized, field).Raw, field)
		}

		// Public API-key path keeps native cache options and upgrades the legacy field.
		apiBody, changed, err := normalizeOpenAIGPT6Request(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, body)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "30m", gjson.GetBytes(apiBody, "prompt_cache_options.ttl").String())

		// Idempotent, and other providers are never touched.
		again, changed, err := normalizeOpenAIGPT6Request(oauth, normalized)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, normalized, again)
		other, changed, err := normalizeOpenAIGPT6Request(&Account{Platform: PlatformKiro}, body)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, other)

		ws, _, err := normalizeOpenAIResponsesWebSocketCompatibilityBody(body, oauth, false)
		require.NoError(t, err)
		require.Equal(t, "none", gjson.GetBytes(ws, "reasoning.effort").String())
		require.Equal(t, "configuration_update", gjson.GetBytes(ws, "input.0.type").String())
		require.True(t, gjson.GetBytes(ws, "tools.0.async").Bool())
	}
}

func gpt6SolLunaSSE(model string) string {
	items := []any{
		map[string]any{"id": "msg_g6", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "hello world"}}},
		map[string]any{"id": "fc_g6", "type": "function_call", "call_id": "call_g6", "name": "probe_echo", "arguments": "{\"value\":\"OK\"}", "status": "completed"},
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_g6", "model": model, "status": "in_progress"}},
		{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_g6", "type": "message", "role": "assistant", "content": []any{}}},
		{"type": "response.content_part.added", "output_index": 0, "content_index": 0, "item_id": "msg_g6", "part": map[string]any{"type": "output_text", "text": ""}},
		{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": "msg_g6", "delta": "hello "},
		{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": "msg_g6", "delta": "world"},
		{"type": "response.output_item.done", "output_index": 0, "item": items[0]},
		{"type": "response.output_item.added", "output_index": 1, "item": map[string]any{"id": "fc_g6", "type": "function_call", "call_id": "call_g6", "name": "probe_echo", "arguments": ""}},
		{"type": "response.function_call_arguments.delta", "output_index": 1, "item_id": "fc_g6", "delta": "{\"value\":\"OK\"}"},
		{"type": "response.function_call_arguments.done", "output_index": 1, "item_id": "fc_g6", "arguments": "{\"value\":\"OK\"}"},
		{"type": "response.output_item.done", "output_index": 1, "item": items[1]},
		// Real Codex responses omit output from the terminal envelope.
		{"type": "response.completed", "response": map[string]any{"id": "resp_g6", "model": model, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 40, "output_tokens": 15, "input_tokens_details": map[string]any{"cached_tokens": 8, "cache_write_tokens": 4}}}},
	}
	var wire strings.Builder
	for _, e := range events {
		b, _ := json.Marshal(e)
		wire.WriteString("data: " + string(b) + "\n\n")
	}
	return wire.String()
}

func TestGPT6SolLunaProtocolForwarders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range gpt6SolLunaModels {
		for _, protocol := range []string{"responses", "chat", "messages"} {
			for _, stream := range []bool{false, true} {
				name := model + "/" + protocol + "/nonstream"
				if stream {
					name = model + "/" + protocol + "/stream"
				}
				t.Run(name, func(t *testing.T) {
					upstream := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: 200,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(gpt6SolLunaSSE(model))),
					}}
					svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
					a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
						Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}}
					payload := map[string]any{"model": model, "stream": stream, "temperature": 0.5,
						"reasoning": map[string]any{"effort": "max"}}
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

					require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String(),
						"the exact official id must reach the upstream wire")
					require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
					if protocol == "responses" {
						require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_options").Exists())
						require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_retention").Exists())
					}
					require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
					require.Contains(t, rec.Body.String(), "world")
					require.Contains(t, rec.Body.String(), "probe_echo")
					if stream {
						terminal := "\"type\":\"response.completed\""
						switch protocol {
						case "chat":
							terminal = "data: [DONE]"
						case "messages":
							terminal = "event: message_stop"
						}
						require.Equal(t, 1, strings.Count(rec.Body.String(), terminal), rec.Body.String())
					}
				})
			}
		}
	}
}

func TestGPT6SolLunaPricingFallbackAndCatalog(t *testing.T) {
	body, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := &PricingService{}
	catalog.pricingData, err = catalog.parsePricingData(body)
	require.NoError(t, err)

	for _, tc := range []struct {
		model                      string
		input, output, write, read float64
	}{
		{"gpt-6-sol", 2e-6, 10e-6, 2.5e-6, 0.2e-6},
		{"gpt-6-luna", 0.1e-6, 0.5e-6, 0.125e-6, 0.01e-6},
	} {
		s := NewBillingService(&config.Config{}, nil)
		// nil / bundled catalog / empty catalog all resolve to the official prices.
		for _, source := range []*PricingService{nil, catalog, {pricingData: map[string]*LiteLLMModelPricing{}}} {
			s.pricingService = source
			price, err := s.GetModelPricing(tc.model)
			require.NoError(t, err, tc.model)
			require.InDelta(t, tc.input, price.InputPricePerToken, 1e-14, tc.model)
			require.InDelta(t, tc.output, price.OutputPricePerToken, 1e-14, tc.model)
			require.InDelta(t, tc.write, price.CacheCreationPricePerToken, 1e-14, tc.model)
			require.InDelta(t, tc.read, price.CacheReadPricePerToken, 1e-14, tc.model)
			// Fast is 2x on every component.
			require.InDelta(t, tc.input*2, price.InputPricePerTokenPriority, 1e-14, tc.model)
			require.InDelta(t, tc.output*2, price.OutputPricePerTokenPriority, 1e-14, tc.model)
			require.InDelta(t, tc.write*2, price.CacheCreationPricePerTokenPriority, 1e-14, tc.model)
			require.InDelta(t, tc.read*2, price.CacheReadPricePerTokenPriority, 1e-14, tc.model)
			// Long context starts strictly above 272,000 input tokens.
			require.Equal(t, 272000, price.LongContextInputThreshold, tc.model)
			require.InDelta(t, 2, price.LongContextInputMultiplier, 1e-9, tc.model)
			require.InDelta(t, 1.5, price.LongContextOutputMultiplier, 1e-9, tc.model)
			require.False(t, s.shouldApplySessionLongContextPricing(UsageTokens{InputTokens: 272000}, price))
			require.True(t, s.shouldApplySessionLongContextPricing(UsageTokens{InputTokens: 272001}, price))
		}
		require.InDelta(t, 2.0, openAIModelFastPricingRatio(tc.model), 1e-9, tc.model)
		require.Contains(t, localChannelPricingModelNamesByProvider("openai"), tc.model)

		// A remote catalog that ships the new model without a cache-write rate must
		// still bill cache creation at the official 1.25x input rule, not at zero.
		policy := NewBillingService(&config.Config{}, nil).applyModelSpecificPricingPolicy(tc.model, &ModelPricing{
			InputPricePerToken:         tc.input,
			OutputPricePerToken:        tc.output,
			InputPricePerTokenPriority: tc.input * 2,
		})
		require.InDelta(t, tc.input*1.25, policy.CacheCreationPricePerToken, 1e-14, tc.model)
		require.InDelta(t, tc.input*2*1.25, policy.CacheCreationPricePerTokenPriority, 1e-14, tc.model)
	}
}

// TestGPT6SolLunaAPIKeyKeepsPromptCacheOptions locks the public API-key wire
// contract: prompt_cache_options is the supported field for the whole GPT-6
// family, and the legacy prompt_cache_retention is migrated rather than dropped.
func TestGPT6SolLunaAPIKeyKeepsPromptCacheOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range gpt6SolLunaModels {
		for _, passthrough := range []bool{false, true} {
			a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://example.com", "api_key": "test"}}
			s := &OpenAIGatewayService{cfg: &config.Config{}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			body := []byte(`{"model":"` + model + `","temperature":0.7,"reasoning":{"effort":"minimal"},` +
				`"include":["message.output_text.logprobs"],"prompt_cache_retention":"24h","prompt_cache_key":"stable","input":[]}`)

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
			require.Equal(t, "30m", gjson.GetBytes(sent, "prompt_cache_options.ttl").String(), "%s/passthrough=%v", model, passthrough)
			require.False(t, gjson.GetBytes(sent, "prompt_cache_retention").Exists())
			require.False(t, gjson.GetBytes(sent, "temperature").Exists())
			require.Equal(t, "low", gjson.GetBytes(sent, "reasoning.effort").String(), "minimal is rejected upstream")
			require.Equal(t, "stable", gjson.GetBytes(sent, "prompt_cache_key").String())
		}
	}
}

// BenchmarkGPT6SolNativeRequestUnchanged proves the pre-first-token hot path does
// not copy a large valid native request: the normalizer must report "unchanged"
// without decoding the (up to 1M-context) conversation.
func BenchmarkGPT6SolNativeRequestUnchanged(b *testing.B) {
	a := &Account{Platform: PlatformOpenAI}
	body := []byte(`{"model":"gpt-6-sol","input":"` + strings.Repeat("x", 600000) + `","reasoning":{"effort":"none"},"prompt_cache_key":"stable"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, changed, err := normalizeOpenAIGPT6Request(a, body); changed || err != nil {
			b.Fatal(changed, err)
		}
	}
}

// TestGPT6SolLunaStripsUnsupportedReasoningMode records the 2026-09-23 probe:
// reasoning.mode returns HTTP 400 for every GPT-6 model, so the legacy
// strip-mode / pro->max compatibility path must keep applying to Sol and Luna.
func TestGPT6SolLunaStripsUnsupportedReasoningMode(t *testing.T) {
	for _, model := range gpt6SolLunaModels {
		body := []byte(`{"model":"` + model + `","reasoning":{"mode":"pro"},"input":"hello"}`)
		normalized, changed, err := normalizeOpenAIResponsesReasoningMode(body)
		require.NoError(t, err)
		require.True(t, changed, model)
		require.False(t, gjson.GetBytes(normalized, "reasoning.mode").Exists(), model)
		require.Equal(t, "max", gjson.GetBytes(normalized, "reasoning.effort").String(), model)
	}
}
