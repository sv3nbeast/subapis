-- Add group-level Kiro endpoint policy.
--
-- q    = existing AWS Q/CodeWhisperer/AmazonQ fallback order
-- krs  = Kiro Runtime Service only
-- auto = existing AWS fallback order, then KRS on retryable endpoint failures

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS kiro_endpoint_mode VARCHAR(16) NOT NULL DEFAULT 'q';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'groups_kiro_endpoint_mode_check'
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_kiro_endpoint_mode_check
            CHECK (kiro_endpoint_mode IN ('q', 'krs', 'auto'));
    END IF;
END $$;
