-- Allow usage_logs.request_type to store cyber-blocked rows.
--
-- Background:
--   service/usage_log.go introduced RequestTypeCyberBlocked = 4 so cyber-policy
--   blocked requests can be recorded explicitly without overloading legacy
--   stream/openai_ws_mode fields.
--   The original 061 migration only allowed request_type IN (0,1,2,3), which
--   causes production inserts to fail with usage_logs_request_type_check once a
--   blocked request tries to write request_type=4.
--
-- Fix:
--   Recreate the CHECK constraint as the current superset 0..4. DROP IF EXISTS
--   keeps the migration idempotent and safe across environments that may
--   already have a widened constraint.
ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_request_type_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_request_type_check
    CHECK (request_type IN (0, 1, 2, 3, 4));
