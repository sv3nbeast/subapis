-- Add Claude Fable 5.1 to persisted Anthropic account/group allowlists and
-- create a dedicated channel-pricing rule with the official rates.
--
-- Scope is intentionally Anthropic-only. Kiro, Bedrock, Vertex, and
-- Antigravity have not been verified for this model.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

-- Explicit model mappings are account-level capability allowlists. Extend only
-- Anthropic accounts that already opted into Fable 5, and leave wildcard
-- mappings under operator control.
WITH target_accounts AS (
    SELECT id, credentials->'model_mapping' AS model_mapping
    FROM accounts
    WHERE platform = 'anthropic'
      AND deleted_at IS NULL
      AND jsonb_typeof(credentials->'model_mapping') = 'object'
      AND credentials->'model_mapping' ? 'claude-fable-5'
      AND NOT EXISTS (
          SELECT 1
          FROM jsonb_object_keys(credentials->'model_mapping') AS mapping_key(key)
          WHERE mapping_key.key LIKE '%*%'
      )
), expanded_accounts AS (
    SELECT
        id,
        model_mapping || '{"claude-fable-5-1":"claude-fable-5-1"}'::jsonb AS model_mapping
    FROM target_accounts
)
UPDATE accounts a
SET credentials = jsonb_set(a.credentials, '{model_mapping}', expanded.model_mapping, true),
    updated_at = NOW()
FROM expanded_accounts expanded
WHERE a.id = expanded.id
  AND a.credentials->'model_mapping' IS DISTINCT FROM expanded.model_mapping;

-- Preserve configured model order, append Fable 5.1 once, and deduplicate
-- pre-existing entries without changing any other models.
WITH target_groups AS (
    SELECT id, models_list_config, models_list_config->'models' AS models
    FROM groups
    WHERE platform = 'anthropic'
      AND deleted_at IS NULL
      AND jsonb_typeof(models_list_config->'models') = 'array'
      AND models_list_config->'models' @> '["claude-fable-5"]'::jsonb
), expanded_group_models AS (
    SELECT id, jsonb_agg(model ORDER BY first_order) AS models
    FROM (
        SELECT id, model, MIN(sort_order) AS first_order
        FROM (
            SELECT id, model, ordinality AS sort_order
            FROM target_groups
            CROSS JOIN LATERAL jsonb_array_elements_text(models)
                WITH ORDINALITY AS existing_models(model, ordinality)
            UNION ALL
            SELECT id, 'claude-fable-5-1', 1000000
            FROM target_groups
            WHERE NOT models @> '["claude-fable-5-1"]'::jsonb
        ) ordered_models
        GROUP BY id, model
    ) deduplicated_models
    GROUP BY id
), expanded_groups AS (
    SELECT
        target.id,
        jsonb_set(target.models_list_config, '{models}', expanded.models, true) AS models_list_config
    FROM target_groups target
    JOIN expanded_group_models expanded USING (id)
)
UPDATE groups g
SET models_list_config = expanded.models_list_config,
    updated_at = NOW()
FROM expanded_groups expanded
WHERE g.id = expanded.id
  AND g.models_list_config IS DISTINCT FROM expanded.models_list_config;

-- Normalize one existing enabled dedicated row per channel. Custom prices and
-- interval rows are deliberately preserved.
WITH dedicated_rows AS (
    SELECT
        p.id,
        ROW_NUMBER() OVER (
            PARTITION BY p.channel_id, p.platform
            ORDER BY p.id
        ) AS row_number
    FROM channel_model_pricing p
    WHERE p.platform = 'anthropic'
      AND p.enabled = true
      AND p.billing_mode = 'token'
      AND p.models @> '["claude-fable-5-1"]'::jsonb
      AND NOT EXISTS (
          SELECT 1
          FROM jsonb_array_elements_text(p.models) AS bound_model(model)
          WHERE bound_model.model <> 'claude-fable-5-1'
      )
)
UPDATE channel_model_pricing p
SET models = '["claude-fable-5-1"]'::jsonb,
    updated_at = NOW()
FROM dedicated_rows dedicated
WHERE p.id = dedicated.id
  AND dedicated.row_number = 1
  AND p.models IS DISTINCT FROM '["claude-fable-5-1"]'::jsonb;

-- Any active Anthropic token-pricing scope that exposes Fable 5 gains one
-- dedicated Fable 5.1 row. A pre-existing mixed Fable 5.1 binding also creates
-- the scope so that the cleanup below cannot remove its only active rule.
WITH target_scopes AS (
    SELECT DISTINCT p.channel_id, p.platform
    FROM channel_model_pricing p
    WHERE p.platform = 'anthropic'
      AND p.enabled = true
      AND p.billing_mode = 'token'
      AND p.models ?| ARRAY['claude-fable-5', 'claude-fable-5-1']
)
INSERT INTO channel_model_pricing (
    channel_id,
    platform,
    models,
    billing_mode,
    input_price,
    output_price,
    cache_write_price,
    cache_write_5m_price,
    cache_write_1h_price,
    cache_read_price,
    image_output_price,
    enabled
)
SELECT
    scope.channel_id,
    scope.platform,
    '["claude-fable-5-1"]'::jsonb,
    'token',
    0.000015000000,  -- input          $15.00 / MTok
    0.000075000000,  -- output         $75.00 / MTok
    0.000018750000,  -- cache write    $18.75 / MTok (legacy/default = 5m)
    0.000018750000,  -- cache write 5m $18.75 / MTok
    0.000030000000,  -- cache write 1h $30.00 / MTok
    0.000000250000,  -- cache read     $0.25 / MTok
    0,
    true
FROM target_scopes scope
WHERE NOT EXISTS (
    SELECT 1
    FROM channel_model_pricing existing
    WHERE existing.channel_id = scope.channel_id
      AND existing.platform = scope.platform
      AND existing.enabled = true
      AND existing.billing_mode = 'token'
      AND existing.models @> '["claude-fable-5-1"]'::jsonb
      AND NOT EXISTS (
          SELECT 1
          FROM jsonb_array_elements_text(existing.models) AS bound_model(model)
          WHERE bound_model.model <> 'claude-fable-5-1'
      )
);

-- Keep the first dedicated row as canonical. Remove Fable 5.1 from every
-- other enabled row in the same channel/platform and disable rows left empty.
WITH ranked_dedicated_rows AS (
    SELECT
        p.id,
        p.channel_id,
        p.platform,
        ROW_NUMBER() OVER (
            PARTITION BY p.channel_id, p.platform
            ORDER BY p.id
        ) AS row_number
    FROM channel_model_pricing p
    WHERE p.platform = 'anthropic'
      AND p.enabled = true
      AND p.billing_mode = 'token'
      AND p.models @> '["claude-fable-5-1"]'::jsonb
      AND NOT EXISTS (
          SELECT 1
          FROM jsonb_array_elements_text(p.models) AS bound_model(model)
          WHERE bound_model.model <> 'claude-fable-5-1'
      )
), canonical_rows AS (
    SELECT id, channel_id, platform
    FROM ranked_dedicated_rows
    WHERE row_number = 1
), rows_to_clean AS (
    SELECT
        p.id,
        COALESCE(
            (
                SELECT jsonb_agg(bound_model.model ORDER BY bound_model.ordinality)
                FROM jsonb_array_elements_text(p.models)
                    WITH ORDINALITY AS bound_model(model, ordinality)
                WHERE bound_model.model <> 'claude-fable-5-1'
            ),
            '[]'::jsonb
        ) AS models
    FROM channel_model_pricing p
    JOIN canonical_rows canonical
      ON canonical.channel_id = p.channel_id
     AND canonical.platform = p.platform
    WHERE p.enabled = true
      AND p.billing_mode = 'token'
      AND p.id <> canonical.id
      AND p.models @> '["claude-fable-5-1"]'::jsonb
)
UPDATE channel_model_pricing p
SET models = cleaned.models,
    enabled = jsonb_array_length(cleaned.models) > 0,
    updated_at = NOW()
FROM rows_to_clean cleaned
WHERE p.id = cleaned.id
  AND (
      p.models IS DISTINCT FROM cleaned.models
      OR (p.enabled AND jsonb_array_length(cleaned.models) = 0)
  );
