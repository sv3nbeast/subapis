-- Add Claude Sonnet 5 channel pricing to the "claude-最新模型" channel.
--
-- This makes Sonnet 5 pass the channel model restriction gate in production
-- environments that restrict available models from channel_model_pricing.
--
-- Pricing follows the current mirrored model-price-repo promotional pricing
-- through 2026-08-31.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

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
FROM channels c
WHERE c.name = 'claude-最新模型'
  AND NOT EXISTS (
      SELECT 1
      FROM channel_model_pricing p
      WHERE p.channel_id = c.id
        AND p.platform = 'anthropic'
        AND p.models @> '["claude-sonnet-5"]'::jsonb
  );
