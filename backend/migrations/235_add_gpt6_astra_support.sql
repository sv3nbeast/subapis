-- GPT-6 Astra: verified ChatGPT OAuth Responses only (2026-09-05).
-- Kiro Q returns INVALID_MODEL_ID; never expand Kiro-backed OpenAI groups.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

UPDATE accounts a
SET credentials = jsonb_set(credentials, '{model_mapping}',
        credentials->'model_mapping' || '{"gpt-6-astra":"gpt-6-astra"}'::jsonb),
    updated_at = NOW()
WHERE platform = 'openai' AND type = 'oauth' AND deleted_at IS NULL
  AND parent_account_id IS NULL
  AND jsonb_typeof(credentials->'model_mapping') = 'object'
  AND credentials->'model_mapping'->>'gpt-5.6-sol' = 'gpt-5.6-sol'
  AND NOT credentials->'model_mapping' ? 'gpt-6-astra'
  AND NOT EXISTS (SELECT 1 FROM jsonb_object_keys(credentials->'model_mapping') k WHERE k LIKE '%*%');

WITH eligible AS (
    SELECT g.id, g.models_list_config
    FROM groups g
    WHERE g.platform = 'openai' AND g.deleted_at IS NULL
      AND jsonb_typeof(g.models_list_config->'models') = 'array'
      AND g.models_list_config->'models' ?| ARRAY['gpt-5.6-sol','gpt-5.5']
      AND EXISTS (
          SELECT 1 FROM account_groups ag JOIN accounts a ON a.id = ag.account_id
          WHERE ag.group_id = g.id AND a.platform = 'openai' AND a.type = 'oauth'
            AND a.deleted_at IS NULL AND a.parent_account_id IS NULL
      )
), expanded AS (
    SELECT id, jsonb_set(models_list_config, '{models}', (
        SELECT jsonb_agg(model ORDER BY first_pos) FROM (
            SELECT model, MIN(pos) AS first_pos
            FROM jsonb_array_elements_text(models_list_config->'models' || '["gpt-6-astra"]'::jsonb)
                 WITH ORDINALITY AS v(model,pos)
            GROUP BY model
        ) dedup
    )) AS config FROM eligible
)
UPDATE groups g SET models_list_config=e.config, updated_at=NOW()
FROM expanded e WHERE g.id=e.id AND g.models_list_config IS DISTINCT FROM e.config;

-- Billing and account statistics have separate ownership. The latter is only
-- normalized when the operator already bound Astra; Sol cost rules are NOT copied.
DO $astra$
DECLARE
    price_table TEXT;
    scope_column TEXT;
    interval_table TEXT;
    scope_id BIGINT;
    canonical_id BIGINT;
    other_row RECORD;
    remaining JSONB;
    aliases JSONB := '["gpt-6-astra","gpt-6-astra-low","gpt-6-astra-medium","gpt-6-astra-high","gpt-6-astra-xhigh","gpt-6-astra-max"]';
    scopes_query TEXT;
BEGIN
    FOR price_table, scope_column, interval_table IN VALUES
        ('channel_model_pricing','channel_id','channel_pricing_intervals'),
        ('channel_account_stats_model_pricing','rule_id','channel_account_stats_pricing_intervals')
    LOOP
        scopes_query := format('SELECT DISTINCT %I FROM %I p WHERE platform=''openai'' AND enabled AND models ?| ARRAY(SELECT jsonb_array_elements_text($1))', scope_column, price_table);
        IF price_table = 'channel_model_pricing' THEN
            scopes_query := scopes_query || ' UNION SELECT DISTINCT p.channel_id FROM channel_model_pricing p
                WHERE p.platform=''openai'' AND p.enabled AND p.models ? ''gpt-5.6-sol''
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
                    VALUES ($1,''openai'',$2,''token'',0.000010,0.000050,0.0000125,0.000001,true) RETURNING id', price_table,scope_column)
                    INTO canonical_id USING scope_id,aliases;
                -- Runtime interval convention is (min_tokens, max_tokens].
                EXECUTE format('INSERT INTO %I (pricing_id,min_tokens,max_tokens,tier_label,input_price,output_price,cache_write_price,cache_read_price,sort_order)
                    VALUES ($1,0,272000,''Standard'',0.000010,0.000050,0.0000125,0.000001,0),
                           ($1,272000,NULL,''>272K'',0.000020,0.000075,0.000025,0.000002,1)',interval_table) USING canonical_id;
                IF price_table = 'channel_model_pricing' THEN
                    UPDATE channel_model_pricing SET fast_multiplier=2,flex_multiplier=0.5 WHERE id=canonical_id;
                END IF;
            ELSE
                EXECUTE format('UPDATE %I SET models=$1,updated_at=NOW() WHERE id=$2 AND models IS DISTINCT FROM $1',price_table)
                    USING aliases,canonical_id;
            END IF;
            FOR other_row IN EXECUTE format('SELECT id,models FROM %I WHERE %I=$1 AND platform=''openai'' AND enabled
                AND id<>$2 AND models ?| ARRAY(SELECT jsonb_array_elements_text($3))',price_table,scope_column)
                USING scope_id,canonical_id,aliases
            LOOP
                SELECT COALESCE(jsonb_agg(model ORDER BY pos),'[]'::jsonb) INTO remaining
                FROM jsonb_array_elements_text(other_row.models) WITH ORDINALITY v(model,pos)
                WHERE NOT aliases ? model;
                EXECUTE format('UPDATE %I SET models=$1,enabled=(jsonb_array_length($1)>0),updated_at=NOW() WHERE id=$2',price_table)
                    USING remaining,other_row.id;
            END LOOP;
        END LOOP;
    END LOOP;
END $astra$;
