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

// TestGPT6SolLunaMigrationScopeAndIdempotency covers migration 245 against a
// PostgreSQL-compatible fixture: eligible/ineligible accounts and groups, the
// Kiro-backed channel exclusion, mixed-row splitting, custom-row preservation,
// duplicate disabling, per-model dedicated rows with their own intervals, and
// byte-identical state after a second run.
func TestGPT6SolLunaMigrationScopeAndIdempotency(t *testing.T) {
	ctx := context.Background()
	image := os.Getenv("SUB2API_TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:18-alpine"
	}
	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("gpt6_sol_luna_test"),
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
 CREATE TABLE account_groups (account_id BIGINT,group_id BIGINT);
 CREATE TABLE channel_groups (channel_id BIGINT,group_id BIGINT);
 CREATE TABLE channel_account_stats_pricing_rules (id BIGINT PRIMARY KEY, channel_id BIGINT);
 CREATE TABLE channel_model_pricing (id BIGSERIAL PRIMARY KEY,channel_id BIGINT,platform TEXT,models JSONB,billing_mode TEXT DEFAULT 'token',input_price NUMERIC,output_price NUMERIC,cache_write_price NUMERIC,cache_read_price NUMERIC,cache_write_5m_price NUMERIC,cache_write_1h_price NUMERIC,fast_multiplier NUMERIC,flex_multiplier NUMERIC,enabled BOOLEAN DEFAULT true,updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE channel_pricing_intervals (id BIGSERIAL PRIMARY KEY,pricing_id BIGINT,min_tokens INT,max_tokens INT,tier_label TEXT,input_price NUMERIC,output_price NUMERIC,cache_write_price NUMERIC,cache_read_price NUMERIC,cache_write_5m_price NUMERIC,cache_write_1h_price NUMERIC,sort_order INT);
 CREATE TABLE channel_account_stats_model_pricing (LIKE channel_model_pricing INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
 ALTER TABLE channel_account_stats_model_pricing RENAME COLUMN channel_id TO rule_id;
 CREATE TABLE channel_account_stats_pricing_intervals (LIKE channel_pricing_intervals INCLUDING DEFAULTS INCLUDING CONSTRAINTS);

 -- 1 eligible; 2 wildcard; 3 explicit override; 4 Kiro; 5 API key; 6 no Astra; 7 shadow; 8 deleted.
 INSERT INTO accounts(id,platform,type,parent_account_id,credentials,deleted_at) VALUES
 (1,'openai','oauth',NULL,'{"model_mapping":{"gpt-5.6-sol":"gpt-5.6-sol","gpt-6-astra":"gpt-6-astra"}}',NULL),
 (2,'openai','oauth',NULL,'{"model_mapping":{"gpt-6-astra":"gpt-6-astra","*":"*"}}',NULL),
 (3,'openai','oauth',NULL,'{"model_mapping":{"gpt-6-astra":"gpt-6-astra","gpt-6-sol":"custom","gpt-6-luna":"custom"}}',NULL),
 (4,'kiro','oauth',NULL,'{"model_mapping":{"gpt-6-astra":"gpt-6-astra"}}',NULL),
 (5,'openai','apikey',NULL,'{"model_mapping":{"gpt-6-astra":"gpt-6-astra"}}',NULL),
 (6,'openai','oauth',NULL,'{"model_mapping":{"gpt-5.6-sol":"gpt-5.6-sol"}}',NULL),
 (7,'openai','oauth',1,'{"model_mapping":{"gpt-6-astra":"gpt-6-astra"}}',NULL),
 (8,'openai','oauth',NULL,'{"model_mapping":{"gpt-6-astra":"gpt-6-astra"}}',NOW());

 -- 1 eligible; 2 Kiro-backed (Astra listed but no direct OpenAI OAuth member);
 -- 3 disabled-but-eligible; 4 no Astra in the allowlist.
 INSERT INTO groups(id,platform,model_allowlist) VALUES
 (1,'openai','{"enabled":true,"models":["gpt-5.5","gpt-6-astra","gpt-5.5"]}'),
 (2,'openai','{"enabled":true,"models":["gpt-6-astra"]}'),
 (3,'openai','{"enabled":false,"models":["gpt-6-astra"]}'),
 (4,'openai','{"enabled":true,"models":["gpt-5.6-sol"]}');
 INSERT INTO account_groups VALUES (1,1),(4,2),(1,3),(1,4);
 -- channel 10 -> group 1 (direct OAuth); channel 12 -> group 2 (Kiro only).
 INSERT INTO channel_groups VALUES (10,1),(11,1),(12,2);

 INSERT INTO channel_model_pricing(id,channel_id,platform,models,input_price,output_price) VALUES
 (100,10,'openai','["gpt-6-astra"]',0.000010,0.000050),
 (101,10,'openai','["gpt-5.5","gpt-6-sol","gpt-5.4"]',0.000321,0.000654),
 (110,11,'openai','["gpt-6-sol","gpt-6-sol"]',0.000123,0.000456),
 (111,11,'openai','["gpt-6-sol"]',0.000222,0.000333),
 (120,12,'openai','["gpt-6-astra"]',0.000010,0.000050),
 (130,13,'kiro','["gpt-6-sol"]',0.000111,0.000222);
 INSERT INTO channel_pricing_intervals(pricing_id,min_tokens,input_price,cache_write_1h_price) VALUES (110,0,0.000789,0.000987);

 INSERT INTO channel_account_stats_pricing_rules VALUES (20,10),(21,10);
 INSERT INTO channel_account_stats_model_pricing(id,rule_id,platform,models,input_price,output_price) VALUES
 (200,20,'openai','["gpt-6-astra"]',0.000004,0.000025),
 (210,21,'openai','["gpt-6-luna"]',0.000088,0.000099);
 INSERT INTO channel_account_stats_pricing_intervals(pricing_id,min_tokens,input_price) VALUES (210,0,0.000066);
 `)
	require.NoError(t, err)

	migration, err := FS.ReadFile("245_add_gpt6_sol_luna_support.sql")
	require.NoError(t, err)
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

	// Accounts: only the eligible direct OAuth account gains both models.
	var value string
	require.NoError(t, db.QueryRow(`SELECT credentials->'model_mapping'->>'gpt-6-sol' FROM accounts WHERE id=1`).Scan(&value))
	require.Equal(t, "gpt-6-sol", value)
	require.NoError(t, db.QueryRow(`SELECT credentials->'model_mapping'->>'gpt-6-luna' FROM accounts WHERE id=1`).Scan(&value))
	require.Equal(t, "gpt-6-luna", value)
	require.NoError(t, db.QueryRow(`SELECT credentials->'model_mapping'->>'gpt-6-sol' FROM accounts WHERE id=3`).Scan(&value))
	require.Equal(t, "custom", value)
	var count int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM accounts WHERE id IN (2,4,5,6,7,8) AND (credentials->'model_mapping' ? 'gpt-6-sol' OR credentials->'model_mapping' ? 'gpt-6-luna')`,
	).Scan(&count))
	require.Zero(t, count)

	// Groups: order preserved, duplicates removed, enabled flag untouched.
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=1`).Scan(&value))
	require.JSONEq(t, `["gpt-5.5","gpt-6-astra","gpt-6-sol","gpt-6-luna"]`, value)
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=2`).Scan(&value))
	require.JSONEq(t, `["gpt-6-astra"]`, value, "Kiro-backed OpenAI group must not advertise gpt-6-*")
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=4`).Scan(&value))
	require.JSONEq(t, `["gpt-5.6-sol"]`, value)
	var enabled bool
	require.NoError(t, db.QueryRow(`SELECT (model_allowlist->>'enabled')::boolean FROM groups WHERE id=3`).Scan(&enabled))
	require.False(t, enabled)
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=3`).Scan(&value))
	require.JSONEq(t, `["gpt-6-astra","gpt-6-sol","gpt-6-luna"]`, value)

	// Channel 10: one enabled dedicated row per model, with official prices and intervals.
	for _, tc := range []struct {
		model                      string
		input, output, write, read float64
	}{
		{"gpt-6-sol", 2e-6, 10e-6, 2.5e-6, 0.2e-6},
		{"gpt-6-luna", 0.1e-6, 0.5e-6, 0.125e-6, 0.01e-6},
	} {
		var rows int
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM channel_model_pricing WHERE channel_id=10 AND enabled AND models ? $1`, tc.model).Scan(&rows))
		require.Equal(t, 1, rows, tc.model)

		var id int64
		var models string
		var in, out, write, read, fast, flex float64
		require.NoError(t, db.QueryRow(
			`SELECT id,models,input_price,output_price,cache_write_price,cache_read_price,fast_multiplier,flex_multiplier
			 FROM channel_model_pricing WHERE channel_id=10 AND enabled AND models ? $1`, tc.model).
			Scan(&id, &models, &in, &out, &write, &read, &fast, &flex))
		var aliases []string
		require.NoError(t, json.Unmarshal([]byte(models), &aliases))
		require.Equal(t, []string{tc.model}, aliases, "dedicated row carries only the official id")
		require.InDelta(t, tc.input, in, 1e-14)
		require.InDelta(t, tc.output, out, 1e-14)
		require.InDelta(t, tc.write, write, 1e-14)
		require.InDelta(t, tc.read, read, 1e-14)
		require.InDelta(t, 2, fast, 1e-9)
		require.InDelta(t, 0.5, flex, 1e-9)

		var std, long float64
		require.NoError(t, db.QueryRow(
			`SELECT input_price FROM channel_pricing_intervals WHERE pricing_id=$1 AND tier_label='Standard'`, id).Scan(&std))
		require.InDelta(t, tc.input, std, 1e-14)
		require.NoError(t, db.QueryRow(
			`SELECT input_price FROM channel_pricing_intervals WHERE pricing_id=$1 AND tier_label='>272K'`, id).Scan(&long))
		require.InDelta(t, tc.input*2, long, 1e-14)
		require.NoError(t, db.QueryRow(
			`SELECT output_price FROM channel_pricing_intervals WHERE pricing_id=$1 AND tier_label='>272K'`, id).Scan(&long))
		require.InDelta(t, tc.output*1.5, long, 1e-14)
	}

	// Mixed row 101 keeps its other models and prices, minus the migrated alias.
	require.NoError(t, db.QueryRow(`SELECT models FROM channel_model_pricing WHERE id=101`).Scan(&value))
	require.JSONEq(t, `["gpt-5.5","gpt-5.4"]`, value)
	var price float64
	require.NoError(t, db.QueryRow(`SELECT input_price FROM channel_model_pricing WHERE id=101`).Scan(&price))
	require.InDelta(t, 0.000321, price, 1e-14)

	// Channel 11 already had a dedicated custom Sol row: prices and intervals survive,
	// and the duplicate row is emptied and disabled.
	require.NoError(t, db.QueryRow(`SELECT input_price FROM channel_model_pricing WHERE id=110`).Scan(&price))
	require.InDelta(t, 0.000123, price, 1e-14)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_pricing_intervals WHERE pricing_id=110`).Scan(&count))
	require.Equal(t, 1, count, "custom intervals are preserved, not replaced")
	require.NoError(t, db.QueryRow(`SELECT models,enabled FROM channel_model_pricing WHERE id=111`).Scan(&value, &enabled))
	require.JSONEq(t, `[]`, value)
	require.False(t, enabled)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_model_pricing WHERE channel_id=11 AND models ? 'gpt-6-luna'`).Scan(&count))
	require.Zero(t, count, "a Sol-only custom scope must not gain a Luna row")

	// Kiro-backed channel 12 and the non-OpenAI row 130 are untouched.
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM channel_model_pricing WHERE channel_id=12 AND (models ? 'gpt-6-sol' OR models ? 'gpt-6-luna')`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT models FROM channel_model_pricing WHERE id=130`).Scan(&value))
	require.JSONEq(t, `["gpt-6-sol"]`, value)

	// Account statistics: only the scope that already bound Luna is normalized;
	// Astra-only scope 20 is never expanded from user billing.
	require.NoError(t, db.QueryRow(`SELECT models FROM channel_account_stats_model_pricing WHERE id=200`).Scan(&value))
	require.JSONEq(t, `["gpt-6-astra"]`, value)
	require.NoError(t, db.QueryRow(`SELECT models,input_price FROM channel_account_stats_model_pricing WHERE id=210`).Scan(&value, &price))
	require.JSONEq(t, `["gpt-6-luna"]`, value)
	require.InDelta(t, 0.000088, price, 1e-14)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_account_stats_model_pricing WHERE rule_id=21 AND models ? 'gpt-6-sol'`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_account_stats_pricing_intervals WHERE pricing_id=210`).Scan(&count))
	require.Equal(t, 1, count)

	// Running the migration twice leaves the persisted state identical.
	snapshot := func() string {
		var v string
		require.NoError(t, db.QueryRow(`SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_model_pricing p),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_account_stats_model_pricing p),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_pricing_intervals p),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_account_stats_pricing_intervals p),
 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
 (SELECT jsonb_agg(to_jsonb(g) ORDER BY id) FROM groups g))`).Scan(&v))
		return v
	}
	before := snapshot()
	run()
	require.JSONEq(t, before, snapshot())
}
