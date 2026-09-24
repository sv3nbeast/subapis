-- Claude Opus 5.5 + Grok 4.7 support (verified 2026-09-24).
--
-- Live evidence:
--   claude-opus-5-5 — Anthropic Messages, account 2587: HTTP 200, echoes
--     claude-opus-5-5, exactly one message_stop, thinking on by default.
--     thinking.type=disabled and thinking.budget_tokens both return 400
--     ("requires adaptive thinking"); forced tool_choice returns 400; a
--     synthetic claude-opus-5-5-* id returns 404 model_not_found, so no silent
--     fallback hides an unsupported name. Kiro ListAvailableModels (account
--     2696) lists 19 models and does NOT carry Opus 5.5, so Kiro mappings and
--     Kiro-backed groups are not expanded. No first-party Anthropic OAuth
--     account is active, and no Bedrock/Vertex ID is invented here.
--   grok-4.7 — xAI chat completions, account 2539: HTTP 200, echoes grok-4.7,
--     finish_reason stop; low/medium/high/xhigh all accepted; tool calling
--     returns finish_reason tool_calls. A synthetic grok-4.7-* id returns 404
--     not-found while grok-4.7 returns 402 on a credit-exhausted account, so
--     the id is recognised upstream rather than silently aliased.
--
-- Official prices (USD per token, retrieved 2026-09-24):
--   Opus 5.5  input 0.000004  output 0.00002  5m write 0.000005  1h write 0.000008
--             cache read 0.0000002 (0.05x base input, NOT the usual 0.1x)
--             1M context is billed at standard rates - no long-context tier.
--   Grok 4.7  input 0.000002  output 0.000006  cached input 0.0000005 below 200k;
--             at/above 200k prompt tokens every token doubles to 0.000004 /
--             0.000012 / 0.000001. Runtime intervals are (min, max], so the
--             boundary is stored as 199999 to mean ">= 200000" - this mirrors
--             the existing grok-4.6 rows exactly.
--
-- Forward-only and safe to run twice.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

-- 1) Anthropic accounts that already map Opus 5 to itself. Deleted rows,
-- credential shadows, wildcard mappings and explicit overrides are skipped.
UPDATE accounts a
SET credentials = jsonb_set(credentials, '{model_mapping}',
        credentials->'model_mapping' || '{"claude-opus-5-5":"claude-opus-5-5"}'::jsonb),
    updated_at = NOW()
WHERE a.platform = 'anthropic' AND a.deleted_at IS NULL
  AND a.parent_account_id IS NULL
  AND jsonb_typeof(a.credentials->'model_mapping') = 'object'
  AND a.credentials->'model_mapping'->>'claude-opus-5' = 'claude-opus-5'
  AND NOT a.credentials->'model_mapping' ? 'claude-opus-5-5'
  AND NOT EXISTS (SELECT 1 FROM jsonb_object_keys(a.credentials->'model_mapping') k WHERE k LIKE '%*%');

-- 2) Anthropic groups that already advertise Opus 5. The thinking alias is added
-- alongside the base id because the existing rows pair them. Kiro-backed groups
-- are untouched: Kiro does not serve Opus 5.5. Order, duplicates and the enabled
-- flag of the existing list are preserved.
WITH eligible AS (
    SELECT g.id, g.model_allowlist
    FROM groups g
    WHERE g.platform = 'anthropic' AND g.deleted_at IS NULL
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

-- 3) Channel pricing. Each new model family gets exactly one enabled dedicated
-- row per eligible scope. Existing dedicated custom rows keep their prices,
-- billing mode and intervals; mixed/duplicate rows only lose the new aliases,
-- and a row that becomes empty is retained but disabled.
--
-- Account statistics pricing has separate ownership - its numbers are the
-- operator's upstream cost, not a list price. It is therefore only normalized,
-- never extended: no row is created there, and an existing row only gains
-- aliases that scope had already bound.
DO $opus55grok47$
DECLARE
    price_table TEXT;
    scope_column TEXT;
    interval_table TEXT;
    spec RECORD;
    scope_id BIGINT;
    canonical_id BIGINT;
    other_row RECORD;
    remaining JSONB;
    scopes_query TEXT;
    is_user_billing BOOLEAN;
    target_aliases JSONB;
BEGIN
    FOR price_table, scope_column, interval_table IN VALUES
        ('channel_model_pricing', 'channel_id', 'channel_pricing_intervals'),
        ('channel_account_stats_model_pricing', 'rule_id', 'channel_account_stats_pricing_intervals')
    LOOP
        is_user_billing := (price_table = 'channel_model_pricing');
        FOR spec IN
            SELECT * FROM (VALUES
                -- platform, aliases, predecessor, input, output, write, read, long_ctx
                ('anthropic',
                 '["claude-opus-5-5","claude-opus-5-5-thinking"]'::jsonb, 'claude-opus-5',
                 0.000004::numeric, 0.00002::numeric, 0.000005::numeric, 0.0000002::numeric,
                 false),
                ('grok',
                 '["grok-4.7"]'::jsonb, 'grok-4.6',
                 0.000002::numeric, 0.000006::numeric, 0::numeric, 0.0000005::numeric,
                 true)
            ) AS s(platform, aliases, predecessor, input_price, output_price,
                   write_price, read_price, long_ctx)
        LOOP
            -- Scopes already referencing the new aliases are always normalized.
            -- Only user billing additionally follows the predecessor model;
            -- account-statistics pricing is operator-owned and never expanded
            -- from it, which $4 encodes.
            scopes_query := format(
                'SELECT DISTINCT %I FROM %I p WHERE platform=$1 AND enabled
                   AND (models ?| ARRAY(SELECT jsonb_array_elements_text($2))
                        OR ($4 AND models ? $3))',
                scope_column, price_table);
            FOR scope_id IN EXECUTE scopes_query
                USING spec.platform, spec.aliases, spec.predecessor, is_user_billing LOOP
                -- User billing gets the full alias set. Account statistics is
                -- operator-owned: it only merges aliases the operator already
                -- bound here, so the migration never introduces a model the
                -- operator never priced. The suffixed alias is normalized away
                -- before the upstream call anyway, so statistics is always
                -- looked up by the base id and would never match it.
                target_aliases := spec.aliases;
                IF NOT is_user_billing THEN
                    EXECUTE format(
                        'SELECT COALESCE(jsonb_agg(alias ORDER BY pos), ''[]''::jsonb)
                           FROM jsonb_array_elements_text($1) WITH ORDINALITY v(alias, pos)
                          WHERE EXISTS (SELECT 1 FROM %I WHERE %I=$2 AND platform=$3
                                          AND enabled AND models ? alias)',
                        price_table, scope_column)
                        INTO target_aliases USING spec.aliases, scope_id, spec.platform;
                END IF;
                canonical_id := NULL;
                -- A dedicated custom row wins; preserve its prices, mode and intervals.
                EXECUTE format('SELECT id FROM %I WHERE %I=$1 AND platform=$2 AND enabled
                    AND jsonb_array_length(models)>0 AND models <@ $3 ORDER BY id LIMIT 1',
                    price_table, scope_column)
                    INTO canonical_id USING scope_id, spec.platform, target_aliases;
                -- Never create a statistics row: its prices are the operator's
                -- upstream cost, which user list prices must not stand in for.
                CONTINUE WHEN canonical_id IS NULL AND NOT is_user_billing;
                IF canonical_id IS NULL THEN
                    EXECUTE format('INSERT INTO %I (%I, platform, models, billing_mode,
                        input_price,output_price,cache_write_price,cache_read_price,enabled)
                        VALUES ($1,$2,$3,''token'',$4,$5,$6,$7,true) RETURNING id',
                        price_table, scope_column)
                        INTO canonical_id
                        USING scope_id, spec.platform, target_aliases, spec.input_price,
                              spec.output_price, spec.write_price, spec.read_price;
                    IF spec.long_ctx THEN
                        -- Runtime interval convention is (min_tokens, max_tokens]; 199999
                        -- therefore means "prompt reaches 200000 tokens", matching grok-4.6.
                        EXECUTE format('INSERT INTO %I (pricing_id,min_tokens,max_tokens,
                            input_price,output_price,cache_read_price,sort_order)
                            VALUES ($1,0,199999,$2,$3,$4,0),
                                   ($1,199999,NULL,$5,$6,$7,1)', interval_table)
                            USING canonical_id, spec.input_price, spec.output_price, spec.read_price,
                                  spec.input_price*2, spec.output_price*2, spec.read_price*2;
                    ELSE
                        -- Opus 5.5 has no long-context tier; carry the official
                        -- 5m/1h cache-write breakdown instead.
                        EXECUTE format('UPDATE %I SET cache_write_5m_price=0.000005,
                            cache_write_1h_price=0.000008 WHERE id=$1', price_table)
                            USING canonical_id;
                    END IF;
                ELSE
                    EXECUTE format('UPDATE %I SET models=$1,updated_at=NOW()
                        WHERE id=$2 AND models IS DISTINCT FROM $1', price_table)
                        USING target_aliases, canonical_id;
                END IF;
                FOR other_row IN EXECUTE format('SELECT id,models FROM %I WHERE %I=$1 AND platform=$2
                    AND enabled AND id<>$3 AND models ?| ARRAY(SELECT jsonb_array_elements_text($4))',
                    price_table, scope_column)
                    USING scope_id, spec.platform, canonical_id, target_aliases
                LOOP
                    SELECT COALESCE(jsonb_agg(model ORDER BY pos), '[]'::jsonb) INTO remaining
                    FROM jsonb_array_elements_text(other_row.models) WITH ORDINALITY v(model, pos)
                    WHERE NOT target_aliases ? model;
                    EXECUTE format('UPDATE %I SET models=$1,enabled=(jsonb_array_length($1)>0),
                        updated_at=NOW() WHERE id=$2', price_table)
                        USING remaining, other_row.id;
                END LOOP;
            END LOOP;
        END LOOP;
    END LOOP;
END $opus55grok47$;
