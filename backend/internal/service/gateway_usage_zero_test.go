package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSEUsageDeltaExplicitZeroOverridesStartCacheBuckets(t *testing.T) {
	svc := &GatewayService{}
	usage := &ClaudeUsage{}

	svc.parseSSEUsage(`{"type":"message_start","message":{"usage":{"input_tokens":100,"cache_creation_input_tokens":30,"cache_read_input_tokens":70,"cache_creation":{"ephemeral_5m_input_tokens":30,"ephemeral_1h_input_tokens":0}}}}`, usage)
	svc.parseSSEUsage(`{"type":"message_delta","usage":{"input_tokens":20,"output_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":80,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"_sub2api_kiro_usage_final":true}}`, usage)

	require.Equal(t, 20, usage.InputTokens)
	require.Equal(t, 12, usage.OutputTokens)
	require.Zero(t, usage.CacheCreationInputTokens)
	require.Equal(t, 80, usage.CacheReadInputTokens)
	require.Zero(t, usage.CacheCreation5mTokens)
	require.Zero(t, usage.CacheCreation1hTokens)
}

func TestSSEUsagePassthroughDeltaExplicitZeroOverridesStartCacheBuckets(t *testing.T) {
	svc := &GatewayService{}
	usage := &ClaudeUsage{}

	svc.parseSSEUsagePassthrough(`{"type":"message_start","message":{"usage":{"input_tokens":100,"cache_creation_input_tokens":30,"cache_read_input_tokens":70,"cache_creation":{"ephemeral_5m_input_tokens":30,"ephemeral_1h_input_tokens":0}}}}`, usage)
	svc.parseSSEUsagePassthrough(`{"type":"message_delta","usage":{"input_tokens":20,"output_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":80,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"_sub2api_kiro_usage_final":true}}`, usage)

	require.Equal(t, 20, usage.InputTokens)
	require.Equal(t, 12, usage.OutputTokens)
	require.Zero(t, usage.CacheCreationInputTokens)
	require.Equal(t, 80, usage.CacheReadInputTokens)
	require.Zero(t, usage.CacheCreation5mTokens)
	require.Zero(t, usage.CacheCreation1hTokens)
}

func TestSSEUsageGenericDeltaPlaceholderZeroDoesNotOverrideStart(t *testing.T) {
	svc := &GatewayService{}
	usage := &ClaudeUsage{}

	svc.parseSSEUsage(`{"type":"message_start","message":{"usage":{"input_tokens":100,"cache_creation_input_tokens":30,"cache_read_input_tokens":70}}}`, usage)
	svc.parseSSEUsage(`{"type":"message_delta","usage":{"input_tokens":0,"output_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`, usage)

	require.Equal(t, 100, usage.InputTokens)
	require.Equal(t, 12, usage.OutputTokens)
	require.Equal(t, 30, usage.CacheCreationInputTokens)
	require.Equal(t, 70, usage.CacheReadInputTokens)
}
