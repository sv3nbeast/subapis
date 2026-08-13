-- Durable session ownership for the opt-in Anthropic stable canary shared mode.
-- Raw Claude session UUIDs are never stored; session_hash is an HMAC-SHA256.
-- These rows are intentionally opaque tombstones. Account/user deletion is
-- restricted, while group_id remains a logical scope because groups are
-- soft-deleted in normal operation and old generations must not be silently
-- reinterpreted after a later configuration rollback.

CREATE TABLE IF NOT EXISTS anthropic_stable_canary_session_keys (
    group_id BIGINT NOT NULL CHECK (group_id > 0),
    generation BIGINT NOT NULL CHECK (generation > 0),
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT CHECK (account_id > 0),
    key_fingerprint VARCHAR(64) NOT NULL,
    policy_fingerprint VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT anthropic_stable_canary_session_keys_fingerprint_shape
        CHECK (key_fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT anthropic_stable_canary_session_keys_policy_shape
        CHECK (policy_fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT stable_canary_session_keys_group_account_generation_unique
        UNIQUE (group_id, account_id, generation),
    CONSTRAINT anthropic_stable_canary_session_keys_generation_unique
        UNIQUE (group_id, generation)
);

CREATE TABLE IF NOT EXISTS anthropic_stable_canary_sessions (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL CHECK (group_id > 0),
    generation BIGINT NOT NULL CHECK (generation > 0),
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT CHECK (owner_user_id > 0),
    session_hash VARCHAR(64) NOT NULL,
    key_fingerprint VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT anthropic_stable_canary_sessions_hash_shape
        CHECK (session_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT anthropic_stable_canary_sessions_key_fingerprint_shape
        CHECK (key_fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT anthropic_stable_canary_sessions_key_parent
        FOREIGN KEY (group_id, account_id, generation)
        REFERENCES anthropic_stable_canary_session_keys (group_id, account_id, generation)
        ON DELETE RESTRICT,
    CONSTRAINT anthropic_stable_canary_sessions_group_session_unique
        UNIQUE (group_id, generation, session_hash)
);

CREATE INDEX IF NOT EXISTS idx_anthropic_stable_canary_sessions_owner
    ON anthropic_stable_canary_sessions (owner_user_id, group_id, generation);

CREATE INDEX IF NOT EXISTS idx_anthropic_stable_canary_sessions_account
    ON anthropic_stable_canary_sessions (account_id, group_id, generation);
