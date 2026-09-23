-- GPT-6 Sol / GPT-6 Luna: verified ChatGPT OAuth Responses only (2026-09-23).
-- Live probes echoed the exact model on response.created/response.completed and
-- rejected gpt-6-terra plus a synthetic gpt-6-* id with HTTP 400, so no silent
-- upstream fallback hides an unsupported name. Kiro Q returned
-- 400 INVALID_MODEL_ID for both models while gpt-5.6-sol returned 200 on the same
-- account, so Kiro model lists/mappings and Kiro-backed OpenAI groups/channels are
-- never expanded here.
--
-- Official prices (USD per token, retrieved 2026-09-23):
--   Sol   input 0.000002   cache read 0.0000002    cache write 0.0000025    output 0.00001
--   Luna  input 0.0000001  cache read 0.00000001   cache write 0.000000125  output 0.0000005
-- Above 272,000 input tokens input/cache are x2 and output is x1.5. Fast is x2 and
-- Flex/Batch are x0.5. Each model is its own price family, so each gets its own
-- dedicated row instead of sharing Astra's.
--
-- Forward-only and safe to run twice.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

-- 1) Direct OpenAI OAuth accounts that already map Astra to itself. Deleted rows,
-- credential shadows, wildcard mappings and explicit operator overrides are skipped.
UPDATE accounts a
SET credentials = jsonb_set(credentials, '{model_mapping}',
        credentials->'model_mapping' || '{"gpt-6-sol":"gpt-6-sol","gpt-6-luna":"gpt-6-luna"}'::jsonb),
    updated_at = NOW()
WHERE a.platform = 'openai' AND a.type = 'oauth' AND a.deleted_at IS NULL
  AND a.parent_account_id IS NULL
  AND jsonb_typeof(a.credentials->'model_mapping') = 'object'
  AND a.credentials->'model_mapping'->>'gpt-6-astra' = 'gpt-6-astra'
  AND NOT (a.credentials->'model_mapping' ? 'gpt-6-sol' AND a.credentials->'model_mapping' ? 'gpt-6-luna')
  AND NOT EXISTS (SELECT 1 FROM jsonb_object_keys(a.credentials->'model_mapping') k WHERE k LIKE '%*%');

-- 2) OpenAI groups whose allowlist already advertises Astra and that are actually
-- served by a direct OpenAI OAuth account. Kiro-backed OpenAI groups are excluded
-- because Kiro rejects every gpt-6-* model id. The enabled flag, order and
-- deduplication of the existing list are preserved.
WITH eligible AS (
    SELECT g.id, g.model_allowlist
    FROM groups g
    WHERE g.platform = 'openai' AND g.deleted_at IS NULL
      AND jsonb_typeof(g.model_allowlist->'models') = 'array'
      AND g.model_allowlist->'models' ? 'gpt-6-astra'
      AND EXISTS (
          SELECT 1 FROM account_groups ag JOIN accounts a ON a.id = ag.account_id
          WHERE ag.group_id = g.id AND a.platform = 'openai' AND a.type = 'oauth'
            AND a.deleted_at IS NULL AND a.parent_account_id IS NULL
      )
), expanded AS (
    SELECT id, jsonb_set(model_allowlist, '{models}', (
        SELECT jsonb_agg(model ORDER BY first_pos) FROM (
            SELECT model, MIN(pos) AS first_pos
            FROM jsonb_array_elements_text(model_allowlist->'models' || '["gpt-6-sol","gpt-6-luna"]'::jsonb)
                 WITH ORDINALITY AS v(model, pos)
            GROUP BY model
        ) dedup
    )) AS config FROM eligible
)
UPDATE groups g SET model_allowlist = e.config, updated_at = NOW()
FROM expanded e WHERE g.id = e.id AND g.model_allowlist IS DISTINCT FROM e.config;

-- 3) Channel pricing. Each model gets exactly one enabled dedicated row per eligible
-- scope, with its own standard and >272K intervals. Existing dedicated custom rows
-- keep their prices, billing mode and intervals; mixed/duplicate rows only lose the
-- new aliases, and a row that becomes empty is retained but disabled.
--
-- Billing and account statistics have separate ownership. The latter is only
-- normalized when the operator already bound these models; user prices are NOT copied.
DO $gpt6$
DECLARE
    price_table TEXT;
    scope_column TEXT;
    interval_table TEXT;
    model_name TEXT;
    std_input NUMERIC;
    std_output NUMERIC;
    std_write NUMERIC;
    std_read NUMERIC;
    scope_id BIGINT;
    canonical_id BIGINT;
    other_row RECORD;
    remaining JSONB;
    aliases JSONB;
    scopes_query TEXT;
BEGIN
    FOR price_table, scope_column, interval_table IN VALUES
        ('channel_model_pricing', 'channel_id', 'channel_pricing_intervals'),
        ('channel_account_stats_model_pricing', 'rule_id', 'channel_account_stats_pricing_intervals')
    LOOP
        FOR model_name, std_input, std_output, std_write, std_read IN VALUES
            ('gpt-6-sol', 0.000002, 0.000010, 0.0000025, 0.0000002),
            ('gpt-6-luna', 0.0000001, 0.0000005, 0.000000125, 0.00000001)
        LOOP
            aliases := jsonb_build_array(model_name);
            scopes_query := format('SELECT DISTINCT %I FROM %I p WHERE platform=''openai'' AND enabled AND models ?| ARRAY(SELECT jsonb_array_elements_text($1))', scope_column, price_table);
            IF price_table = 'channel_model_pricing' THEN
                -- Only channels actually served by a direct OpenAI OAuth account are
                -- expanded; Kiro-backed ChatGPT-compatible channels are left alone.
                scopes_query := scopes_query || ' UNION SELECT DISTINCT p.channel_id FROM channel_model_pricing p
                    WHERE p.platform=''openai'' AND p.enabled AND p.models ? ''gpt-6-astra''
                      AND EXISTS (SELECT 1 FROM channel_groups cg JOIN account_groups ag ON ag.group_id=cg.group_id
                                  JOIN accounts a ON a.id=ag.account_id
                                  WHERE cg.channel_id=p.channel_id AND a.platform=''openai'' AND a.type=''oauth''
                                    AND a.deleted_at IS NULL AND a.parent_account_id IS NULL)';
            END IF;
            FOR scope_id IN EXECUTE scopes_query USING aliases LOOP
                canonical_id := NULL;
                -- A dedicated custom row wins; preserve its prices, mode and intervals.
                EXECUTE format('SELECT id FROM %I WHERE %I=$1 AND platform=''openai'' AND enabled
                    AND jsonb_array_length(models)>0 AND models <@ $2 ORDER BY id LIMIT 1', price_table, scope_column)
                    INTO canonical_id USING scope_id, aliases;
                IF canonical_id IS NULL THEN
                    EXECUTE format('INSERT INTO %I (%I, platform, models, billing_mode,
                        input_price,output_price,cache_write_price,cache_read_price,enabled)
                        VALUES ($1,''openai'',$2,''token'',$3,$4,$5,$6,true) RETURNING id', price_table, scope_column)
                        INTO canonical_id USING scope_id, aliases, std_input, std_output, std_write, std_read;
                    -- Runtime interval convention is (min_tokens, max_tokens].
                    EXECUTE format('INSERT INTO %I (pricing_id,min_tokens,max_tokens,tier_label,input_price,output_price,cache_write_price,cache_read_price,sort_order)
                        VALUES ($1,0,272000,''Standard'',$2,$3,$4,$5,0),
                               ($1,272000,NULL,''>272K'',$6,$7,$8,$9,1)', interval_table)
                        USING canonical_id, std_input, std_output, std_write, std_read,
                              std_input*2, std_output*1.5, std_write*2, std_read*2;
                    IF price_table = 'channel_model_pricing' THEN
                        UPDATE channel_model_pricing SET fast_multiplier=2, flex_multiplier=0.5 WHERE id=canonical_id;
                    END IF;
                ELSE
                    EXECUTE format('UPDATE %I SET models=$1,updated_at=NOW() WHERE id=$2 AND models IS DISTINCT FROM $1', price_table)
                        USING aliases, canonical_id;
                END IF;
                FOR other_row IN EXECUTE format('SELECT id,models FROM %I WHERE %I=$1 AND platform=''openai'' AND enabled
                    AND id<>$2 AND models ?| ARRAY(SELECT jsonb_array_elements_text($3))', price_table, scope_column)
                    USING scope_id, canonical_id, aliases
                LOOP
                    SELECT COALESCE(jsonb_agg(model ORDER BY pos), '[]'::jsonb) INTO remaining
                    FROM jsonb_array_elements_text(other_row.models) WITH ORDINALITY v(model, pos)
                    WHERE NOT aliases ? model;
                    EXECUTE format('UPDATE %I SET models=$1,enabled=(jsonb_array_length($1)>0),updated_at=NOW() WHERE id=$2', price_table)
                        USING remaining, other_row.id;
                END LOOP;
            END LOOP;
        END LOOP;
    END LOOP;
END $gpt6$;
