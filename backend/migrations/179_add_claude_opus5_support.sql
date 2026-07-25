-- Add Claude Opus 5 to persisted account mappings, group model lists, and
-- existing Opus channel-pricing rows for the verified Anthropic and Kiro paths.
--
-- The official Opus 5 token prices match Opus 4.8: $5 input, $25 output,
-- $6.25 5m cache write, $10 1h cache write, and $0.50 cache read per MTok.
-- Existing custom price overrides are preserved; cache breakdown fields are
-- only backfilled on rows that already carry the exact official Opus prices.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

-- Existing explicit account mappings are capability allowlists. Extend only
-- accounts that already expose Opus 4.8 and have no wildcard override.
WITH target_accounts AS (
    SELECT
        id,
        credentials->'model_mapping' AS model_mapping
    FROM accounts
    WHERE platform IN ('anthropic', 'kiro')
      AND deleted_at IS NULL
      AND jsonb_typeof(credentials->'model_mapping') = 'object'
      AND (
          credentials->'model_mapping' ? 'claude-opus-4-8'
          OR credentials->'model_mapping' ? 'claude-opus-4-8-thinking'
      )
      AND NOT EXISTS (
          SELECT 1
          FROM jsonb_object_keys(credentials->'model_mapping') AS mapping_key(key)
          WHERE mapping_key.key LIKE '%*%'
      )
), expanded_accounts AS (
    SELECT
        id,
        model_mapping
            || CASE
                WHEN model_mapping ? 'claude-opus-5' THEN '{}'::jsonb
                ELSE '{"claude-opus-5":"claude-opus-5"}'::jsonb
               END
            || CASE
                WHEN model_mapping ? 'claude-opus-5-thinking' THEN '{}'::jsonb
                ELSE '{"claude-opus-5-thinking":"claude-opus-5"}'::jsonb
               END AS model_mapping
    FROM target_accounts
)
UPDATE accounts a
SET credentials = jsonb_set(a.credentials, '{model_mapping}', e.model_mapping, true),
    updated_at = NOW()
FROM expanded_accounts e
WHERE a.id = e.id
  AND a.credentials->'model_mapping' IS DISTINCT FROM e.model_mapping;

-- Preserve each group's configured order while extending lists that already
-- opted into Opus 4.8. This also covers currently disabled lists if an admin
-- enables them later.
WITH target_groups AS (
    SELECT
        id,
        models_list_config,
        models_list_config->'models' AS models
    FROM groups
    WHERE platform IN ('anthropic', 'kiro')
      AND deleted_at IS NULL
      AND jsonb_typeof(models_list_config->'models') = 'array'
      AND (
          models_list_config->'models' @> '["claude-opus-4-8"]'::jsonb
          OR models_list_config->'models' @> '["claude-opus-4-8-thinking"]'::jsonb
      )
), expanded_group_models AS (
    SELECT
        id,
        jsonb_agg(model ORDER BY first_order) AS models
    FROM (
        SELECT id, model, MIN(sort_order) AS first_order
        FROM (
            SELECT id, model, ordinality AS sort_order
            FROM target_groups
            CROSS JOIN LATERAL jsonb_array_elements_text(models)
                WITH ORDINALITY AS existing_models(model, ordinality)
            UNION ALL
            SELECT id, 'claude-opus-5', 1000000
            FROM target_groups
            WHERE NOT models @> '["claude-opus-5"]'::jsonb
            UNION ALL
            SELECT id, 'claude-opus-5-thinking', 1000001
            FROM target_groups
            WHERE NOT models @> '["claude-opus-5-thinking"]'::jsonb
        ) ordered_models
        GROUP BY id, model
    ) deduped_models
    GROUP BY id
), expanded_groups AS (
    SELECT
        g.id,
        jsonb_set(g.models_list_config, '{models}', e.models, true) AS models_list_config
    FROM target_groups g
    JOIN expanded_group_models e USING (id)
)
UPDATE groups g
SET models_list_config = e.models_list_config,
    updated_at = NOW()
FROM expanded_groups e
WHERE g.id = e.id
  AND g.models_list_config IS DISTINCT FROM e.models_list_config;

-- Pick exactly one active Opus row per channel/platform. Prefer an existing
-- Opus 5 row when present, otherwise inherit the Opus 4.8 row's channel price.
WITH ranked_pricing AS (
    SELECT
        p.id,
        p.channel_id,
        p.platform,
        p.models,
        ROW_NUMBER() OVER (
            PARTITION BY p.channel_id, p.platform
            ORDER BY
                CASE
                    WHEN p.models @> '["claude-opus-5"]'::jsonb
                      OR p.models @> '["claude-opus-5-thinking"]'::jsonb
                    THEN 0 ELSE 1
                END,
                p.id
        ) AS row_number
    FROM channel_model_pricing p
    WHERE p.platform IN ('anthropic', 'kiro')
      AND p.billing_mode = 'token'
      AND p.enabled = true
      AND (
          p.models @> '["claude-opus-5"]'::jsonb
          OR p.models @> '["claude-opus-5-thinking"]'::jsonb
          OR p.models @> '["claude-opus-4-8"]'::jsonb
          OR p.models @> '["claude-opus-4-8-thinking"]'::jsonb
      )
), target_pricing AS (
    SELECT
        r.id,
        r.channel_id,
        r.platform,
        r.models,
        NOT EXISTS (
            SELECT 1
            FROM channel_model_pricing existing
            WHERE existing.channel_id = r.channel_id
              AND existing.platform = r.platform
              AND existing.enabled = true
              AND existing.models @> '["claude-opus-5"]'::jsonb
        ) AS add_base,
        NOT EXISTS (
            SELECT 1
            FROM channel_model_pricing existing
            WHERE existing.channel_id = r.channel_id
              AND existing.platform = r.platform
              AND existing.enabled = true
              AND existing.models @> '["claude-opus-5-thinking"]'::jsonb
        ) AS add_thinking
    FROM ranked_pricing r
    WHERE r.row_number = 1
), expanded_pricing_models AS (
    SELECT
        id,
        jsonb_agg(model ORDER BY first_order) AS models
    FROM (
        SELECT id, model, MIN(sort_order) AS first_order
        FROM (
            SELECT id, model, ordinality AS sort_order
            FROM target_pricing
            CROSS JOIN LATERAL jsonb_array_elements_text(models)
                WITH ORDINALITY AS existing_models(model, ordinality)
            UNION ALL
            SELECT id, 'claude-opus-5', 1000000
            FROM target_pricing
            WHERE add_base
            UNION ALL
            SELECT id, 'claude-opus-5-thinking', 1000001
            FROM target_pricing
            WHERE add_thinking
        ) ordered_models
        GROUP BY id, model
    ) deduped_models
    GROUP BY id
)
UPDATE channel_model_pricing p
SET models = e.models,
    cache_write_5m_price = CASE
        WHEN p.input_price = 0.000005000000
         AND p.output_price = 0.000025000000
         AND p.cache_read_price = 0.000000500000
         AND (p.cache_write_price IS NULL OR p.cache_write_price = 0.000006250000)
        THEN COALESCE(p.cache_write_5m_price, 0.000006250000)
        ELSE p.cache_write_5m_price
    END,
    cache_write_1h_price = CASE
        WHEN p.input_price = 0.000005000000
         AND p.output_price = 0.000025000000
         AND p.cache_read_price = 0.000000500000
         AND (p.cache_write_price IS NULL OR p.cache_write_price = 0.000006250000)
        THEN COALESCE(p.cache_write_1h_price, 0.000010000000)
        ELSE p.cache_write_1h_price
    END,
    updated_at = NOW()
FROM expanded_pricing_models e
WHERE p.id = e.id
  AND (
      p.models IS DISTINCT FROM e.models
      OR (
          p.input_price = 0.000005000000
          AND p.output_price = 0.000025000000
          AND p.cache_read_price = 0.000000500000
          AND (p.cache_write_price IS NULL OR p.cache_write_price = 0.000006250000)
          AND (p.cache_write_5m_price IS NULL OR p.cache_write_1h_price IS NULL)
      )
  );
