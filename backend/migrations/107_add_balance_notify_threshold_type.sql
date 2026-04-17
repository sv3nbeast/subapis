-- Add missing balance notification threshold type for notify/billing/auth flows
ALTER TABLE users ADD COLUMN IF NOT EXISTS balance_notify_threshold_type TEXT;

UPDATE users
SET balance_notify_threshold_type = 'fixed'
WHERE balance_notify_threshold_type IS NULL OR balance_notify_threshold_type = '';

ALTER TABLE users
ALTER COLUMN balance_notify_threshold_type SET DEFAULT 'fixed';

ALTER TABLE users
ALTER COLUMN balance_notify_threshold_type SET NOT NULL;
