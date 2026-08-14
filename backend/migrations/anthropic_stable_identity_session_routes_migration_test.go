package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicStableIdentitySessionRoutesMigrationIsDurableAndOpaque(t *testing.T) {
	raw, err := FS.ReadFile("225_anthropic_stable_identity_session_routes.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Contains(t, sql, "create table if not exists anthropic_stable_identity_session_routes")
	require.Contains(t, sql, "group_id bigint not null")
	require.Contains(t, sql, "owner_user_id bigint not null")
	require.Contains(t, sql, "session_hash varchar(64) not null")
	require.Contains(t, sql, "account_id bigint not null")
	require.Contains(t, sql, "account_generation bigint not null")
	require.Contains(t, sql, "identity_fingerprint varchar(64) not null")
	require.Contains(t, sql, "unique (group_id, session_hash)")
	require.Contains(t, sql, "idx_anthropic_stable_identity_session_routes_last_seen")
	require.NotContains(t, sql, "references users")
	require.NotContains(t, sql, "references accounts")
	require.Contains(t, sql, "fail-closed tombstone")
	require.NotContains(t, sql, "session_uuid")
	require.NotContains(t, sql, "raw_session")
	require.NotContains(t, sql, "client_session")
	require.NotContains(t, sql, "on delete cascade")
	require.NotContains(t, sql, "drop table")
}
