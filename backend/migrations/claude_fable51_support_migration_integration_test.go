//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestMigration231AddsFable51SupportIdempotently(t *testing.T) {
	ctx := context.Background()
	postgresImage := strings.TrimSpace(os.Getenv("SUB2API_TEST_POSTGRES_IMAGE"))
	if postgresImage == "" {
		postgresImage = "postgres:15-alpine"
	}
	container, err := tcpostgres.Run(
		ctx,
		postgresImage,
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
CREATE TABLE accounts (
    id BIGINT PRIMARY KEY,
    platform TEXT NOT NULL,
    credentials JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE TABLE groups (
    id BIGINT PRIMARY KEY,
    platform TEXT NOT NULL,
    models_list_config JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE TABLE channels (
    id BIGINT PRIMARY KEY,
    name TEXT NOT NULL
);
CREATE TABLE channel_model_pricing (
    id BIGSERIAL PRIMARY KEY,
    channel_id BIGINT NOT NULL REFERENCES channels(id),
    platform TEXT NOT NULL,
    models JSONB NOT NULL DEFAULT '[]'::jsonb,
    billing_mode TEXT NOT NULL DEFAULT 'token',
    input_price NUMERIC(20,12),
    output_price NUMERIC(20,12),
    cache_write_price NUMERIC(20,12),
    cache_write_5m_price NUMERIC(20,12),
    cache_write_1h_price NUMERIC(20,12),
    cache_read_price NUMERIC(20,12),
    image_output_price NUMERIC(20,8),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE channel_pricing_intervals (
    id BIGINT PRIMARY KEY,
    pricing_id BIGINT NOT NULL REFERENCES channel_model_pricing(id),
    marker NUMERIC(20,12) NOT NULL
);

INSERT INTO accounts (id, platform, credentials) VALUES
    (1, 'anthropic', '{"model_mapping":{"claude-fable-5":"claude-fable-5"}}'),
    (2, 'anthropic', '{"model_mapping":{"*":"*","claude-fable-5":"claude-fable-5"}}'),
    (3, 'kiro',      '{"model_mapping":{"claude-fable-5":"claude-fable-5"}}'),
    (4, 'anthropic', '{"model_mapping":{"claude-sonnet-5":"claude-sonnet-5"}}');

INSERT INTO groups (id, platform, models_list_config) VALUES
    (1, 'anthropic', '{"enabled":true,"models":["claude-sonnet-5","claude-fable-5","claude-sonnet-5"]}'),
    (2, 'kiro',      '{"enabled":true,"models":["claude-fable-5"]}'),
    (3, 'anthropic', '{"enabled":true,"models":["claude-sonnet-5"]}');

INSERT INTO channels (id, name) VALUES (10, 'latest'), (11, 'custom');
INSERT INTO channel_model_pricing (
    id, channel_id, platform, models, billing_mode, input_price, output_price,
    cache_write_price, cache_write_5m_price, cache_write_1h_price,
    cache_read_price, image_output_price, enabled
) VALUES
    (100, 10, 'anthropic', '["claude-fable-5","claude-fable-5-1","claude-sonnet-5"]', 'token', 0.000010, 0.000050, 0.0000125, 0.0000125, 0.000020, 0.000001, 0, true),
    (101, 10, 'kiro',      '["claude-fable-5"]', 'token', 0.000010, 0.000050, 0.0000125, 0.0000125, 0.000020, 0.000001, 0, true),
    (200, 11, 'anthropic', '["claude-fable-5-1","claude-fable-5-1"]', 'token', 0.000123, 0.000456, 0.000789, 0.000789, 0.000987, 0.000321, 0, true),
    (201, 11, 'anthropic', '["claude-fable-5-1"]', 'token', 0.000222, 0.000333, 0.000444, 0.000444, 0.000555, 0.000111, 0, true);
INSERT INTO channel_pricing_intervals (id, pricing_id, marker) VALUES (1, 200, 0.000777);
`)
	require.NoError(t, err)

	migrationSQL, err := FS.ReadFile("231_add_claude_fable51_support.sql")
	require.NoError(t, err)
	runMigration := func() {
		t.Helper()
		tx, txErr := db.BeginTx(ctx, nil)
		require.NoError(t, txErr)
		_, txErr = tx.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, txErr)
		require.NoError(t, tx.Commit())
	}
	runMigration()

	var mapping string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials->'model_mapping' FROM accounts WHERE id = 1`).Scan(&mapping))
	require.JSONEq(t, `{"claude-fable-5":"claude-fable-5","claude-fable-5-1":"claude-fable-5-1"}`, mapping)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials->'model_mapping' FROM accounts WHERE id = 2`).Scan(&mapping))
	require.NotContains(t, mapping, "claude-fable-5-1")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials->'model_mapping' FROM accounts WHERE id = 3`).Scan(&mapping))
	require.NotContains(t, mapping, "claude-fable-5-1")

	var models string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT models_list_config->'models' FROM groups WHERE id = 1`).Scan(&models))
	require.JSONEq(t, `["claude-sonnet-5","claude-fable-5","claude-fable-5-1"]`, models)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT models_list_config->'models' FROM groups WHERE id = 2`).Scan(&models))
	require.JSONEq(t, `["claude-fable-5"]`, models)

	var activeRules int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM channel_model_pricing
WHERE channel_id = 10 AND platform = 'anthropic' AND enabled
  AND models = '["claude-fable-5-1"]'::jsonb
`).Scan(&activeRules))
	require.Equal(t, 1, activeRules)

	var input, output, write5m, write1h, read float64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT input_price, output_price, cache_write_5m_price, cache_write_1h_price, cache_read_price
FROM channel_model_pricing
WHERE channel_id = 10 AND platform = 'anthropic' AND enabled
  AND models = '["claude-fable-5-1"]'::jsonb
`).Scan(&input, &output, &write5m, &write1h, &read))
	require.InDelta(t, 15e-6, input, 1e-12)
	require.InDelta(t, 75e-6, output, 1e-12)
	require.InDelta(t, 18.75e-6, write5m, 1e-12)
	require.InDelta(t, 30e-6, write1h, 1e-12)
	require.InDelta(t, 0.25e-6, read, 1e-12)

	require.NoError(t, db.QueryRowContext(ctx, `SELECT models FROM channel_model_pricing WHERE id = 100`).Scan(&models))
	require.JSONEq(t, `["claude-fable-5","claude-sonnet-5"]`, models)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT models, input_price FROM channel_model_pricing WHERE id = 200`).Scan(&models, &input))
	require.JSONEq(t, `["claude-fable-5-1"]`, models)
	require.InDelta(t, 0.000123, input, 1e-12)

	var enabled bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT models, enabled FROM channel_model_pricing WHERE id = 201`).Scan(&models, &enabled))
	require.JSONEq(t, `[]`, models)
	require.False(t, enabled)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT marker FROM channel_pricing_intervals WHERE id = 1`).Scan(&input))
	require.InDelta(t, 0.000777, input, 1e-12)

	var firstSnapshot string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT jsonb_agg(jsonb_build_object(
    'id', id, 'channel_id', channel_id, 'platform', platform,
    'models', models, 'input_price', input_price, 'enabled', enabled
) ORDER BY id)::text
FROM channel_model_pricing
`).Scan(&firstSnapshot))
	runMigration()
	var secondSnapshot string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT jsonb_agg(jsonb_build_object(
    'id', id, 'channel_id', channel_id, 'platform', platform,
    'models', models, 'input_price', input_price, 'enabled', enabled
) ORDER BY id)::text
FROM channel_model_pricing
`).Scan(&secondSnapshot))
	require.JSONEq(t, firstSnapshot, secondSnapshot)
}
