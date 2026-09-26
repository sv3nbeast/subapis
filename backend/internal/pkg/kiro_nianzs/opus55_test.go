package kiro

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Kiro ListAvailableModels (account 2696, 2026-09-26) lists Opus 5.5 as
// claude-opus-5.5 with maxInputTokens 1000000, maxOutputTokens 128000,
// thinking.type enum ["adaptive"] and effort default "medium".

func TestMapModelOpus55UsesKiroDottedUpstreamID(t *testing.T) {
	for _, model := range []string{
		"claude-opus-5-5", "claude-opus-5-5-thinking",
		"claude-opus-5.5", "claude-opus-5.5-thinking",
		"CLAUDE-OPUS-5-5",
	} {
		require.Equal(t, "claude-opus-5.5", MapModel(model), model)
	}
	// Opus 5 keeps its own undotted upstream ID.
	require.Equal(t, "claude-opus-5", MapModel("claude-opus-5"))
	// An unreleased spelling still normalizes generically rather than
	// collapsing onto 5.5; Kiro rejects it upstream with INVALID_MODEL_ID.
	require.Equal(t, "claude-opus-5.9", MapModel("claude-opus-5-9"))
}

func TestOpus55KiroLimits(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-opus-5.5", "claude-opus-5-5-thinking"} {
		require.Equal(t, 128000, MaxOutputTokensForModel(model), model)
		require.Equal(t, kiroExtendedContextTokens, ContextWindowTokensForModel(model), model)
		require.True(t, isOutputConfigPathModel(MapModel(model)), model)
		require.True(t, requiresImplicitThinkingTagStripping(model), model)
	}
}

func TestBuildKiroPayloadOpus55ThinkingAliasIsAdaptive(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5-5-thinking",
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, MapModel("claude-opus-5-5-thinking"), "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	require.Equal(t, "claude-opus-5.5", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.modelId").String())
	// Opus 5.5 only accepts adaptive thinking; the legacy enabled/budget form
	// must never reach the upstream.
	require.Equal(t, "adaptive", gjson.GetBytes(payload, "additionalModelRequestFields.thinking.type").String())
	require.False(t, gjson.GetBytes(payload, "additionalModelRequestFields.thinking.budget_tokens").Exists())
	require.Equal(t, "high", gjson.GetBytes(payload, "additionalModelRequestFields.output_config.effort").String())
	require.True(t, result.Context.ThinkingEnabled)
}

func TestBuildKiroPayloadOpus55LegacyEnabledBecomesAdaptive(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5-5",
		"max_tokens":8000,
		"thinking":{"type":"enabled","budget_tokens":4000},
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, MapModel("claude-opus-5-5"), "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	require.Equal(t, "adaptive", gjson.GetBytes(payload, "additionalModelRequestFields.thinking.type").String())
	require.False(t, gjson.GetBytes(payload, "additionalModelRequestFields.thinking.budget_tokens").Exists())
}

func TestBuildKiroPayloadOpus55KeepsFullOutputBudget(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5-5",
		"max_tokens":128000,
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, MapModel("claude-opus-5-5"), "", "AI_EDITOR", nil)
	require.NoError(t, err)
	// Without the 128K cap a request would be silently clamped to the 64K
	// default for unknown models.
	require.EqualValues(t, 128000, gjson.GetBytes(result.Payload, "inferenceConfig.maxTokens").Int())
}
