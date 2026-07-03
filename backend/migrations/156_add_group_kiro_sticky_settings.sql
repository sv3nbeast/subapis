-- Add group-level Kiro sticky session settings.
--
-- kiro_auto_sticky_enabled keeps Kiro replay-style clients on a stable account
-- even when their request body conversation id changes every turn.
-- kiro_sticky_session_ttl_seconds controls how long that binding is retained.

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS kiro_auto_sticky_enabled BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS kiro_sticky_session_ttl_seconds INT NOT NULL DEFAULT 3600;
