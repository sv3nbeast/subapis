-- Preserve the original subscription quota reset boundary when an active
-- subscription is renewed before expiry.

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS quota_cycle_start_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS quota_cycle_end_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS quota_cycle_days INT NOT NULL DEFAULT 30;

UPDATE user_subscriptions
SET
    quota_cycle_start_at = COALESCE(quota_cycle_start_at, starts_at),
    quota_cycle_end_at = COALESCE(quota_cycle_end_at, expires_at),
    quota_cycle_days = COALESCE(NULLIF(quota_cycle_days, 0), 30)
WHERE quota_cycle_start_at IS NULL
   OR quota_cycle_end_at IS NULL
   OR quota_cycle_days <= 0;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_quota_cycle_end
    ON user_subscriptions(quota_cycle_end_at)
    WHERE deleted_at IS NULL;

COMMENT ON COLUMN user_subscriptions.quota_cycle_start_at IS 'Current subscription quota cycle start. Renewals before expiry keep the existing cycle boundary.';
COMMENT ON COLUMN user_subscriptions.quota_cycle_end_at IS 'Current subscription quota cycle end. Usage resets when this boundary is reached, independent of extended expires_at.';
COMMENT ON COLUMN user_subscriptions.quota_cycle_days IS 'Quota cycle length in days, normally equal to the subscription package validity days.';
