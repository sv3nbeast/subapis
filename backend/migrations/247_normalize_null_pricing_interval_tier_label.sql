-- Normalize NULL tier_label on pricing intervals (2026-09-24).
--
-- Migration 246 inserted the two grok-4.7 long-context intervals without a
-- tier_label. The column is nullable with no default, so they were stored as
-- NULL, while the repository scanned the column into a plain string. Every
-- channel-cache rebuild then failed with
--   scan interval: sql: Scan error on column index 4, name "tier_label":
--   converting NULL to string is unsupported
-- which broke the admin channel list and dropped channel pricing, routing and
-- attribution for every request until the rows were repaired by hand.
--
-- The repository now reads COALESCE(tier_label, ''), so a NULL can no longer
-- take the cache down. This migration aligns the stored data with that
-- contract and with the existing token-mode rows, which already use ''.
-- Migration 246 is already applied in production and is not edited; on a
-- fresh database this runs immediately after it and repairs its rows.
--
-- Forward-only and safe to run twice.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

UPDATE channel_pricing_intervals
SET tier_label = ''
WHERE tier_label IS NULL;

UPDATE channel_account_stats_pricing_intervals
SET tier_label = ''
WHERE tier_label IS NULL;
