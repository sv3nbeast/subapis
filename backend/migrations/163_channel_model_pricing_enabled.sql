-- Add an enabled switch for channel model pricing entries.
-- Disabled entries keep their configured prices in admin UI, but are ignored by
-- routing allowlist checks, channel billing overrides, available-model display,
-- and account-stats pricing rules.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

ALTER TABLE channel_model_pricing
    ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX IF NOT EXISTS idx_channel_model_pricing_enabled
    ON channel_model_pricing (enabled);

CREATE INDEX IF NOT EXISTS idx_cas_model_pricing_enabled
    ON channel_account_stats_model_pricing (enabled);

COMMENT ON COLUMN channel_model_pricing.enabled IS '定价条目是否启用；关闭后保留价格但运行时忽略';
COMMENT ON COLUMN channel_account_stats_model_pricing.enabled IS '账号统计定价条目是否启用；关闭后保留价格但统计时忽略';
