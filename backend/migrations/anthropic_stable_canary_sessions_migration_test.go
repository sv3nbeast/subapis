package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicStableCanarySessionsMigrationIsNarrowAndOpaque(t *testing.T) {
	raw, err := FS.ReadFile("223_anthropic_stable_canary_sessions.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "create table if not exists anthropic_stable_canary_session_keys")
	require.Contains(t, sql, "create table if not exists anthropic_stable_canary_sessions")
	require.Contains(t, sql, "generation bigint not null")
	require.Contains(t, sql, "key_fingerprint varchar(64) not null")
	require.Contains(t, sql, "session_hash varchar(64) not null")
	require.Contains(t, sql, "unique (group_id, generation, session_hash)")
	require.Contains(t, sql, "references accounts(id) on delete restrict")
	require.Contains(t, sql, "references users(id) on delete restrict")
	require.Contains(t, sql, "foreign key (group_id, account_id, generation)")
	require.Contains(t, sql, "references anthropic_stable_canary_session_keys (group_id, account_id, generation)")
	require.Contains(t, sql, "key_fingerprint")
	require.Contains(t, sql, "policy_fingerprint")
	require.NotContains(t, sql, "session_uuid")
	require.NotContains(t, sql, "raw_session")
	require.NotContains(t, sql, "drop table")
	require.NotContains(t, sql, "alter table")
}
