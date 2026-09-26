package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestIsAnthropicAdaptiveOnlyThinkingModel(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4-7", "claude-opus-4-8", "claude-opus-4.8", "claude-opus-4-8-thinking",
		"claude-opus-5", "claude-opus-5-5", "claude-opus-5.5", "claude-opus-5-5[1m]", "claude-opus-6",
		"claude-sonnet-5", "claude-fable-5", "claude-fable-5-1", "CLAUDE-OPUS-4-7",
	} {
		require.True(t, isAnthropicAdaptiveOnlyThinkingModel(model), model)
	}
	for _, model := range []string{
		"claude-opus-4-6", "claude-opus-4.6", "claude-opus-4-5-20251101",
		// A dated snapshot must not read its date as the minor version.
		"claude-opus-4-20250514", "claude-opus-4-1-20250805",
		"claude-sonnet-4-6", "claude-sonnet-4-5-20250929", "claude-haiku-4-5-20251001",
		"gpt-5.6-sol", "",
	} {
		require.False(t, isAnthropicAdaptiveOnlyThinkingModel(model), model)
	}
}

// convertOpenAIChatForAnthropic runs the Chat Completions → Anthropic chain used
// by ForwardAsChatCompletions, with upstreamModel standing in for the account's
// mapped model.
func convertOpenAIChatForAnthropic(t *testing.T, requestedModel, upstreamModel, effort string) *apicompat.AnthropicRequest {
	t.Helper()
	body := []byte(`{"model":"` + requestedModel + `","max_tokens":2048,"reasoning_effort":"` + effort + `","messages":[{"role":"user","content":"hi"}]}`)
	var cc apicompat.ChatCompletionsRequest
	require.NoError(t, json.Unmarshal(body, &cc))
	responsesReq, err := apicompat.ChatCompletionsToResponses(&cc)
	require.NoError(t, err)
	req, err := apicompat.ResponsesToAnthropicRequest(responsesReq)
	require.NoError(t, err)
	req.Model = upstreamModel
	applyAnthropicThinkingAliasToRequest(req, requestedModel)
	return req
}

func TestOpenAICompatReasoningUsesAdaptiveThinkingOnAdaptiveOnlyModels(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-opus-4-8", "claude-opus-4-7", "claude-sonnet-5", "claude-fable-5", "claude-opus-5"} {
		for effort, wantEffort := range map[string]string{"medium": "medium", "high": "high", "xhigh": "max"} {
			req := convertOpenAIChatForAnthropic(t, model, model, effort)
			require.NotNil(t, req.Thinking, "%s %s", model, effort)
			require.Equal(t, "adaptive", req.Thinking.Type, "%s %s", model, effort)
			require.Zero(t, req.Thinking.BudgetTokens, "%s %s", model, effort)
			require.Equal(t, "summarized", req.Thinking.Display, "%s %s", model, effort)
			require.NotNil(t, req.OutputConfig)
			require.Equal(t, wantEffort, req.OutputConfig.Effort, "%s %s", model, effort)

			raw, err := json.Marshal(req)
			require.NoError(t, err)
			require.JSONEq(t, `{"type":"adaptive","display":"summarized"}`, gjson.GetBytes(raw, "thinking").Raw)
		}
	}
}

func TestOpenAICompatReasoningKeepsLegacyThinkingOnOlderModels(t *testing.T) {
	// Opus 4.6 still takes an explicit budget; nothing changes there.
	req := convertOpenAIChatForAnthropic(t, "claude-opus-4-6", "claude-opus-4-6", "high")
	require.Equal(t, "enabled", req.Thinking.Type)
	require.Positive(t, req.Thinking.BudgetTokens)
	require.Empty(t, req.Thinking.Display)

	// low effort converts without a thinking block, as before.
	req = convertOpenAIChatForAnthropic(t, "claude-opus-4-8", "claude-opus-4-8", "low")
	require.Nil(t, req.Thinking)
	require.Equal(t, "low", req.OutputConfig.Effort)
}

// On Kiro the budget used to be re-derived into an effort, so high arrived as
// medium and xhigh/max as high. With adaptive thinking the requested effort
// reaches the Kiro payload unchanged.
func TestOpenAICompatReasoningEffortReachesKiroUnchanged(t *testing.T) {
	cases := []struct {
		requested, kiroUpstream, effort, want string
	}{
		{"claude-opus-5-5", "claude-opus-5.5", "high", "high"},
		{"claude-opus-4-8", "claude-opus-4.8", "high", "high"},
		{"claude-opus-4-7", "claude-opus-4.7", "high", "high"},
		{"claude-sonnet-5", "claude-sonnet-5", "high", "high"},
		{"claude-opus-4-8", "claude-opus-4.8", "xhigh", "max"},
		{"claude-opus-5-5", "claude-opus-5.5", "medium", "medium"},
	}
	for _, tc := range cases {
		req := convertOpenAIChatForAnthropic(t, tc.requested, tc.kiroUpstream, tc.effort)
		anthropicBody, err := json.Marshal(req)
		require.NoError(t, err)

		prepared := nianzsPrepareKiroPayloadBodyForRequestModel(anthropicBody, tc.requested)
		result, err := nianzskiro.BuildKiroPayloadWithOptions(prepared, nianzskiro.MapModel(tc.requested), "", nil, nianzskiro.KiroPayloadOptions{Origin: "AI_EDITOR"})
		require.NoError(t, err)
		fields := gjson.GetBytes(result.Payload, "additionalModelRequestFields")
		require.Equal(t, "adaptive", fields.Get("thinking.type").String(), "%s %s", tc.requested, tc.effort)
		require.Equal(t, tc.want, fields.Get("output_config.effort").String(), "%s %s", tc.requested, tc.effort)
	}
}
