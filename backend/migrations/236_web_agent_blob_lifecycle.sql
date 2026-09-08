-- Keep the bounded original JSON representation. JSONB numeric normalization
-- can expand a small exponent into megabytes and is unnecessary for revisions.
ALTER TABLE web_agent_artifacts ALTER COLUMN spec TYPE TEXT USING spec::text;

-- Intentionally no cascading task/user FK: this ownership journal must outlive
-- their deletion until the corresponding physical files have been removed.
CREATE TABLE IF NOT EXISTS web_agent_blob_stages (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL UNIQUE,
    user_id BIGINT NOT NULL,
    lease_token VARCHAR(100) NOT NULL,
    blob_key VARCHAR(100) NOT NULL UNIQUE,
    preview_key VARCHAR(100) NOT NULL UNIQUE,
    state VARCHAR(20) NOT NULL CHECK(state IN ('allocated','ready','published','abandoned')),
    reserved_bytes BIGINT NOT NULL CHECK(reserved_bytes > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS web_agent_blob_stages_owner ON web_agent_blob_stages(user_id);
CREATE INDEX IF NOT EXISTS web_agent_blob_stages_cleanup ON web_agent_blob_stages(state,id);
CREATE TABLE IF NOT EXISTS web_agent_storage_identity (
    id INTEGER PRIMARY KEY CHECK(id=1),
    storage_id UUID NOT NULL
);

INSERT INTO web_agent_blob_stages(task_id,user_id,lease_token,blob_key,preview_key,state,reserved_bytes)
SELECT task_id,user_id,'legacy-published',blob_key,preview_key,'published',size_bytes+preview_bytes+octet_length(spec)
FROM web_agent_artifacts ON CONFLICT(task_id) DO NOTHING;
