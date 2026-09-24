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

// TestOpus55Grok47MigrationScopeAndIdempotency exercises migration 246 against a
// PostgreSQL-compatible fixture: eligible/ineligible accounts and groups, mixed-row
// splitting, custom-row preservation, duplicate disabling, the Grok long-context
// interval boundary, the untouched account-statistics table, and a byte-identical
// second run.
func TestOpus55Grok47MigrationScopeAndIdempotency(t *testing.T) {
	ctx := context.Background()
	image := os.Getenv("SUB2API_TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:18-alpine"
	}
	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("opus55_grok47_test"),
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
 CREATE TABLE channel_model_pricing (id BIGSERIAL PRIMARY KEY,channel_id BIGINT,platform TEXT,models JSONB,billing_mode TEXT DEFAULT 'token',input_price NUMERIC,output_price NUMERIC,cache_write_price NUMERIC,cache_read_price NUMERIC,cache_write_5m_price NUMERIC,cache_write_1h_price NUMERIC,fast_multiplier NUMERIC,flex_multiplier NUMERIC,enabled BOOLEAN DEFAULT true,updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE channel_pricing_intervals (id BIGSERIAL PRIMARY KEY,pricing_id BIGINT,min_tokens INT,max_tokens INT,tier_label TEXT,input_price NUMERIC,output_price NUMERIC,cache_write_price NUMERIC,cache_read_price NUMERIC,cache_write_5m_price NUMERIC,cache_write_1h_price NUMERIC,sort_order INT);
 CREATE TABLE channel_account_stats_model_pricing (LIKE channel_model_pricing INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
 ALTER TABLE channel_account_stats_model_pricing RENAME COLUMN channel_id TO rule_id;
 CREATE TABLE channel_account_stats_pricing_intervals (LIKE channel_pricing_intervals INCLUDING DEFAULTS INCLUDING CONSTRAINTS);

 -- 1 eligible; 2 wildcard; 3 explicit override; 4 non-identity target; 5 shadow;
 -- 6 deleted; 7 wrong platform.
 INSERT INTO accounts(id,platform,type,parent_account_id,credentials,deleted_at) VALUES
 (1,'anthropic','apikey',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5"}}',NULL),
 (2,'anthropic','apikey',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5","*":"*"}}',NULL),
 (3,'anthropic','apikey',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5","claude-opus-5-5":"custom"}}',NULL),
 (4,'anthropic','apikey',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-4-8"}}',NULL),
 (5,'anthropic','apikey',1,'{"model_mapping":{"claude-opus-5":"claude-opus-5"}}',NULL),
 (6,'anthropic','apikey',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5"}}',NOW()),
 (7,'kiro','oauth',NULL,'{"model_mapping":{"claude-opus-5":"claude-opus-5"}}',NULL);

 -- 1 eligible (keeps order + enabled flag); 2 disabled-but-eligible;
 -- 3 no Opus 5; 4 Kiro platform must never gain Opus 5.5.
 INSERT INTO groups(id,platform,model_allowlist) VALUES
 (1,'anthropic','{"enabled":true,"models":["claude-sonnet-5","claude-opus-5","claude-opus-5-thinking","claude-sonnet-5"]}'),
 (2,'anthropic','{"enabled":false,"models":["claude-opus-5"]}'),
 (3,'anthropic','{"enabled":true,"models":["claude-opus-4-8"]}'),
 (4,'kiro','{"enabled":true,"models":["claude-opus-5"]}');

 -- Anthropic: ch 10 has the predecessor; ch 11 already has a custom dedicated
 -- row plus a duplicate; ch 12 has a mixed row carrying one new alias.
 INSERT INTO channel_model_pricing(id,channel_id,platform,models,input_price,output_price) VALUES
 (100,10,'anthropic','["claude-opus-5","claude-opus-5-thinking"]',0.000005,0.000025),
 (110,11,'anthropic','["claude-opus-5-5"]',0.000123,0.000456),
 (111,11,'anthropic','["claude-opus-5-5-thinking"]',0.000222,0.000333),
 (120,12,'anthropic','["claude-sonnet-5","claude-opus-5-5","claude-haiku-4-5"]',0.000321,0.000654),
 (130,13,'grok','["grok-4.6"]',0.000002,0.000006),
 (140,14,'openai','["claude-opus-5"]',0.000009,0.000009);
 INSERT INTO channel_pricing_intervals(pricing_id,min_tokens,max_tokens,input_price) VALUES (110,0,199999,0.000789);

 -- Operator-owned statistics pricing. Scope 20 already binds a new model on a
 -- dedicated row; scope 23 binds one inside a mixed row; scopes 21/22 only have
 -- the predecessor.
 INSERT INTO channel_account_stats_model_pricing(id,rule_id,platform,models,input_price,output_price) VALUES
 (200,20,'anthropic','["claude-opus-5-5"]',0.000088,0.000099),
 (210,21,'anthropic','["claude-opus-5"]',0.000005,0.000025),
 (220,22,'grok','["grok-4.6"]',0.000002,0.000006),
 (230,23,'anthropic','["claude-sonnet-5","claude-opus-5-5"]',0.000077,0.000066);
 `)
	require.NoError(t, err)

	migration, err := FS.ReadFile("246_add_opus55_grok47_support.sql")
	require.NoError(t, err)
	// Account statistics prices are the operator's upstream cost. The migration
	// may normalize an existing row but must never write a user list price
	// there, so the whole table is captured and compared verbatim below.
	statsSnapshot := func() string {
		var v string
		require.NoError(t, db.QueryRow(`SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_account_stats_model_pricing p),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_account_stats_pricing_intervals p))`).Scan(&v))
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

	// Accounts: only the eligible identity mapping gains the model.
	var value string
	require.NoError(t, db.QueryRow(`SELECT credentials->'model_mapping'->>'claude-opus-5-5' FROM accounts WHERE id=1`).Scan(&value))
	require.Equal(t, "claude-opus-5-5", value)
	require.NoError(t, db.QueryRow(`SELECT credentials->'model_mapping'->>'claude-opus-5-5' FROM accounts WHERE id=3`).Scan(&value))
	require.Equal(t, "custom", value)
	var count int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM accounts WHERE id IN (2,4,5,6,7) AND credentials->'model_mapping' ? 'claude-opus-5-5'`).Scan(&count))
	require.Zero(t, count)

	// Groups: order preserved, duplicates removed, enabled flag untouched,
	// Kiro never expanded.
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=1`).Scan(&value))
	require.JSONEq(t, `["claude-sonnet-5","claude-opus-5","claude-opus-5-thinking","claude-opus-5-5","claude-opus-5-5-thinking"]`, value)
	var enabled bool
	require.NoError(t, db.QueryRow(`SELECT (model_allowlist->>'enabled')::boolean FROM groups WHERE id=2`).Scan(&enabled))
	require.False(t, enabled)
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=3`).Scan(&value))
	require.JSONEq(t, `["claude-opus-4-8"]`, value)
	require.NoError(t, db.QueryRow(`SELECT model_allowlist->'models' FROM groups WHERE id=4`).Scan(&value))
	require.JSONEq(t, `["claude-opus-5"]`, value, "Kiro does not serve Opus 5.5")

	// Anthropic channel 10: one dedicated row at official prices, with the
	// 5m/1h breakdown and no long-context interval.
	var id int64
	var in, out, write, read, w5m, w1h float64
	require.NoError(t, db.QueryRow(
		`SELECT id,models,input_price,output_price,cache_write_price,cache_read_price,cache_write_5m_price,cache_write_1h_price
		 FROM channel_model_pricing WHERE channel_id=10 AND platform='anthropic' AND enabled AND models ? 'claude-opus-5-5'`).
		Scan(&id, &value, &in, &out, &write, &read, &w5m, &w1h))
	var aliases []string
	require.NoError(t, json.Unmarshal([]byte(value), &aliases))
	require.Equal(t, []string{"claude-opus-5-5", "claude-opus-5-5-thinking"}, aliases)
	require.InDelta(t, 4e-6, in, 1e-14)
	require.InDelta(t, 20e-6, out, 1e-14)
	require.InDelta(t, 5e-6, write, 1e-14)
	require.InDelta(t, 0.2e-6, read, 1e-14)
	require.InDelta(t, 5e-6, w5m, 1e-14)
	require.InDelta(t, 8e-6, w1h, 1e-14)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_pricing_intervals WHERE pricing_id=$1`, id).Scan(&count))
	require.Zero(t, count, "Opus 5.5 bills 1M context at standard rates")
	// The predecessor row keeps its own models and price.
	require.NoError(t, db.QueryRow(`SELECT models,input_price FROM channel_model_pricing WHERE id=100`).Scan(&value, &in))
	require.JSONEq(t, `["claude-opus-5","claude-opus-5-thinking"]`, value)
	require.InDelta(t, 5e-6, in, 1e-14)

	// Channel 11: the custom dedicated row and its interval survive; the
	// duplicate is emptied and disabled.
	require.NoError(t, db.QueryRow(`SELECT input_price FROM channel_model_pricing WHERE id=110`).Scan(&in))
	require.InDelta(t, 0.000123, in, 1e-14)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_pricing_intervals WHERE pricing_id=110`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT models,enabled FROM channel_model_pricing WHERE id=111`).Scan(&value, &enabled))
	require.JSONEq(t, `[]`, value)
	require.False(t, enabled)

	// Channel 12: the mixed row only loses the migrated alias.
	require.NoError(t, db.QueryRow(`SELECT models FROM channel_model_pricing WHERE id=120`).Scan(&value))
	require.JSONEq(t, `["claude-sonnet-5","claude-haiku-4-5"]`, value)

	// Grok channel 13: dedicated row plus the (min,max] boundary encoding 200k.
	require.NoError(t, db.QueryRow(
		`SELECT id,models,input_price,output_price,cache_read_price FROM channel_model_pricing
		 WHERE channel_id=13 AND platform='grok' AND enabled AND models ? 'grok-4.7'`).
		Scan(&id, &value, &in, &out, &read))
	require.JSONEq(t, `["grok-4.7"]`, value)
	require.InDelta(t, 2e-6, in, 1e-14)
	require.InDelta(t, 6e-6, out, 1e-14)
	require.InDelta(t, 0.5e-6, read, 1e-14)
	var minTok, maxTok sql.NullInt64
	require.NoError(t, db.QueryRow(
		`SELECT min_tokens,max_tokens,input_price,output_price,cache_read_price FROM channel_pricing_intervals
		 WHERE pricing_id=$1 AND sort_order=1`, id).Scan(&minTok, &maxTok, &in, &out, &read))
	require.EqualValues(t, 199999, minTok.Int64, "(min,max] means >=200000")
	require.False(t, maxTok.Valid)
	require.InDelta(t, 4e-6, in, 1e-14)
	require.InDelta(t, 12e-6, out, 1e-14)
	require.InDelta(t, 1e-6, read, 1e-14)

	// A non-matching platform row is never rewritten.
	require.NoError(t, db.QueryRow(`SELECT models FROM channel_model_pricing WHERE id=140`).Scan(&value))
	require.JSONEq(t, `["claude-opus-5"]`, value)

	// Account statistics holds the operator's upstream cost, so the migration
	// only normalizes it: no row is created, and an existing row gains no alias
	// that scope had not already bound. The suffixed alias in particular is
	// normalized away before the upstream call, so a statistics lookup (which
	// runs on the upstream model) could never match it anyway.
	require.NoError(t, db.QueryRow(`SELECT models,input_price FROM channel_account_stats_model_pricing WHERE id=200`).Scan(&value, &in))
	require.JSONEq(t, `["claude-opus-5-5"]`, value)
	require.InDelta(t, 0.000088, in, 1e-14, "custom statistics prices are preserved")
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM channel_account_stats_model_pricing WHERE models ?| ARRAY['claude-opus-5-5-thinking','grok-4.7']`).Scan(&count))
	require.Zero(t, count, "statistics pricing is operator-owned and not expanded from user billing")
	require.NoError(t, db.QueryRow(`SELECT models FROM channel_account_stats_model_pricing WHERE id=210`).Scan(&value))
	require.JSONEq(t, `["claude-opus-5"]`, value)
	require.NoError(t, db.QueryRow(`SELECT models FROM channel_account_stats_model_pricing WHERE id=220`).Scan(&value))
	require.JSONEq(t, `["grok-4.6"]`, value)
	// A mixed statistics row is left intact rather than split into a new row
	// that would carry the user list price as if it were an upstream cost.
	require.NoError(t, db.QueryRow(`SELECT models,input_price FROM channel_account_stats_model_pricing WHERE id=230`).Scan(&value, &in))
	require.JSONEq(t, `["claude-sonnet-5","claude-opus-5-5"]`, value)
	require.InDelta(t, 0.000077, in, 1e-14)
	// The strongest form of the rule: for this fixture the migration has no
	// business writing anything into upstream-cost accounting at all.
	require.JSONEq(t, statsBefore, statsSnapshot(),
		"the migration must not create statistics rows or write user list prices into them")

	// Running the migration twice leaves persisted state identical.
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
