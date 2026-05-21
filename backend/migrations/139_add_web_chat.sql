-- Web Chat MVP: hidden managed API keys and persisted chat sessions.

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS source varchar(32) NOT NULL DEFAULT 'user';

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS is_hidden boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_api_keys_source_hidden
    ON api_keys(source, is_hidden)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_web_chat_user_group_active
    ON api_keys(user_id, group_id)
    WHERE deleted_at IS NULL
      AND source = 'web_chat'
      AND is_hidden = true
      AND group_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS web_chat_sessions (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id bigint NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    model varchar(255) NOT NULL,
    title varchar(200) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_web_chat_sessions_user_updated
    ON web_chat_sessions(user_id, updated_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_web_chat_sessions_group
    ON web_chat_sessions(group_id)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS web_chat_messages (
    id bigserial PRIMARY KEY,
    session_id bigint NOT NULL REFERENCES web_chat_sessions(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role varchar(20) NOT NULL,
    content text NOT NULL DEFAULT '',
    status varchar(20) NOT NULL DEFAULT 'completed',
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_web_chat_messages_session_created
    ON web_chat_messages(session_id, created_at ASC, id ASC);

CREATE INDEX IF NOT EXISTS idx_web_chat_messages_user_created
    ON web_chat_messages(user_id, created_at DESC);

