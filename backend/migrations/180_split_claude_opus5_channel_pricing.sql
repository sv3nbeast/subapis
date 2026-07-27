-- Give Claude Opus 5 a dedicated, admin-visible channel pricing row.
--
-- Migration 179 safely enabled the model by appending its aliases to an
-- existing Opus row. That makes billing correct, but long mixed model arrays
-- are collapsed in the admin UI and make the new price rule look absent.
--
-- This forward-only follow-up preserves operator pricing:
--   - an existing enabled Opus 5-only row keeps all of its price fields;
--   - otherwise a dedicated row is created with official Opus 5 prices;
--   - Opus 5 aliases are removed from other enabled rows in the same scope;
--   - a duplicate row that becomes empty is retained but disabled.
--
-- Account-statistics pricing is independently operator-configured and is not
-- synthesized here. It must be audited separately when adding a model.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

-- Normalize one existing dedicated row per channel/platform. Its prices and
-- intervals are intentionally untouched, including custom operator values.
WITH dedicated_rows AS (
    SELECT
        p.id,
        ROW_NUMBER() OVER (
            PARTITION BY p.channel_id, p.platform
            ORDER BY p.id
        ) AS row_number
    FROM channel_model_pricing p
    WHERE p.enabled = true
      AND p.billing_mode = 'token'
      AND p.models ?| ARRAY['claude-opus-5', 'claude-opus-5-thinking']
      AND NOT EXISTS (
          SELECT 1
          FROM jsonb_array_elements_text(p.models) AS bound_model(model)
          WHERE bound_model.model NOT IN ('claude-opus-5', 'claude-opus-5-thinking')
      )
)
UPDATE channel_model_pricing p
SET models = '["claude-opus-5", "claude-opus-5-thinking"]'::jsonb,
    updated_at = NOW()
FROM dedicated_rows d
WHERE p.id = d.id
  AND d.row_number = 1
  AND p.models IS DISTINCT FROM '["claude-opus-5", "claude-opus-5-thinking"]'::jsonb;

-- Create an official dedicated row only when the scope has an enabled Opus 5
-- binding but no enabled dedicated row. This includes the mixed rows produced
-- by migration 179 and remains safe on operator-customized installations.
WITH target_scopes AS (
    SELECT DISTINCT p.channel_id, p.platform
    FROM channel_model_pricing p
    WHERE p.enabled = true
      AND p.billing_mode = 'token'
      AND p.models ?| ARRAY['claude-opus-5', 'claude-opus-5-thinking']
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
    '["claude-opus-5", "claude-opus-5-thinking"]'::jsonb,
    'token',
    0.000005000000,  -- input          $5.00 / MTok
    0.000025000000,  -- output        $25.00 / MTok
    0.000006250000,  -- cache write    $6.25 / MTok (legacy/default = 5m)
    0.000006250000,  -- cache write 5m $6.25 / MTok
    0.000010000000,  -- cache write 1h $10.00 / MTok
    0.000000500000,  -- cache read     $0.50 / MTok
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
      AND existing.models ?| ARRAY['claude-opus-5', 'claude-opus-5-thinking']
      AND NOT EXISTS (
          SELECT 1
          FROM jsonb_array_elements_text(existing.models) AS bound_model(model)
          WHERE bound_model.model NOT IN ('claude-opus-5', 'claude-opus-5-thinking')
      )
);

-- Keep the first enabled dedicated row as the canonical binding, then remove
-- both aliases from every other enabled row in that channel/platform.
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
    WHERE p.enabled = true
      AND p.billing_mode = 'token'
      AND p.models ?| ARRAY['claude-opus-5', 'claude-opus-5-thinking']
      AND NOT EXISTS (
          SELECT 1
          FROM jsonb_array_elements_text(p.models) AS bound_model(model)
          WHERE bound_model.model NOT IN ('claude-opus-5', 'claude-opus-5-thinking')
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
                WHERE bound_model.model NOT IN ('claude-opus-5', 'claude-opus-5-thinking')
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
      AND p.models ?| ARRAY['claude-opus-5', 'claude-opus-5-thinking']
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
