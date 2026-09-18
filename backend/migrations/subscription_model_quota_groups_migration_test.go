package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration242CreatesAndBackfillsSharedFableQuota(t *testing.T) {
	content, err := FS.ReadFile("242_subscription_model_quota_groups.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS model_quota_groups")
	require.Contains(t, sql, "fable-5-shared")
	require.Contains(t, sql, "jsonb_build_array('claude-fable-5', 'claude-fable-5-1')")
	require.Contains(t, sql, "model_quota_ratios = COALESCE(model_quota_ratios, '{}'::jsonb)")
	require.Contains(t, sql, "'claude-fable-5-1-thinking'")
	require.Contains(t, sql, "ARRAY['group:fable-5-shared']")
	require.Contains(t, sql, "GREATEST(")
}
