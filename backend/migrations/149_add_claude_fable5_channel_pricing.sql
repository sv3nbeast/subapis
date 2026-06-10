-- Add a Claude Fable 5 channel pricing row to the "claude-最新模型" channel.
--
-- Unlike Opus 4.8 (migration 140), Fable 5 CANNOT be appended to the existing
-- Opus pricing row: these rows use billing_mode='token' and bill by the row's
-- own per-token prices. Fable 5 has its own tier ($10 in / $50 out per MTok),
-- so appending it to the Opus row (input $5 / output $25) would undercharge it
-- by half. It must be a separate row with its own prices.
--
-- Side effect (intended): once Fable 5 appears in this channel's pricing models
-- list, the restrict_models gate (IsModelRestricted) stops rejecting it.
--
-- Scope: only the latest-model channel offers Fable 5 (per ops decision); the
-- "Claude 历史模型" channel is intentionally excluded.
--
-- Idempotent + environment-safe: no-op if the channel is absent or the row
-- already exists.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

INSERT INTO channel_model_pricing
    (channel_id, models, input_price, output_price, cache_write_price,
     cache_read_price, image_output_price, billing_mode, platform,
     cache_write_5m_price, cache_write_1h_price)
SELECT
    c.id,
    '["claude-fable-5"]'::jsonb,
    0.000010000000,  -- input        $10  / MTok
    0.000050000000,  -- output       $50  / MTok
    0.000012500000,  -- cache_write  $12.5/ MTok  (legacy/default = 5m create)
    0.000001000000,  -- cache_read   $1   / MTok
    0,               -- image_output (unused for Fable 5)
    'token',
    'anthropic',
    0.000012500000,  -- cache_write_5m  $12.5 / MTok
    0.000020000000   -- cache_write_1h  $20   / MTok
FROM channels c
WHERE c.name = 'claude-最新模型'
  AND NOT EXISTS (
      SELECT 1
      FROM channel_model_pricing p
      WHERE p.channel_id = c.id
        AND p.platform = 'anthropic'
        AND p.models @> '["claude-fable-5"]'::jsonb
  );
