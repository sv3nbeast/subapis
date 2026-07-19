ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS model_quota_ratios JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS model_usage JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN groups.model_quota_ratios IS
    'Subscription model quota ratios: canonical model ID to ratio in the range (0, 1].';

COMMENT ON COLUMN user_subscriptions.model_usage IS
    'Per-model subscription usage in the existing daily, weekly, and monthly quota windows.';
