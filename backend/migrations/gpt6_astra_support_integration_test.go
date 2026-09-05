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

func TestAstraMigrationIdempotentPricingAndScope(t *testing.T) {
	ctx := context.Background()
	image := os.Getenv("SUB2API_TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:18-alpine"
	}
	container, err := tcpostgres.Run(ctx, image, tcpostgres.WithDatabase("astra_test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("test"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`
 CREATE TABLE accounts (id BIGINT PRIMARY KEY,platform TEXT,type TEXT,parent_account_id BIGINT,credentials JSONB,deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE groups (id BIGINT PRIMARY KEY,platform TEXT,models_list_config JSONB,deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE account_groups (account_id BIGINT,group_id BIGINT);
 CREATE TABLE channel_groups (channel_id BIGINT,group_id BIGINT);
 CREATE TABLE channel_account_stats_pricing_rules (id BIGINT PRIMARY KEY, channel_id BIGINT);
 CREATE TABLE channel_model_pricing (id BIGSERIAL PRIMARY KEY,channel_id BIGINT,platform TEXT,models JSONB,billing_mode TEXT DEFAULT 'token',input_price NUMERIC,output_price NUMERIC,cache_write_price NUMERIC,cache_read_price NUMERIC,cache_write_5m_price NUMERIC,cache_write_1h_price NUMERIC,fast_multiplier NUMERIC,flex_multiplier NUMERIC,enabled BOOLEAN DEFAULT true,updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE channel_pricing_intervals (id BIGSERIAL PRIMARY KEY,pricing_id BIGINT,min_tokens INT,max_tokens INT,tier_label TEXT,input_price NUMERIC,output_price NUMERIC,cache_write_price NUMERIC,cache_read_price NUMERIC,cache_write_5m_price NUMERIC,cache_write_1h_price NUMERIC,sort_order INT);
 CREATE TABLE channel_account_stats_model_pricing (LIKE channel_model_pricing INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
 ALTER TABLE channel_account_stats_model_pricing RENAME COLUMN channel_id TO rule_id;
 CREATE TABLE channel_account_stats_pricing_intervals (LIKE channel_pricing_intervals INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
 INSERT INTO accounts(id,platform,type,credentials) VALUES
 (1,'openai','oauth','{"model_mapping":{"gpt-5.6-sol":"gpt-5.6-sol"}}'),
 (2,'openai','oauth','{"model_mapping":{"gpt-5.6-sol":"gpt-5.6-sol","*":"*"}}'),
 (3,'openai','oauth','{"model_mapping":{"gpt-5.6-sol":"gpt-5.6-sol","gpt-6-astra":"custom"}}'),
 (4,'kiro','oauth','{"model_mapping":{"gpt-5.6-sol":"gpt-5.6-sol"}}'),
 (5,'openai','apikey','{"model_mapping":{"gpt-5.6-sol":"gpt-5.6-sol"}}'),
 (6,'openai','oauth','{"model_mapping":{"gpt-5.6-sol":"gpt-5.6-terra"}}');
 INSERT INTO groups(id,platform,models_list_config) VALUES
 (1,'openai','{"enabled":true,"models":["gpt-5.5","gpt-5.6-sol","gpt-5.5"]}'),
 (2,'openai','{"enabled":true,"models":["gpt-5.6-sol"]}'),
 (3,'openai','{"enabled":false,"models":["gpt-5.6-sol"]}');
 INSERT INTO account_groups VALUES (1,1),(4,2),(1,3);
 INSERT INTO channel_groups VALUES (10,1),(11,1),(12,2);
 INSERT INTO channel_model_pricing(id,channel_id,platform,models,input_price,output_price) VALUES
 (100,10,'openai','["gpt-5.6-sol","gpt-6-astra-high"]',0.000005,0.000030),
 (110,11,'openai','["gpt-6-astra","gpt-6-astra"]',0.000123,0.000456),
 (111,11,'openai','["gpt-6-astra-max"]',0.000222,0.000333),
 (120,12,'openai','["gpt-5.6-sol"]',0.000005,0.000030);
 INSERT INTO channel_pricing_intervals(pricing_id,min_tokens,input_price,cache_write_1h_price) VALUES (110,0,0.000789,0.000987);
 INSERT INTO channel_account_stats_pricing_rules VALUES (20,10),(21,10),(22,11);
 INSERT INTO channel_account_stats_model_pricing(id,rule_id,platform,models,input_price,output_price) VALUES
 (200,20,'openai','["gpt-5.6-sol"]',0.000004,0.000025),
 (210,21,'openai','["gpt-5.6-sol","gpt-6-astra"]',0.000004,0.000025),
 (220,22,'openai','["gpt-6-astra-low"]',0.000088,0.000099),
 (221,22,'openai','["gpt-6-astra-max"]',0.000111,0.000222);
 INSERT INTO channel_account_stats_pricing_intervals(pricing_id,min_tokens,input_price,cache_write_1h_price) VALUES (220,0,0.000066,0.000077);
 `)
	require.NoError(t, err)
	migration, err := FS.ReadFile("235_add_gpt6_astra_support.sql")
	require.NoError(t, err)
	run := func() {
		tx, e := db.BeginTx(ctx, nil)
		require.NoError(t, e)
		_, e = tx.Exec(string(migration))
		if e != nil {
			_ = tx.Rollback()
		}
		require.NoError(t, e)
		require.NoError(t, tx.Commit())
	}
	run()
	var mapping string
	require.NoError(t, db.QueryRow(`SELECT credentials->'model_mapping'->>'gpt-6-astra' FROM accounts WHERE id=1`).Scan(&mapping))
	require.Equal(t, "gpt-6-astra", mapping)
	require.NoError(t, db.QueryRow(`SELECT credentials->'model_mapping'->>'gpt-6-astra' FROM accounts WHERE id=3`).Scan(&mapping))
	require.Equal(t, "custom", mapping)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id IN (2,4,5,6) AND credentials->'model_mapping' ? 'gpt-6-astra'`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT models_list_config->'models' FROM groups WHERE id=1`).Scan(&mapping))
	require.JSONEq(t, `["gpt-5.5","gpt-5.6-sol","gpt-6-astra"]`, mapping)
	require.NoError(t, db.QueryRow(`SELECT models_list_config->'models' FROM groups WHERE id=2`).Scan(&mapping))
	require.JSONEq(t, `["gpt-5.6-sol"]`, mapping)
	var enabled bool
	require.NoError(t, db.QueryRow(`SELECT (models_list_config->>'enabled')::boolean FROM groups WHERE id=3`).Scan(&enabled))
	require.False(t, enabled)
	for _, tc := range []struct {
		table, scope, iv                                string
		newScope, customID, duplicateID, untouchedScope int
	}{
		{"channel_model_pricing", "channel_id", "channel_pricing_intervals", 10, 110, 111, 12},
		{"channel_account_stats_model_pricing", "rule_id", "channel_account_stats_pricing_intervals", 21, 220, 221, 20},
	} {
		var price float64
		q := `SELECT COUNT(*) FROM ` + tc.table + ` WHERE ` + tc.scope + `=$1 AND enabled AND models ? 'gpt-6-astra'`
		require.NoError(t, db.QueryRow(q, tc.newScope).Scan(&count))
		require.Equal(t, 1, count)
		require.NoError(t, db.QueryRow(`SELECT models,input_price FROM `+tc.table+` WHERE `+tc.scope+`=$1 AND enabled AND models ? 'gpt-6-astra'`, tc.newScope).Scan(&mapping, &price))
		var aliases []string
		require.NoError(t, json.Unmarshal([]byte(mapping), &aliases))
		require.Len(t, aliases, 6)
		require.InDelta(t, 10e-6, price, 1e-12)
		require.NoError(t, db.QueryRow(q, tc.untouchedScope).Scan(&count))
		require.Zero(t, count)
		require.NoError(t, db.QueryRow(`SELECT models,enabled FROM `+tc.table+` WHERE id=$1`, tc.duplicateID).Scan(&mapping, &enabled))
		require.JSONEq(t, `[]`, mapping)
		require.False(t, enabled)
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+tc.iv+` WHERE pricing_id=$1`, tc.customID).Scan(&count))
		require.Equal(t, 1, count)
	}
	var input, write1h float64
	require.NoError(t, db.QueryRow(`SELECT input_price FROM channel_model_pricing WHERE id=110`).Scan(&input))
	require.InDelta(t, 0.000123, input, 1e-12)
	require.NoError(t, db.QueryRow(`SELECT cache_write_1h_price FROM channel_pricing_intervals WHERE pricing_id=110`).Scan(&write1h))
	require.InDelta(t, 0.000987, write1h, 1e-12)
	require.NoError(t, db.QueryRow(`SELECT input_price FROM channel_account_stats_model_pricing WHERE id=220`).Scan(&input))
	require.InDelta(t, 0.000088, input, 1e-12)
	require.NoError(t, db.QueryRow(`SELECT cache_write_1h_price FROM channel_account_stats_pricing_intervals WHERE pricing_id=220`).Scan(&write1h))
	require.InDelta(t, 0.000077, write1h, 1e-12)
	snapshot := func() string {
		var v string
		require.NoError(t, db.QueryRow(`SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_model_pricing p),(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_account_stats_model_pricing p),(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_pricing_intervals p),(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM channel_account_stats_pricing_intervals p))`).Scan(&v))
		return v
	}
	before := snapshot()
	run()
	require.JSONEq(t, before, snapshot())
}
