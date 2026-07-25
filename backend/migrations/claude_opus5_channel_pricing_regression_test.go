package migrations

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration180SplitsClaudeOpus5IntoDedicatedChannelPricing(t *testing.T) {
	content, err := FS.ReadFile("180_split_claude_opus5_channel_pricing.sql")
	require.NoError(t, err)

	sql := string(content)
	for _, model := range []string{"claude-opus-5", "claude-opus-5-thinking"} {
		require.Contains(t, sql, model)
	}
	for _, price := range []string{
		"0.000005000000",
		"0.000025000000",
		"0.000006250000",
		"0.000010000000",
		"0.000000500000",
	} {
		require.Contains(t, sql, price)
	}

	require.Contains(t, sql, "INSERT INTO channel_model_pricing")
	require.Contains(t, sql, "jsonb_array_elements_text")
	require.Contains(t, sql, "enabled = jsonb_array_length(cleaned.models) > 0")
	require.Contains(t, sql, "existing.enabled = true")
	require.Contains(t, sql, "existing.billing_mode = 'token'")
	require.Contains(t, sql, "Account-statistics pricing is independently operator-configured")
	require.Equal(t, 1, strings.Count(sql, "INSERT INTO channel_model_pricing"))
}

func TestMigration179RemainsAnImmutablePredecessorOfOpus5PricingSplit(t *testing.T) {
	content, err := FS.ReadFile("179_add_claude_opus5_support.sql")
	require.NoError(t, err)

	checksum := sha256.Sum256(content)
	require.Equal(t,
		"6183959757c4cc6bf44b18d1cf22aab3b289fdecc67d881bbd627dad83d81713",
		fmt.Sprintf("%x", checksum),
	)
}
