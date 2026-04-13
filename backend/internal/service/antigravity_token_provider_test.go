//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type antigravityTokenCacheStub struct {
	token        string
	getCalls     int
	setCalls     int
	lastSetToken string
}

func (s *antigravityTokenCacheStub) GetAccessToken(ctx context.Context, cacheKey string) (string, error) {
	s.getCalls++
	return s.token, nil
}

func (s *antigravityTokenCacheStub) SetAccessToken(ctx context.Context, cacheKey string, token string, ttl time.Duration) error {
	s.setCalls++
	s.lastSetToken = token
	return nil
}

func (s *antigravityTokenCacheStub) DeleteAccessToken(context.Context, string) error { return nil }

func (s *antigravityTokenCacheStub) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func (s *antigravityTokenCacheStub) ReleaseRefreshLock(context.Context, string) error { return nil }

func TestAntigravityTokenProvider_GetAccessToken_Upstream(t *testing.T) {
	provider := &AntigravityTokenProvider{}

	t.Run("upstream account with valid api_key", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAntigravity,
			Type:     AccountTypeUpstream,
			Credentials: map[string]any{
				"api_key": "sk-test-key-12345",
			},
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, "sk-test-key-12345", token)
	})

	t.Run("upstream account missing api_key", func(t *testing.T) {
		account := &Account{
			Platform:    PlatformAntigravity,
			Type:        AccountTypeUpstream,
			Credentials: map[string]any{},
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "upstream account missing api_key")
		require.Empty(t, token)
	})

	t.Run("upstream account with empty api_key", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAntigravity,
			Type:     AccountTypeUpstream,
			Credentials: map[string]any{
				"api_key": "",
			},
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "upstream account missing api_key")
		require.Empty(t, token)
	})

	t.Run("upstream account with nil credentials", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAntigravity,
			Type:     AccountTypeUpstream,
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "upstream account missing api_key")
		require.Empty(t, token)
	})
}

func TestAntigravityTokenProvider_GetAccessToken_Guards(t *testing.T) {
	provider := &AntigravityTokenProvider{}

	t.Run("nil account", func(t *testing.T) {
		token, err := provider.GetAccessToken(context.Background(), nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "account is nil")
		require.Empty(t, token)
	})

	t.Run("non-antigravity platform", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeOAuth,
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not an antigravity account")
		require.Empty(t, token)
	})

	t.Run("unsupported account type", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAntigravity,
			Type:     AccountTypeAPIKey,
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not an antigravity oauth account")
		require.Empty(t, token)
	})
}

func TestAntigravityTokenProvider_GetAccessToken_Delayed401RefreshBypassesCacheWhenDue(t *testing.T) {
	account := &Account{
		ID:       88,
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":                  "stale-token",
			"refresh_token":                 "rt-1",
			"expires_at":                    time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
			antigravityOAuth401RefreshAtKey: time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339),
		},
	}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &antigravityTokenCacheStub{token: "cached-token"}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials: map[string]any{
			"access_token": "refreshed-token",
			"expires_at":   time.Now().Add(55 * time.Minute).UTC().Format(time.RFC3339),
		},
	}
	provider := NewAntigravityTokenProvider(repo, cache, nil)
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), executor)

	token, err := provider.GetAccessToken(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "refreshed-token", token)
	require.Equal(t, 0, cache.getCalls, "due delayed refresh should bypass stale cache read")
	require.Equal(t, 1, executor.refreshCalls)
	require.Equal(t, "refreshed-token", cache.lastSetToken)
}
