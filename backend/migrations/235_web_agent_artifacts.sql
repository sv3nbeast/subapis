CREATE TABLE IF NOT EXISTS web_agent_artifacts (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL UNIQUE REFERENCES web_agent_tasks(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id BIGINT NOT NULL REFERENCES web_chat_sessions(id) ON DELETE CASCADE,
    lineage_id UUID NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    parent_id BIGINT REFERENCES web_agent_artifacts(id) ON DELETE SET NULL,
    kind VARCHAR(32) NOT NULL CHECK (kind IN ('slides','document','spreadsheet')),
    title VARCHAR(120) NOT NULL,
    filename VARCHAR(200) NOT NULL,
    mime VARCHAR(150) NOT NULL,
    blob_key VARCHAR(100) NOT NULL UNIQUE,
    preview_key VARCHAR(100) NOT NULL UNIQUE,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 33554432),
    preview_bytes BIGINT NOT NULL CHECK (preview_bytes > 0 AND preview_bytes <= 33554432),
    sha256 VARCHAR(64) NOT NULL,
    spec JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(lineage_id,version)
);
CREATE INDEX IF NOT EXISTS web_agent_artifacts_owner ON web_agent_artifacts(user_id,session_id,id DESC);
-- Retain the requested source identifier even if that artifact is deleted.
-- Execution must then fail explicitly, not silently create an unrelated file.
ALTER TABLE web_agent_tasks ADD COLUMN IF NOT EXISTS source_artifact_id BIGINT;
