-- Allow a subscription plan to grant additional subscription groups and
-- snapshot the complete entitlement set on each order. Existing subscription
-- orders are backfilled with their historical primary group only, so later
-- plan edits never change already-created orders.

ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS bonus_group_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE payment_orders
    ADD COLUMN IF NOT EXISTS subscription_group_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

UPDATE payment_orders
SET subscription_group_ids = jsonb_build_array(subscription_group_id)
WHERE order_type = 'subscription'
  AND subscription_group_id IS NOT NULL
  AND subscription_group_ids = '[]'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'subscription_plans_bonus_group_ids_array_check'
    ) THEN
        ALTER TABLE subscription_plans
            ADD CONSTRAINT subscription_plans_bonus_group_ids_array_check
            CHECK (jsonb_typeof(bonus_group_ids) = 'array');
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'payment_orders_subscription_group_ids_array_check'
    ) THEN
        ALTER TABLE payment_orders
            ADD CONSTRAINT payment_orders_subscription_group_ids_array_check
            CHECK (jsonb_typeof(subscription_group_ids) = 'array');
    END IF;
END $$;

COMMENT ON COLUMN subscription_plans.bonus_group_ids IS
    'Additional subscription group IDs granted with the primary plan group';

COMMENT ON COLUMN payment_orders.subscription_group_ids IS
    'Immutable subscription entitlement snapshot captured when the order is created';
