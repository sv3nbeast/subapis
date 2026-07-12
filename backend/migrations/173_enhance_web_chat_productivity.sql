-- Web Chat productivity: session settings, branching and persisted usage metadata.

ALTER TABLE web_chat_sessions
    ADD COLUMN IF NOT EXISTS pinned_at timestamptz,
    ADD COLUMN IF NOT EXISTS system_prompt text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS temperature double precision,
    ADD COLUMN IF NOT EXISTS max_output_tokens integer NOT NULL DEFAULT 8192;

ALTER TABLE web_chat_messages
    ADD COLUMN IF NOT EXISTS deleted_at timestamptz,
    ADD COLUMN IF NOT EXISTS request_id varchar(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS input_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_read_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_creation_tokens bigint NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_web_chat_sessions_user_pinned_updated
    ON web_chat_sessions(user_id, ((pinned_at IS NOT NULL)) DESC, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_web_chat_sessions_user_title_lower
    ON web_chat_sessions(user_id, lower(title))
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_web_chat_messages_active_session_created
    ON web_chat_messages(session_id, created_at ASC, id ASC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_web_chat_messages_streaming
    ON web_chat_messages(session_id, updated_at)
    WHERE deleted_at IS NULL AND status = 'streaming';

ALTER TABLE web_chat_sessions
    DROP CONSTRAINT IF EXISTS chk_web_chat_temperature;
ALTER TABLE web_chat_sessions
    ADD CONSTRAINT chk_web_chat_temperature
    CHECK (temperature IS NULL OR (temperature >= 0 AND temperature <= 2));

ALTER TABLE web_chat_sessions
    DROP CONSTRAINT IF EXISTS chk_web_chat_max_output_tokens;
ALTER TABLE web_chat_sessions
    ADD CONSTRAINT chk_web_chat_max_output_tokens
    CHECK (max_output_tokens >= 1 AND max_output_tokens <= 32768);
