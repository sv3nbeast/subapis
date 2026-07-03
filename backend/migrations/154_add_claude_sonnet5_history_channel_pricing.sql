-- Add Claude Sonnet 5 channel pricing to the Claude history-model channel.
--
-- Migration 150 added Sonnet 5 to "claude-最新模型" only. Some production
-- environments also gate Claude access through a separate history-model channel,
-- so that channel needs its own pricing row for restrict_models to allow the
-- model.
--
-- Pricing matches the current mirrored model-price-repo promotional pricing
-- through 2026-08-31.
--
-- Idempotent + environment-safe: no-op if the channel is absent or the row
-- already exists.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

WITH target_channels AS (
    SELECT id
    FROM channels
    WHERE name IN ('Claude 历史模型', 'claude-历史模型', 'claude历史模型')
       OR lower(regexp_replace(name, '[[:space:]-]+', '', 'g')) = 'claude历史模型'
)
INSERT INTO channel_model_pricing
    (channel_id, models, input_price, output_price, cache_write_price,
     cache_read_price, image_output_price, billing_mode, platform,
     cache_write_5m_price, cache_write_1h_price)
SELECT
    c.id,
    '["claude-sonnet-5"]'::jsonb,
    0.000002000000,  -- input        $2   / MTok
    0.000010000000,  -- output       $10  / MTok
    0.000002500000,  -- cache_write  $2.5 / MTok (legacy/default = 5m create)
    0.000000200000,  -- cache_read   $0.20/ MTok
    0,
    'token',
    'anthropic',
    0.000002500000,  -- cache_write_5m  $2.5 / MTok
    0.000004000000   -- cache_write_1h  $4   / MTok
FROM target_channels c
WHERE NOT EXISTS (
    SELECT 1
    FROM channel_model_pricing p
    WHERE p.channel_id = c.id
      AND p.platform = 'anthropic'
      AND p.models @> '["claude-sonnet-5"]'::jsonb
);
