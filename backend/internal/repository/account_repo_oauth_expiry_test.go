package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOAuth401ExpiryUsesGenerationCASAndAtomicOutbox(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		exec := &recordingSQLExecutor{result: rowsAffectedResult(affected)}
		repo := newAccountRepositoryWithSQL(nil, exec, nil)
		account := &service.Account{ID: 71, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "old-access", "refresh_token": "old-refresh"}}
		at := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
		applied, err := repo.ExpireOAuthCredentialsIfUnchanged(context.Background(), account, at)
		require.NoError(t, err)
		require.Equal(t, affected == 1, applied)
		require.Len(t, exec.execQueries, 1)
		query := normalizeSQLWhitespace(exec.execQueries[0])
		require.Contains(t, query, "jsonb_set(a.credentials, '{expires_at}'")
		require.Contains(t, query, "a.credentials = $5::jsonb")
		require.Contains(t, query, "a.proxy_id IS NOT DISTINCT FROM $6")
		require.Contains(t, query, "a.parent_account_id IS NULL")
		require.Contains(t, query, "INSERT INTO scheduler_outbox")
		require.Len(t, exec.execArgs[0], 7)
		require.Equal(t, at.Format(time.RFC3339Nano), exec.execArgs[0][0])
		require.JSONEq(t, `{"access_token":"old-access","refresh_token":"old-refresh"}`, exec.execArgs[0][4].(string))
		require.NotContains(t, account.Credentials, "expires_at", "request snapshot is never mutated")
	}
}
