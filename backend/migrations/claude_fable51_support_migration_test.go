package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration231AddsAnthropicFable51Support(t *testing.T) {
	content, err := FS.ReadFile("231_add_claude_fable51_support.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "platform = 'anthropic'")
	require.Contains(t, sql, "claude-fable-5-1")
	require.Contains(t, sql, "jsonb_object_keys")
	require.Contains(t, sql, "mapping_key.key LIKE '%*%'")
	require.Contains(t, sql, "jsonb_array_elements_text")
	require.Contains(t, sql, "INSERT INTO channel_model_pricing")
	require.Contains(t, sql, "enabled = jsonb_array_length(cleaned.models) > 0")
	require.Contains(t, sql, "Custom prices")
	require.Equal(t, 1, strings.Count(sql, "INSERT INTO channel_model_pricing"))

	for _, price := range []string{
		"0.000015000000",
		"0.000075000000",
		"0.000018750000",
		"0.000030000000",
		"0.000000250000",
	} {
		require.Contains(t, sql, price)
	}

	for _, excludedPlatform := range []string{"platform = 'kiro'", "platform = 'bedrock'", "platform = 'antigravity'"} {
		require.NotContains(t, sql, excludedPlatform)
	}
	require.NotContains(t, sql, "channel_account_stats_model_pricing")
}
