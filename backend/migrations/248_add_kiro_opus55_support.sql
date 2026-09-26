-- Claude Opus 5.5 on Kiro (verified 2026-09-26).
--
-- Migration 246 added Opus 5.5 for Anthropic accounts only: at the time Kiro's
-- ListAvailableModels did not carry it. Kiro now lists it, so this extends the
-- same model to Kiro accounts, groups and channel pricing. 246 is already
-- applied in production and is not edited.
--
-- Live evidence (the platform's own request builders, account 2683/2696):
--   ListAvailableModels lists modelId "claude-opus-5.5" -- maxInputTokens
--   1000000, maxOutputTokens 128000, thinking.type enum ["adaptive"] only,
--   effort default "medium". Streaming text, adaptive thinking and a
--   structured toolUseEvent (no standalone "call" narration) all returned 200
--   with exactly one message_stop. A synthetic claude-opus-5.9 returns 400
--   INVALID_MODEL_ID, so there is no silent fallback. The legacy thinking forms
--   (disabled, enabled+budget_tokens, the -thinking alias) are all accepted.
--
-- The Kiro upstream ID is dotted (claude-opus-5.5), like claude-opus-4.8;
-- Opus 5 is the exception whose Kiro ID has no minor version.
--
-- Official price (platform.claude.com, same as migration 246): input $4,
-- output $20, 5m write $5, 1h write $8, cache read $0.20 per MTok. No
-- long-context tier, so no interval rows are created.
--
-- Forward-only and safe to run twice.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

-- 1) Kiro accounts that already map Opus 5 to itself. Deleted rows,
-- credential shadows and wildcard mappings are skipped. Existing keys win:
-- the new pairs are placed on the left of ||, so an explicit operator value for
-- either alias is preserved.
UPDATE accounts a
SET credentials = jsonb_set(credentials, '{model_mapping}',
        '{"claude-opus-5-5":"claude-opus-5.5","claude-opus-5-5-thinking":"claude-opus-5.5"}'::jsonb
        || (credentials->'model_mapping')),
    updated_at = NOW()
WHERE a.platform = 'kiro' AND a.deleted_at IS NULL
  AND a.parent_account_id IS NULL
  AND jsonb_typeof(a.credentials->'model_mapping') = 'object'
  AND a.credentials->'model_mapping'->>'claude-opus-5' = 'claude-opus-5'
  AND NOT a.credentials->'model_mapping' ? 'claude-opus-5-5'
  AND NOT EXISTS (SELECT 1 FROM jsonb_object_keys(a.credentials->'model_mapping') k WHERE k LIKE '%*%');

-- 2) Kiro groups that already advertise Opus 5. The thinking alias is added
-- alongside the base id because the existing rows pair them. Order, duplicates
-- and the enabled flag of the existing list are preserved; a group whose
-- allowlist is enforced would otherwise reject Opus 5.5 outright.
WITH eligible AS (
    SELECT g.id, g.model_allowlist
    FROM groups g
    WHERE g.platform = 'kiro' AND g.deleted_at IS NULL
      AND jsonb_typeof(g.model_allowlist->'models') = 'array'
      AND g.model_allowlist->'models' ? 'claude-opus-5'
), expanded AS (
    SELECT id, jsonb_set(model_allowlist, '{models}', (
        SELECT jsonb_agg(model ORDER BY first_pos) FROM (
            SELECT model, MIN(pos) AS first_pos
            FROM jsonb_array_elements_text(
                     model_allowlist->'models' || '["claude-opus-5-5","claude-opus-5-5-thinking"]'::jsonb)
                 WITH ORDINALITY AS v(model, pos)
            GROUP BY model
        ) dedup
    )) AS config FROM eligible
)
UPDATE groups g SET model_allowlist = e.config, updated_at = NOW()
FROM expanded e WHERE g.id = e.id AND g.model_allowlist IS DISTINCT FROM e.config;

-- 3) Channel pricing. One enabled dedicated row per Kiro channel that already
-- prices Opus 5. Existing dedicated custom rows keep their prices, billing mode
-- and intervals; mixed/duplicate rows only lose the new aliases, and a row that
-- becomes empty is retained but disabled.
--
-- Account statistics pricing holds the operator's upstream cost, not a list
-- price. It is therefore only normalized, never extended: no row is created
-- there, and an existing row only gains aliases that scope had already bound.
DO $kiroopus55$
DECLARE
    price_table TEXT;
    scope_column TEXT;
    scope_id BIGINT;
    canonical_id BIGINT;
    other_row RECORD;
    remaining JSONB;
    scopes_query TEXT;
    is_user_billing BOOLEAN;
    target_aliases JSONB;
    aliases CONSTANT JSONB := '["claude-opus-5-5","claude-opus-5-5-thinking"]'::jsonb;
BEGIN
    FOR price_table, scope_column IN VALUES
        ('channel_model_pricing', 'channel_id'),
        ('channel_account_stats_model_pricing', 'rule_id')
    LOOP
        is_user_billing := (price_table = 'channel_model_pricing');
        scopes_query := format(
            'SELECT DISTINCT %I FROM %I p WHERE platform=''kiro'' AND enabled
               AND (models ?| ARRAY(SELECT jsonb_array_elements_text($1))
                    OR ($2 AND models ? ''claude-opus-5''))',
            scope_column, price_table);
        FOR scope_id IN EXECUTE scopes_query USING aliases, is_user_billing LOOP
            target_aliases := aliases;
            IF NOT is_user_billing THEN
                EXECUTE format(
                    'SELECT COALESCE(jsonb_agg(alias ORDER BY pos), ''[]''::jsonb)
                       FROM jsonb_array_elements_text($1) WITH ORDINALITY v(alias, pos)
                      WHERE EXISTS (SELECT 1 FROM %I WHERE %I=$2 AND platform=''kiro''
                                      AND enabled AND models ? alias)',
                    price_table, scope_column)
                    INTO target_aliases USING aliases, scope_id;
            END IF;
            canonical_id := NULL;
            EXECUTE format('SELECT id FROM %I WHERE %I=$1 AND platform=''kiro'' AND enabled
                AND jsonb_array_length(models)>0 AND models <@ $2 ORDER BY id LIMIT 1',
                price_table, scope_column)
                INTO canonical_id USING scope_id, target_aliases;
            CONTINUE WHEN canonical_id IS NULL AND NOT is_user_billing;
            IF canonical_id IS NULL THEN
                EXECUTE format('INSERT INTO %I (%I, platform, models, billing_mode,
                    input_price, output_price, cache_write_price, cache_read_price,
                    cache_write_5m_price, cache_write_1h_price, enabled)
                    VALUES ($1, ''kiro'', $2, ''token'',
                            0.000004, 0.00002, 0.000005, 0.0000002,
                            0.000005, 0.000008, true) RETURNING id',
                    price_table, scope_column)
                    INTO canonical_id USING scope_id, target_aliases;
            ELSE
                EXECUTE format('UPDATE %I SET models=$1, updated_at=NOW()
                    WHERE id=$2 AND models IS DISTINCT FROM $1', price_table)
                    USING target_aliases, canonical_id;
            END IF;
            FOR other_row IN EXECUTE format('SELECT id, models FROM %I WHERE %I=$1 AND platform=''kiro''
                AND enabled AND id<>$2 AND models ?| ARRAY(SELECT jsonb_array_elements_text($3))',
                price_table, scope_column)
                USING scope_id, canonical_id, target_aliases
            LOOP
                SELECT COALESCE(jsonb_agg(model ORDER BY pos), '[]'::jsonb) INTO remaining
                FROM jsonb_array_elements_text(other_row.models) WITH ORDINALITY v(model, pos)
                WHERE NOT target_aliases ? model;
                EXECUTE format('UPDATE %I SET models=$1, enabled=(jsonb_array_length($1)>0),
                    updated_at=NOW() WHERE id=$2', price_table)
                    USING remaining, other_row.id;
            END LOOP;
        END LOOP;
    END LOOP;
END $kiroopus55$;
