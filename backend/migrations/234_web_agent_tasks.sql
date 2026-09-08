-- Durable, user-owned work. No credentials or raw upstream requests are stored.
CREATE TABLE IF NOT EXISTS web_agent_tasks (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id BIGINT NOT NULL REFERENCES web_chat_sessions(id) ON DELETE CASCADE,
    group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    model VARCHAR(200) NOT NULL,
    kind VARCHAR(32) NOT NULL CHECK (kind IN ('slides','spreadsheet','document')),
    prompt TEXT NOT NULL CHECK (length(prompt) BETWEEN 1 AND 20000),
    document_ids JSONB NOT NULL DEFAULT '[]',
    session_snapshot JSONB NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','running','cancel_requested','succeeded','failed','cancelled','interrupted')),
    result JSONB,
    error_code VARCHAR(100) NOT NULL DEFAULT '',
    step_count INTEGER NOT NULL DEFAULT 0 CHECK (step_count BETWEEN 0 AND 16),
    lease_token VARCHAR(64),
    lease_expires_at TIMESTAMPTZ,
    deadline_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    UNIQUE(user_id,idempotency_key),
    CHECK ((status IN ('running','cancel_requested')) = (lease_token IS NOT NULL)),
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)),
    CHECK ((status IN ('succeeded','failed','cancelled','interrupted')) = (finished_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS web_agent_tasks_owner_recent ON web_agent_tasks(user_id,session_id,id DESC);
CREATE INDEX IF NOT EXISTS web_agent_tasks_pending ON web_agent_tasks(id) WHERE status='queued';
CREATE INDEX IF NOT EXISTS web_agent_tasks_leases ON web_agent_tasks(lease_expires_at) WHERE lease_token IS NOT NULL;
CREATE TABLE IF NOT EXISTS web_agent_task_events (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES web_agent_tasks(id) ON DELETE CASCADE,
    type VARCHAR(64) NOT NULL,
    data JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS web_agent_task_events_cursor ON web_agent_task_events(task_id,id);
