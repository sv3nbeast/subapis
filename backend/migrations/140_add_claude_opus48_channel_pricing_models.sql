-- Add Claude Opus 4.8 aliases to existing official Anthropic channel pricing rows.
-- This only extends channel_model_pricing.models for platform='anthropic'; it does
-- not change Antigravity or Bedrock mappings.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

WITH target_pricing AS (
    SELECT
        id,
        models
    FROM channel_model_pricing
    WHERE platform = 'anthropic'
      AND (
          models @> '["claude-opus-4-6"]'::jsonb
          OR models @> '["claude-opus-4-6-thinking"]'::jsonb
          OR models @> '["claude-opus-4-7"]'::jsonb
          OR models @> '["claude-opus-4-7-thinking"]'::jsonb
      )
),
expanded_models AS (
    SELECT
        id,
        jsonb_agg(model ORDER BY first_order) AS models
    FROM (
        SELECT id, model, MIN(sort_order) AS first_order
        FROM (
            SELECT id, model, ordinality AS sort_order
            FROM target_pricing
            CROSS JOIN LATERAL jsonb_array_elements_text(models) WITH ORDINALITY AS existing_models(model, ordinality)
            UNION ALL
            SELECT id, 'claude-opus-4-8', 1000000
            FROM target_pricing
            WHERE NOT models @> '["claude-opus-4-8"]'::jsonb
            UNION ALL
            SELECT id, 'claude-opus-4-8-thinking', 1000001
            FROM target_pricing
            WHERE NOT models @> '["claude-opus-4-8-thinking"]'::jsonb
        ) ordered_models
        GROUP BY id, model
    ) deduped_models
    GROUP BY id
)
UPDATE channel_model_pricing cmp
SET models = expanded_models.models,
    updated_at = NOW()
FROM expanded_models
WHERE cmp.id = expanded_models.id
  AND cmp.models IS DISTINCT FROM expanded_models.models;
