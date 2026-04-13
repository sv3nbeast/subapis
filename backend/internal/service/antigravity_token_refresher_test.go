//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAntigravityTokenRefresher_NeedsRefresh_Delayed401Refresh(t *testing.T) {
	refresher := &AntigravityTokenRefresher{}
	account := &Account{
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"expires_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}

	account.Credentials[antigravityOAuth401RefreshAtKey] = time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	require.False(t, refresher.NeedsRefresh(account, 0), "delayed refresh should not fire before due time")

	account.Credentials[antigravityOAuth401RefreshAtKey] = time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	require.True(t, refresher.NeedsRefresh(account, 0), "delayed refresh should fire after due time")
}
