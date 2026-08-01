package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration192BackfillsOnlyCurrentFableSubscriptionWindows(t *testing.T) {
	content, err := FS.ReadFile("192_backfill_fable_subscription_model_usage.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "178_subscription_model_quota_ratios.sql")
	require.Contains(t, sql, "g.model_quota_ratios ? 'claude-fable-5'")
	require.Contains(t, sql, "ul.subscription_id")
	require.Contains(t, sql, "ul.requested_model")
	require.Contains(t, sql, "target.daily_window_start > NOW() - INTERVAL '24 hours'")
	require.Contains(t, sql, "target.weekly_window_start > NOW() - INTERVAL '7 days'")
	require.Contains(t, sql, "target.monthly_window_start > NOW() - INTERVAL '30 days'")
	require.Contains(t, sql, "GREATEST(")
	require.Contains(t, sql, "jsonb_set(")
}
