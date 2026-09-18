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

func TestMigration242ConvertsAndBackfillsSharedFableQuotaIdempotently(t *testing.T) {
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
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
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
VALUES ('178_subscription_model_quota_ratios.sql', $1)
`, now.Add(-10*24*time.Hour))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO groups (id, status, subscription_type, model_quota_ratios)
VALUES (10, 'active', 'subscription', '{"claude-fable-5-1":0.5}'::jsonb)
`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO user_subscriptions (
    id, group_id, starts_at, expires_at, status,
    daily_window_start, weekly_window_start, monthly_window_start,
    quota_cycle_start_at, quota_cycle_end_at, model_usage
)
VALUES (
	20, 10, $1, $2, 'active', $3, $4, $5, $1, $2,
    '{"claude-fable-5-1":{"daily_usage_usd":1,"weekly_usage_usd":2,"monthly_usage_usd":4}}'::jsonb
)
`, now.Add(-20*24*time.Hour), now.Add(10*24*time.Hour), now.Add(-12*time.Hour),
		now.Add(-3*24*time.Hour), now.Add(-15*24*time.Hour))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO usage_logs (subscription_id, requested_model, model, actual_cost, created_at)
VALUES
	(20, 'claude-fable-5', 'claude-fable-5', 2, $1),
	(20, 'claude-fable-5-1', 'claude-fable-5-1', 3, $2),
	(20, 'claude-fable-5-2', 'claude-fable-5-2', 100, $2)
`, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)

	migrationSQL, err := FS.ReadFile("242_subscription_model_quota_groups.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	var ratios, groups string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT model_quota_ratios::text, model_quota_groups::text FROM groups WHERE id = 10
`).Scan(&ratios, &groups))
	require.JSONEq(t, `{}`, ratios)
	require.JSONEq(t, `[{
        "id":"fable-5-shared",
        "name":"Claude Fable 5 + 5.1",
        "models":["claude-fable-5","claude-fable-5-1"],
        "ratio":0.5
    }]`, groups)

	var daily, weekly, monthly float64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT
    (model_usage->'group:fable-5-shared'->>'daily_usage_usd')::double precision,
    (model_usage->'group:fable-5-shared'->>'weekly_usage_usd')::double precision,
    (model_usage->'group:fable-5-shared'->>'monthly_usage_usd')::double precision
FROM user_subscriptions WHERE id = 20
`).Scan(&daily, &weekly, &monthly))
	require.InDelta(t, 5, daily, 1e-9)
	require.InDelta(t, 5, weekly, 1e-9)
	require.InDelta(t, 5, monthly, 1e-9)

	_, err = db.ExecContext(ctx, `
UPDATE user_subscriptions
SET model_usage = jsonb_set(model_usage, '{group:fable-5-shared,daily_usage_usd}', '9'::jsonb),
    updated_at = NOW()
WHERE id = 20
`)
	require.NoError(t, err)
	var before time.Time
	require.NoError(t, db.QueryRowContext(ctx, "SELECT updated_at FROM user_subscriptions WHERE id = 20").Scan(&before))
	time.Sleep(10 * time.Millisecond)
	_, err = db.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	var after time.Time
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT updated_at FROM user_subscriptions WHERE id = 20
`).Scan(&after))
	require.Equal(t, before, after)
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT (model_usage->'group:fable-5-shared'->>'daily_usage_usd')::double precision
FROM user_subscriptions WHERE id = 20
`).Scan(&daily))
	require.InDelta(t, 9, daily, 1e-9)
}
