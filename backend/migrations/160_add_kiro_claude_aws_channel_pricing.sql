-- Add a dedicated channel-pricing group for the Kiro-backed "Claude-AWS" group.
--
-- Why:
--   Kiro groups use platform='kiro'. Channel pricing is platform-isolated, so
--   the existing Anthropic Claude channels cannot price Kiro usage even when
--   model names look identical. Without this channel, Claude-AWS usage falls
--   through to global model pricing and usage_logs.channel_id remains NULL.
--
-- Pricing:
--   Mirrors the current Claude token prices used by the bundled model pricing
--   table / existing Claude channels, but under platform='kiro'.
--
-- Idempotent + environment-safe:
--   - No-op when the Claude-AWS Kiro group is absent.
--   - Does not steal the group if it is already linked to another active
--     non-display channel.
--   - Does not duplicate pricing rows if they already exist.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

WITH target_group AS (
    SELECT id
    FROM groups
    WHERE name = 'Claude-AWS'
      AND platform = 'kiro'
      AND deleted_at IS NULL
    LIMIT 1
), upsert_channel AS (
    INSERT INTO channels (
        name, description, status, model_mapping, billing_model_source,
        restrict_models, apply_pricing_to_account_stats, features_config,
        features, display_only
    )
    SELECT
        'Kiro Claude-AWS',
        'Dedicated channel pricing for Kiro Claude-AWS group',
        'active',
        '{}'::jsonb,
        'requested',
        true,
        true,
        '{}'::jsonb,
        '',
        false
    WHERE EXISTS (SELECT 1 FROM target_group)
    ON CONFLICT (name) DO UPDATE SET
        description = EXCLUDED.description,
        status = 'active',
        billing_model_source = 'requested',
        restrict_models = true,
        apply_pricing_to_account_stats = true,
        display_only = false,
        updated_at = now()
    RETURNING id
), channel_row AS (
    SELECT id FROM upsert_channel
    UNION ALL
    SELECT id FROM channels WHERE name = 'Kiro Claude-AWS'
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
)
ON CONFLICT (channel_id, group_id) DO NOTHING;

INSERT INTO channel_model_pricing
    (channel_id, platform, models, billing_mode,
     input_price, output_price, cache_write_price, cache_read_price,
     image_output_price)
SELECT
    c.id,
    'kiro',
    '["claude-opus-4-5", "claude-opus-4-5-thinking", "claude-opus-4-5-20251101", "claude-opus-4-6", "claude-opus-4-6-thinking", "claude-opus-4-7", "claude-opus-4-7-thinking", "claude-opus-4-8", "claude-opus-4-8-thinking"]'::jsonb,
    'token',
    0.000005000000,  -- input        $5   / MTok
    0.000025000000,  -- output       $25  / MTok
    0.000006250000,  -- cache_write  $6.25/ MTok
    0.000000500000,  -- cache_read   $0.5 / MTok
    0
FROM channels c
WHERE c.name = 'Kiro Claude-AWS'
  AND NOT EXISTS (
      SELECT 1
      FROM channel_model_pricing p
      WHERE p.channel_id = c.id
        AND p.platform = 'kiro'
        AND p.models @> '["claude-opus-4-8"]'::jsonb
  );

INSERT INTO channel_model_pricing
    (channel_id, platform, models, billing_mode,
     input_price, output_price, cache_write_price, cache_read_price,
     image_output_price)
SELECT
    c.id,
    'kiro',
    '["claude-sonnet-4-5", "claude-sonnet-4-5-20250929", "claude-sonnet-4-6"]'::jsonb,
    'token',
    0.000003000000,  -- input        $3   / MTok
    0.000015000000,  -- output       $15  / MTok
    0.000003750000,  -- cache_write  $3.75/ MTok
    0.000000300000,  -- cache_read   $0.3 / MTok
    0
FROM channels c
WHERE c.name = 'Kiro Claude-AWS'
  AND NOT EXISTS (
      SELECT 1
      FROM channel_model_pricing p
      WHERE p.channel_id = c.id
        AND p.platform = 'kiro'
        AND p.models @> '["claude-sonnet-4-6"]'::jsonb
  );

INSERT INTO channel_model_pricing
    (channel_id, platform, models, billing_mode,
     input_price, output_price, cache_write_price, cache_read_price,
     image_output_price)
SELECT
    c.id,
    'kiro',
    '["claude-haiku-4-5", "claude-haiku-4-5-20251001"]'::jsonb,
    'token',
    0.000001000000,  -- input        $1   / MTok
    0.000005000000,  -- output       $5   / MTok
    0.000001250000,  -- cache_write  $1.25/ MTok
    0.000000100000,  -- cache_read   $0.1 / MTok
    0
FROM channels c
WHERE c.name = 'Kiro Claude-AWS'
  AND NOT EXISTS (
      SELECT 1
      FROM channel_model_pricing p
      WHERE p.channel_id = c.id
        AND p.platform = 'kiro'
        AND p.models @> '["claude-haiku-4-5-20251001"]'::jsonb
  );
