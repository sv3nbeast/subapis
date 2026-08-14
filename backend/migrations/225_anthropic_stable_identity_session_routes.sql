-- Durable account selection for account-scoped Anthropic stable identity pools.
-- A raw Claude session UUID is never stored: session_hash is an HMAC-SHA256
-- that includes the authenticated owner and current API-key group.
-- Rows are opaque tombstones. Pool membership changes never rewrite an
-- existing conversation onto a different upstream account/device identity.

CREATE TABLE IF NOT EXISTS anthropic_stable_identity_session_routes (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL CHECK (group_id > 0),
    -- Deliberately no foreign key: this row is a fail-closed tombstone and
    -- must outlive normal user/account deletion without blocking operations.
    owner_user_id BIGINT NOT NULL CHECK (owner_user_id > 0),
    session_hash VARCHAR(64) NOT NULL,
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    account_generation BIGINT NOT NULL CHECK (account_generation > 0),
    identity_fingerprint VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT anthropic_stable_identity_session_routes_hash_shape
        CHECK (session_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT anthropic_stable_identity_session_routes_identity_shape
        CHECK (identity_fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT anthropic_stable_identity_session_routes_group_session_unique
        UNIQUE (group_id, session_hash)
);

CREATE INDEX IF NOT EXISTS idx_anthropic_stable_identity_session_routes_owner
    ON anthropic_stable_identity_session_routes (owner_user_id, group_id);

CREATE INDEX IF NOT EXISTS idx_anthropic_stable_identity_session_routes_account
    ON anthropic_stable_identity_session_routes (account_id, account_generation, group_id);

-- No automatic deletion is performed: removing a tombstone could silently
-- move an old Claude conversation to a different fixed identity. This index
-- keeps age/volume audits inexpensive without weakening that invariant.
CREATE INDEX IF NOT EXISTS idx_anthropic_stable_identity_session_routes_last_seen
    ON anthropic_stable_identity_session_routes (last_seen_at);
