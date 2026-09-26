//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestKiroOpus55MigrationScopeAndIdempotency exercises migration 248: Kiro-only
// scope (Anthropic rows are left to migration 246), the dotted Kiro upstream
// ID, explicit-key preservation, allowlist order/dedup, dedicated pricing rows,
// mixed-row splitting, custom-row preservation, duplicate disabling, the
// untouched account-statistics table and a byte-identical second run.
func TestKiroOpus55MigrationScopeAndIdempotency(t *testing.T) {
	ctx := context.Background()
	image := os.Getenv("SUB2API_TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:18-alpine"
	}
	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("kiro_opus55_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	_, err = db.Exec(`
 CREATE TABLE accounts (id BIGINT PRIMARY KEY,platform TEXT,type TEXT,parent_account_id BIGINT,credentials JSONB,deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE groups (id BIGINT PRIMARY KEY,platform TEXT,model_allowlist JSONB,deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE channel_model_pricing (id BIGSERIAL PRIMARY KEY,channel_id BIGINT,platform TEXT,models JSONB,billing_mode TEXT DEFAULT 'token',input_price NUMERIC,output_price NUMERIC,cache_write_price NUMERIC,cache_read_price NUMERIC,cache_write_5m_price NUMERIC,cache_write_1h_price NUMERIC,enabled BOOLEAN DEFAULT true,updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE channel_pricing_intervals (id BIGSERIAL PRIMARY KEY,pricing_id BIGINT,min_tokens INT,max_tokens INT,tier_label TEXT,input_price NUMERIC);
 CREATE TABLE channel_account_stats_model_pricing (LIKE channel_model_pricing INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
 ALTER TABLE channel_account_stats_model_pricing RENAME COLUMN channel_id TO rule_id;

 -- 1 eligible Kiro; 2 wildcard; 3 explicit -thinking override; 4 non-identity
 -- Opus 5 target; 5 shadow; 6 deleted; 7 Anthropic (left to migration 246).
 INSERT INTO accounts(id,platform,type,parent_account_id,credentials,deleted_at) VALUES
 (1,'kiro','oauth',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5","claude-opus-4-8":"claude-opus-4.8"}}',NULL),
 (2,'kiro','oauth',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5","*":"*"}}',NULL),
 (3,'kiro','oauth',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5","claude-opus-5-5-thinking":"custom"}}',NULL),
 (4,'kiro','oauth',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-4.8"}}',NULL),
 (5,'kiro','oauth',1,'{"model_mapping":{"claude-opus-5":"claude-opus-5"}}',NULL),
 (6,'kiro','oauth',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5"}}',NOW()),
 (7,'anthropic','apikey',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5"}}',NULL);

 -- 1 enforced Kiro allowlist; 2 advisory Kiro allowlist; 3 Kiro without Opus 5;
 -- 4 Anthropic group (not this migration's business).
 INSERT INTO groups(id,platform,model_allowlist) VALUES
 (1,'kiro','{"enabled":true,"models":["claude-sonnet-5","claude-opus-5","claude-opus-5-thinking","claude-sonnet-5"]}'),
 (2,'kiro','{"enabled":false,"models":["claude-opus-5"]}'),
 (3,'kiro','{"enabled":true,"models":["claude-opus-4-8"]}'),
 (4,'anthropic','{"enabled":true,"models":["claude-opus-5"]}');

 -- Kiro: ch 11 has the predecessor; ch 12 a custom dedicated row plus a
 -- duplicate; ch 13 a mixed row carrying one new alias. Anthropic ch 6 must not
 -- be touched.
 INSERT INTO channel_model_pricing(id,channel_id,platform,models,input_price,output_price) VALUES
 (242,11,'kiro','["claude-opus-5","claude-opus-5-thinking"]',0.000005,0.000025),
 (300,12,'kiro','["claude-opus-5-5"]',0.000123,0.000456),
 (301,12,'kiro','["claude-opus-5-5-thinking"]',0.000222,0.000333),
 (400,13,'kiro','["claude-sonnet-5","claude-opus-5-5"]',0.000321,0.000654),
 (500,6,'anthropic','["claude-opus-5"]',0.000005,0.000025);
 INSERT INTO channel_pricing_intervals(pricing_id,min_tokens,max_tokens,tier_label,input_price) VALUES (300,0,199999,'',0.000789);

 INSERT INTO channel_account_stats_model_pricing(id,rule_id,platform,models,input_price,output_price) VALUES
 (900,20,'kiro','["claude-opus-5"]',0.000005,0.000025),
 (910,21,'kiro','["claude-sonnet-5","claude-opus-5-5"]',0.000077,0.000066);
 `)
	require.NoError(t, err)

	migration, err := FS.ReadFile("248_add_kiro_opus55_support.sql")
	require.NoError(t, err)
	statsSnapshot := func() string {
		var v string
		require.NoError(t, db.QueryRow(`SELECT jsonb_agg(to_jsonb(p) ORDER BY id)::text FROM channel_account_stats_model_pricing p`).Scan(&v))
		return v
	}
	statsBefore := statsSnapshot()
	run := func() {
		tx, e := db.BeginTx(ctx, nil)
		require.NoError(t, e)
		if _, e = tx.Exec(string(migration)); e != nil {
			_ = tx.Rollback()
		}
		require.NoError(t, e)
		require.NoError(t, tx.Commit())
	}
	run()

	mapping := func(id int) map[string]string {
		var raw string
		require.NoError(t, db.QueryRow(`SELECT credentials->>'model_mapping' FROM accounts WHERE id=$1`, id).Scan(&raw))
		out := map[string]string{}
		require.NoError(t, json.Unmarshal([]byte(raw), &out))
		return out
	}

	// Accounts: the eligible Kiro account maps both aliases to the dotted
	// upstream ID and keeps its other keys.
	m := mapping(1)
	require.Equal(t, "claude-opus-5.5", m["claude-opus-5-5"])
	require.Equal(t, "claude-opus-5.5", m["claude-opus-5-5-thinking"])
	require.Equal(t, "claude-opus-5", m["claude-opus-5"])
	require.Equal(t, "claude-opus-4.8", m["claude-opus-4-8"])
	// Explicit operator value for one alias is preserved; the missing one is added.
	m = mapping(3)
	require.Equal(t, "custom", m["claude-opus-5-5-thinking"])
	require.Equal(t, "claude-opus-5.5", m["claude-opus-5-5"])
	for _, id := range []int{2, 4, 5, 6, 7} {
		_, has := mapping(id)["claude-opus-5-5"]
		require.False(t, has, "account %d must not gain Opus 5.5", id)
	}

	var value string
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=1`).Scan(&value))
	require.JSONEq(t, `["claude-sonnet-5","claude-opus-5","claude-opus-5-thinking","claude-opus-5-5","claude-opus-5-5-thinking"]`, value)
	var enabled bool
	require.NoError(t, db.QueryRow(`SELECT (model_allowlist->>'enabled')::boolean FROM groups WHERE id=1`).Scan(&enabled))
	require.True(t, enabled, "the enforced allowlist stays enforced")
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=2`).Scan(&value))
	require.JSONEq(t, `["claude-opus-5","claude-opus-5-5","claude-opus-5-5-thinking"]`, value)
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=3`).Scan(&value))
	require.JSONEq(t, `["claude-opus-4-8"]`, value)
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=4`).Scan(&value))
	require.JSONEq(t, `["claude-opus-5"]`, value, "Anthropic groups belong to migration 246")

	// Kiro channel 11: exactly one dedicated enabled row at the official price.
	var in, out, write, read, w5m, w1h float64
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_model_pricing WHERE channel_id=11 AND platform='kiro' AND enabled AND models ?| ARRAY['claude-opus-5-5','claude-opus-5-5-thinking']`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(
		`SELECT models,input_price,output_price,cache_write_price,cache_read_price,cache_write_5m_price,cache_write_1h_price
		 FROM channel_model_pricing WHERE channel_id=11 AND platform='kiro' AND enabled AND models ? 'claude-opus-5-5'`).
		Scan(&value, &in, &out, &write, &read, &w5m, &w1h))
	require.JSONEq(t, `["claude-opus-5-5","claude-opus-5-5-thinking"]`, value)
	require.InDelta(t, 4e-6, in, 1e-14)
	require.InDelta(t, 20e-6, out, 1e-14)
	require.InDelta(t, 5e-6, write, 1e-14)
	require.InDelta(t, 0.2e-6, read, 1e-14)
	require.InDelta(t, 5e-6, w5m, 1e-14)
	require.InDelta(t, 8e-6, w1h, 1e-14)
	// The Opus 5 row is untouched.
	require.NoError(t, db.QueryRow(`SELECT models,input_price FROM channel_model_pricing WHERE id=242`).Scan(&value, &in))
	require.JSONEq(t, `["claude-opus-5","claude-opus-5-thinking"]`, value)
	require.InDelta(t, 5e-6, in, 1e-14)

	// Channel 12: custom dedicated row, price and interval survive; the
	// duplicate is emptied and disabled.
	require.NoError(t, db.QueryRow(`SELECT models,input_price FROM channel_model_pricing WHERE id=300`).Scan(&value, &in))
	require.JSONEq(t, `["claude-opus-5-5","claude-opus-5-5-thinking"]`, value)
	require.InDelta(t, 0.000123, in, 1e-14)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_pricing_intervals WHERE pricing_id=300`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT models,enabled FROM channel_model_pricing WHERE id=301`).Scan(&value, &enabled))
	require.JSONEq(t, `[]`, value)
	require.False(t, enabled)

	// Channel 13: the mixed row only loses the migrated alias, and a new
	// dedicated row carries the full alias set.
	require.NoError(t, db.QueryRow(`SELECT models FROM channel_model_pricing WHERE id=400`).Scan(&value))
	require.JSONEq(t, `["claude-sonnet-5"]`, value)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_model_pricing WHERE channel_id=13 AND enabled AND models ? 'claude-opus-5-5'`).Scan(&count))
	require.Equal(t, 1, count)

	// Anthropic channel pricing is not this migration's business.
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_model_pricing WHERE platform='anthropic'`).Scan(&count))
	require.Equal(t, 1, count)

	// Account statistics: never created or repriced; the mixed row there stays
	// intact because it already binds the base alias on a non-dedicated row.
	require.JSONEq(t, statsBefore, statsSnapshot())

	// A second run leaves every table byte-identical.
	snapshot := func() string {
		var v string
		require.NoError(t, db.QueryRow(`SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(p) - 'updated_at' ORDER BY id) FROM channel_model_pricing p),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_pricing_intervals p),
 (SELECT jsonb_agg(to_jsonb(a) - 'updated_at' ORDER BY id) FROM accounts a),
 (SELECT jsonb_agg(to_jsonb(g) - 'updated_at' ORDER BY id) FROM groups g),
 (SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM channel_account_stats_model_pricing s))::text`).Scan(&v))
		return v
	}
	before := snapshot()
	run()
	require.JSONEq(t, before, snapshot())
}
