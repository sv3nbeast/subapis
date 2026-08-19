package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestMergeAnthropicUsageCapturesKiroCredits(t *testing.T) {
	var usage ClaudeUsage

	mergeAnthropicUsage(&usage, apicompat.AnthropicUsage{
		OutputTokens: 7,
		KiroCredits:  0.17,
	})

	require.Equal(t, 7, usage.OutputTokens)
	require.InDelta(t, 0.17, usage.KiroCredits, 0.000001)
}

func TestMergeAnthropicUsagePrefersInternalBillingUsageOverContextProjection(t *testing.T) {
	var usage ClaudeUsage

	mergeAnthropicUsage(&usage, apicompat.AnthropicUsage{
		InputTokens:              84_999,
		OutputTokens:             10,
		CacheReadInputTokens:     679_992,
		CacheCreationInputTokens: 84_999,
		BillingUsage: &apicompat.AnthropicBillingUsage{
			InputTokens:              10_000,
			OutputTokens:             10,
			CacheReadInputTokens:     80_000,
			CacheCreationInputTokens: 10_000,
			CacheCreation5mTokens:    3_000,
			CacheCreation1hTokens:    7_000,
		},
	})

	require.Equal(t, 10_000, usage.InputTokens)
	require.Equal(t, 10, usage.OutputTokens)
	require.Equal(t, 80_000, usage.CacheReadInputTokens)
	require.Equal(t, 10_000, usage.CacheCreationInputTokens)
	require.Equal(t, 3_000, usage.CacheCreation5mTokens)
	require.Equal(t, 7_000, usage.CacheCreation1hTokens)
}

func TestOpenAICompatResponseOmitsInternalKiroCredits(t *testing.T) {
	stopReason := "end_turn"
	resp := &apicompat.AnthropicResponse{
		ID:         "msg_kiro_credits",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-sonnet-4.5",
		Content:    []apicompat.AnthropicContentBlock{{Type: "text", Text: "ok"}},
		StopReason: &stopReason,
		Usage: apicompat.AnthropicUsage{
			InputTokens:  3,
			OutputTokens: 5,
			KiroCredits:  0.17,
		},
	}

	responses := apicompat.AnthropicToResponsesResponse(resp)
	chat := apicompat.ResponsesToChatCompletions(responses, "claude-sonnet-4-5")
	body, err := json.Marshal(chat)
	require.NoError(t, err)

	require.NotContains(t, string(body), "_sub2api_kiro_credits")
	require.Contains(t, string(body), `"prompt_tokens":3`)
	require.Contains(t, string(body), `"completion_tokens":5`)
}
