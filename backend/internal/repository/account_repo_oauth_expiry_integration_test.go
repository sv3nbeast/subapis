//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOAuth401ExpiryPostgresPreservesRotatedTokensAndOutbox(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	account := mustCreateAccount(t, tx.Client(), &service.Account{Name: "oauth-401-cas", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "old-access", "refresh_token": "old-refresh", "expires_at": "2030-01-01T00:00:00Z"}})
	stale, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	rotated := map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_at": "2031-01-01T00:00:00Z"}
	_, err = tx.Client().Account.UpdateOneID(account.ID).SetCredentials(rotated).Save(ctx)
	require.NoError(t, err)
	countOutbox := func() int {
		rows, err := tx.QueryContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", account.ID)
		require.NoError(t, err)
		defer rows.Close()
		require.True(t, rows.Next())
		var count int
		require.NoError(t, rows.Scan(&count))
		return count
	}
	before := countOutbox()
	now := time.Now().UTC().Truncate(time.Second)
	applied, err := repo.ExpireOAuthCredentialsIfUnchanged(ctx, stale, now)
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, before, countOutbox())
	fresh, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, rotated, fresh.Credentials)
	wrongProxy := *fresh
	proxyID := int64(999999)
	wrongProxy.ProxyID = &proxyID
	applied, err = repo.ExpireOAuthCredentialsIfUnchanged(ctx, &wrongProxy, now)
	require.NoError(t, err)
	require.False(t, applied)
	applied, err = repo.ExpireOAuthCredentialsIfUnchanged(ctx, fresh, now)
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, before+1, countOutbox())
	stored, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "new-access", stored.GetCredential("access_token"))
	require.Equal(t, "new-refresh", stored.GetCredential("refresh_token"))
	require.Equal(t, now.Format(time.RFC3339Nano), stored.GetCredential("expires_at"))
	require.Equal(t, "2031-01-01T00:00:00Z", fresh.GetCredential("expires_at"), "caller snapshot stays immutable")
	managed := mustCreateAccount(t, tx.Client(), &service.Account{Name: "oauth-401-managed", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeOAuth, Credentials: rotated})
	expectedManaged, err := repo.GetByID(ctx, managed.ID)
	require.NoError(t, err)
	_, err = tx.Client().Account.UpdateOneID(managed.ID).SetExtra(map[string]any{service.AnthropicStableCanaryReservedExtraKey: true}).Save(ctx)
	require.NoError(t, err)
	applied, err = repo.ExpireOAuthCredentialsIfUnchanged(ctx, expectedManaged, now)
	require.NoError(t, err)
	require.False(t, applied, "a stale unreserved snapshot must not bypass current managed-account guards")
	storedManaged, err := repo.GetByID(ctx, managed.ID)
	require.NoError(t, err)
	require.Equal(t, rotated, storedManaged.Credentials)
}
