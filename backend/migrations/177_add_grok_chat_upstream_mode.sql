ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS grok_chat_upstream_mode VARCHAR(16) NOT NULL DEFAULT 'raw';

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS grok_chat_responses_gray_percent INTEGER NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'groups_grok_chat_upstream_mode_check'
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_grok_chat_upstream_mode_check
            CHECK (grok_chat_upstream_mode IN ('raw', 'responses', 'gray'));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'groups_grok_chat_responses_gray_percent_check'
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_grok_chat_responses_gray_percent_check
            CHECK (grok_chat_responses_gray_percent BETWEEN 0 AND 100);
    END IF;
END $$;
