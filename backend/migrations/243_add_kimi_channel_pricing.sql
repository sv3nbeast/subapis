-- Add an effective pricing channel for the production Kimi group.
--
-- The group predates its channel configuration, so it is usable but absent
-- from the user-facing available-channel catalog and has no channel-specific
-- prices. Keep model restriction disabled to preserve the group's current
-- routing behavior while applying explicit prices to the advertised models.
--
-- Prices are USD per token. The group's rate_multiplier remains independent
-- and is applied later by the billing/display layers.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

WITH target_group AS (
    SELECT id
    FROM groups
    WHERE name = 'Kimi最新模型'
      AND platform = 'kimi'
      AND deleted_at IS NULL
    LIMIT 1
), upsert_channel AS (
    INSERT INTO channels (
        name, description, status, model_mapping, billing_model_source,
        restrict_models, apply_pricing_to_account_stats, features_config,
        features, display_only
    )
    SELECT
        'Kimi 模型',
        'Kimi official model pricing',
        'active',
        '{}'::jsonb,
        'requested',
        false,
        false,
        '{}'::jsonb,
        '',
        false
    WHERE EXISTS (SELECT 1 FROM target_group)
    ON CONFLICT (name) DO UPDATE SET
        description = EXCLUDED.description,
        status = 'active',
        billing_model_source = 'requested',
        restrict_models = false,
        display_only = false,
        updated_at = now()
    RETURNING id
), channel_row AS (
    SELECT id FROM upsert_channel
    UNION ALL
    SELECT id FROM channels WHERE name = 'Kimi 模型'
    LIMIT 1
)
INSERT INTO channel_groups(channel_id, group_id)
SELECT c.id, g.id
FROM channel_row c
CROSS JOIN target_group g
WHERE NOT EXISTS (
    SELECT 1
    FROM channel_groups cg
    JOIN channels existing ON existing.id = cg.channel_id
    WHERE cg.group_id = g.id
      AND existing.status = 'active'
      AND existing.display_only = false
      AND existing.id <> c.id
)
ON CONFLICT (channel_id, group_id) DO NOTHING;

-- Kimi K3: $3 input, $15 output, $0.30 cached input per MTok.
INSERT INTO channel_model_pricing (
    channel_id, platform, models, billing_mode,
    input_price, output_price, cache_write_price, cache_read_price,
    image_output_price, enabled
)
SELECT
    c.id,
    'kimi',
    '["kimi-k3"]'::jsonb,
    'token',
    0.000003000000,
    0.000015000000,
    0.000000000000,
    0.000000300000,
    0,
    true
FROM channels c
JOIN channel_groups cg ON cg.channel_id = c.id
JOIN groups g ON g.id = cg.group_id
WHERE c.name = 'Kimi 模型'
  AND g.name = 'Kimi最新模型'
  AND g.platform = 'kimi'
  AND g.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM channel_model_pricing p
      WHERE p.channel_id = c.id
        AND p.platform = 'kimi'
        AND p.models @> '["kimi-k3"]'::jsonb
  );

-- Kimi K2.7 Code: $0.95 input, $4 output, $0.19 cached input per MTok.
INSERT INTO channel_model_pricing (
    channel_id, platform, models, billing_mode,
    input_price, output_price, cache_write_price, cache_read_price,
    image_output_price, enabled
)
SELECT
    c.id,
    'kimi',
    '["kimi-k2.7-code"]'::jsonb,
    'token',
    0.000000950000,
    0.000004000000,
    0.000000000000,
    0.000000190000,
    0,
    true
FROM channels c
JOIN channel_groups cg ON cg.channel_id = c.id
JOIN groups g ON g.id = cg.group_id
WHERE c.name = 'Kimi 模型'
  AND g.name = 'Kimi最新模型'
  AND g.platform = 'kimi'
  AND g.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM channel_model_pricing p
      WHERE p.channel_id = c.id
        AND p.platform = 'kimi'
        AND p.models @> '["kimi-k2.7-code"]'::jsonb
  );

-- Kimi K2.6: $0.95 input, $4 output, $0.15 cached input per MTok.
INSERT INTO channel_model_pricing (
    channel_id, platform, models, billing_mode,
    input_price, output_price, cache_write_price, cache_read_price,
    image_output_price, enabled
)
SELECT
    c.id,
    'kimi',
    '["kimi-k2.6"]'::jsonb,
    'token',
    0.000000950000,
    0.000004000000,
    0.000000000000,
    0.000000150000,
    0,
    true
FROM channels c
JOIN channel_groups cg ON cg.channel_id = c.id
JOIN groups g ON g.id = cg.group_id
WHERE c.name = 'Kimi 模型'
  AND g.name = 'Kimi最新模型'
  AND g.platform = 'kimi'
  AND g.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM channel_model_pricing p
      WHERE p.channel_id = c.id
        AND p.platform = 'kimi'
        AND p.models @> '["kimi-k2.6"]'::jsonb
  );
