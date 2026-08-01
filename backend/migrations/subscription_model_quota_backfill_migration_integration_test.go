//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestMigration192BackfillsFableUsageIdempotently(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(
		ctx,
		"postgres:15-alpine",
		tcpostgres.WithDatabase("sub2api_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.Eventually(t, func() bool { return db.PingContext(ctx) == nil }, 15*time.Second, 100*time.Millisecond)

	_, err = db.ExecContext(ctx, `
CREATE TABLE schema_migrations (
    filename TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE groups (
    id BIGINT PRIMARY KEY,
    status TEXT NOT NULL,
    subscription_type TEXT NOT NULL,
    model_quota_ratios JSONB NOT NULL DEFAULT '{}'::jsonb,
    deleted_at TIMESTAMPTZ
);
CREATE TABLE user_subscriptions (
    id BIGINT PRIMARY KEY,
    group_id BIGINT NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL,
    daily_window_start TIMESTAMPTZ,
    weekly_window_start TIMESTAMPTZ,
    monthly_window_start TIMESTAMPTZ,
    quota_cycle_start_at TIMESTAMPTZ,
    quota_cycle_end_at TIMESTAMPTZ,
    model_usage JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE TABLE usage_logs (
    subscription_id BIGINT,
    requested_model TEXT,
    model TEXT NOT NULL,
    actual_cost NUMERIC(20, 10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
`)
	require.NoError(t, err)

	now := time.Now().UTC()
	_, err = db.ExecContext(ctx, `
INSERT INTO schema_migrations (filename, applied_at)
VALUES ('178_subscription_model_quota_ratios.sql', $1);
INSERT INTO groups (id, status, subscription_type, model_quota_ratios)
VALUES (10, 'active', 'subscription', '{"claude-fable-5":0.5}'::jsonb);
INSERT INTO user_subscriptions (
    id, group_id, starts_at, expires_at, status,
    daily_window_start, weekly_window_start, monthly_window_start,
    quota_cycle_start_at, quota_cycle_end_at, model_usage
)
VALUES (
    20, 10, $2, $3, 'active',
    $4, $5, $6,
    $2, $3,
    '{"claude-sonnet-5":{"daily_usage_usd":1,"weekly_usage_usd":2,"monthly_usage_usd":3}}'::jsonb
);
`,
		now.Add(-10*24*time.Hour),
		now.Add(-25*24*time.Hour),
		now.Add(5*24*time.Hour),
		now.Add(-12*time.Hour),
		now.Add(-3*24*time.Hour),
		now.Add(-20*24*time.Hour),
	)
	require.NoError(t, err)

	insertUsage := func(requestedModel string, cost float64, createdAt time.Time) {
		t.Helper()
		_, insertErr := db.ExecContext(ctx, `
INSERT INTO usage_logs (subscription_id, requested_model, model, actual_cost, created_at)
VALUES (20, $1, $1, $2, $3)
`, requestedModel, cost, createdAt)
		require.NoError(t, insertErr)
	}

	insertUsage("claude-fable-5", 2, now.Add(-2*time.Hour))
	insertUsage("anthropic.claude-fable-5-v1:0", 3, now.Add(-time.Hour))
	insertUsage("projects/p/locations/l/publishers/anthropic/models/claude-fable-5-20260601", 7, now.Add(-8*24*time.Hour))
	insertUsage("claude-fable-50", 50, now.Add(-30*time.Minute))
	insertUsage("claude-sonnet-5", 40, now.Add(-30*time.Minute))
	insertUsage("claude-fable-5", 9, now.Add(-15*24*time.Hour))

	migrationSQL, err := FS.ReadFile("192_backfill_fable_subscription_model_usage.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	var firstUpdatedAt time.Time
	require.NoError(t, db.QueryRowContext(ctx, "SELECT updated_at FROM user_subscriptions WHERE id = 20").Scan(&firstUpdatedAt))
	_, err = db.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	var secondUpdatedAt time.Time
	require.NoError(t, db.QueryRowContext(ctx, "SELECT updated_at FROM user_subscriptions WHERE id = 20").Scan(&secondUpdatedAt))
	require.Equal(t, firstUpdatedAt, secondUpdatedAt)

	var daily, weekly, monthly, sonnetMonthly float64
	err = db.QueryRowContext(ctx, `
SELECT
    (model_usage->'claude-fable-5'->>'daily_usage_usd')::double precision,
    (model_usage->'claude-fable-5'->>'weekly_usage_usd')::double precision,
    (model_usage->'claude-fable-5'->>'monthly_usage_usd')::double precision,
    (model_usage->'claude-sonnet-5'->>'monthly_usage_usd')::double precision
FROM user_subscriptions
WHERE id = 20
`).Scan(&daily, &weekly, &monthly, &sonnetMonthly)
	require.NoError(t, err)
	require.InDelta(t, 5, daily, 1e-9)
	require.InDelta(t, 5, weekly, 1e-9)
	require.InDelta(t, 12, monthly, 1e-9)
	require.InDelta(t, 3, sonnetMonthly, 1e-9)
}
