package kiro

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Legacy-engine parity for Opus 5.5 (Kiro upstream ID claude-opus-5.5,
// verified 2026-09-26): 1M context, 128K output, adaptive-only thinking.

func TestLegacyMapModelOpus55(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-opus-5-5-thinking", "claude-opus-5.5"} {
		require.Equal(t, "claude-opus-5.5", MapModel(model), model)
	}
	require.Equal(t, "claude-opus-5", MapModel("claude-opus-5"))
}

func TestLegacyOpus55Limits(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-opus-5.5", "claude-opus-5-5-thinking"} {
		require.Equal(t, 128000, kiroMaxOutputTokensForModel(model), model)
		require.Equal(t, kiroExtendedContextTokens, contextWindowTokensForModel(model), model)
		require.True(t, requiresImplicitThinkingTagStripping(model), model)
		require.True(t, isKiroOutputConfigPathModel(MapModel(model)), model)
		require.True(t, isKiroAdaptiveOnlyOpus5Family(model), model)
	}
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, kiroModelEffortEnums["claude-opus-5.5"])
	// Opus 4.8 is not part of the adaptive-only family: it still accepts the
	// enabled/budget form.
	require.False(t, isKiroAdaptiveOnlyOpus5Family("claude-opus-4-8"))
}

func TestLegacyBuildKiroPayloadOpus55NormalizesEnabledThinking(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5-5",
		"thinking":{"type":"enabled","budget_tokens":8192},
		"output_config":{"effort":"medium"},
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	t.Run("oauth system prompt", func(t *testing.T) {
		result, err := BuildKiroPayloadWithContext(body, "claude-opus-5.5", "", "AI_EDITOR", nil)
		require.NoError(t, err)
		systemContent := gjson.GetBytes(result.Payload, "conversationState.history.0.userInputMessage.content").String()
		require.Contains(t, systemContent, "<thinking_mode>adaptive</thinking_mode>\n<thinking_effort>medium</thinking_effort>")
		require.NotContains(t, systemContent, "<thinking_mode>enabled</thinking_mode>")
		require.True(t, result.Context.ThinkingEnabled)
	})

	t.Run("cli native fields", func(t *testing.T) {
		result, err := BuildKiroPayloadWithOptions(body, "claude-opus-5.5", "", nil, KiroPayloadOptions{
			Origin:                     "KIRO_CLI",
			UseNativeEffort:            true,
			InjectThinkingSystemPrompt: false,
		})
		require.NoError(t, err)
		require.Equal(t, "adaptive", gjson.GetBytes(result.Payload, "additionalModelRequestFields.thinking.type").String())
		require.Equal(t, "medium", gjson.GetBytes(result.Payload, "additionalModelRequestFields.output_config.effort").String())
		require.False(t, gjson.GetBytes(result.Payload, "additionalModelRequestFields.thinking.budget_tokens").Exists())
	})
}

func TestLegacyBuildKiroPayloadOpus55ThinkingAliasIsAdaptive(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5-5-thinking",
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, MapModel("claude-opus-5-5-thinking"), "", "AI_EDITOR", nil)
	require.NoError(t, err)
	systemContent := gjson.GetBytes(result.Payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "<thinking_mode>adaptive</thinking_mode>")
	require.NotContains(t, systemContent, "<thinking_mode>enabled</thinking_mode>")
	require.Equal(t, "claude-opus-5.5", gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.modelId").String())
}
