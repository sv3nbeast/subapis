-- Web Chat V3: project knowledge documents, leased parsing jobs and citation snapshots.

DO $$ BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
EXCEPTION WHEN insufficient_privilege THEN
    RAISE NOTICE 'pg_trgm is unavailable; web chat document search will use full text only';
END $$;

ALTER TABLE web_chat_sessions
    ADD COLUMN IF NOT EXISTS knowledge_enabled boolean NOT NULL DEFAULT true;

ALTER TABLE web_chat_messages
    ADD COLUMN IF NOT EXISTS sources jsonb NOT NULL DEFAULT '[]'::jsonb;

CREATE TABLE IF NOT EXISTS web_chat_documents (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id bigint REFERENCES web_chat_projects(id) ON DELETE CASCADE,
    session_id bigint REFERENCES web_chat_sessions(id) ON DELETE CASCADE,
    original_name varchar(255) NOT NULL,
    content_type varchar(120) NOT NULL,
    extension varchar(16) NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    sha256 varchar(64) NOT NULL,
    object_key varchar(512) NOT NULL,
    status varchar(24) NOT NULL DEFAULT 'uploaded'
        CHECK (status IN ('uploaded','processing','ready','failed','deleting')),
    enabled boolean NOT NULL DEFAULT true,
    error_message varchar(1000) NOT NULL DEFAULT '',
    extracted_chars bigint NOT NULL DEFAULT 0,
    chunk_count integer NOT NULL DEFAULT 0,
    attempt_count integer NOT NULL DEFAULT 0,
    lease_owner varchar(120),
    lease_expires_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT chk_web_chat_document_scope CHECK (
        (project_id IS NOT NULL AND session_id IS NULL) OR
        (project_id IS NULL AND session_id IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_web_chat_documents_project
    ON web_chat_documents(user_id, project_id, status, created_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_web_chat_documents_session
    ON web_chat_documents(user_id, session_id, status, created_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_web_chat_documents_lease
    ON web_chat_documents(next_attempt_at, lease_expires_at, id)
    WHERE deleted_at IS NULL AND status IN ('uploaded','processing','deleting');
CREATE UNIQUE INDEX IF NOT EXISTS idx_web_chat_documents_scope_hash
    ON web_chat_documents(user_id, COALESCE(project_id, 0), COALESCE(session_id, 0), sha256)
    WHERE deleted_at IS NULL AND status <> 'deleting';

CREATE TABLE IF NOT EXISTS web_chat_document_chunks (
    id bigserial PRIMARY KEY,
    document_id bigint NOT NULL REFERENCES web_chat_documents(id) ON DELETE CASCADE,
    chunk_index integer NOT NULL,
    page_number integer,
    location_label varchar(120) NOT NULL DEFAULT '',
    content text NOT NULL,
    search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(document_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_web_chat_document_chunks_search
    ON web_chat_document_chunks USING gin(search_vector);
DO $$ BEGIN
    CREATE INDEX IF NOT EXISTS idx_web_chat_document_chunks_trgm
        ON web_chat_document_chunks USING gin(content gin_trgm_ops);
EXCEPTION WHEN undefined_object THEN
    RAISE NOTICE 'pg_trgm operator class is unavailable; skipping trigram index';
END $$;

CREATE TABLE IF NOT EXISTS web_chat_message_documents (
    message_id bigint NOT NULL REFERENCES web_chat_messages(id) ON DELETE CASCADE,
    document_id bigint NOT NULL REFERENCES web_chat_documents(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(message_id, document_id)
);

CREATE INDEX IF NOT EXISTS idx_web_chat_message_documents_document
    ON web_chat_message_documents(document_id, message_id);
