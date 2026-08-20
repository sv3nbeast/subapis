package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionPlanBonusGroupsMigrationSnapshotsLegacyOrders(t *testing.T) {
	raw, err := FS.ReadFile("226_subscription_plan_bonus_groups.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Contains(t, sql, "add column if not exists bonus_group_ids jsonb not null default '[]'::jsonb")
	require.Contains(t, sql, "add column if not exists subscription_group_ids jsonb not null default '[]'::jsonb")
	require.Contains(t, sql, "set subscription_group_ids = jsonb_build_array(subscription_group_id)")
	require.Contains(t, sql, "where order_type = 'subscription'")
	require.Contains(t, sql, "subscription_group_ids = '[]'::jsonb")
	require.Contains(t, sql, "check (jsonb_typeof(bonus_group_ids) = 'array')")
	require.Contains(t, sql, "check (jsonb_typeof(subscription_group_ids) = 'array')")
	require.NotContains(t, sql, "join subscription_plans")
	require.NotContains(t, sql, "drop column")
}
