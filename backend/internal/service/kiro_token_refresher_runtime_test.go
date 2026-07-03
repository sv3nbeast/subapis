package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKiroTokenRefresherNeedsRefreshMissingExpiresAtWithRefreshTokenRuntime(t *testing.T) {
	refresher := NewKiroTokenRefresher(nil)
	account := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
		},
	}

	require.True(t, refresher.NeedsRefresh(account, 30*time.Minute))
}

func TestKiroTokenRefresherNeedsRefreshMissingExpiresAtWithoutRefreshTokenRuntime(t *testing.T) {
	refresher := NewKiroTokenRefresher(nil)
	account := &Account{
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "access-token",
		},
	}

	require.False(t, refresher.NeedsRefresh(account, 30*time.Minute))
}
