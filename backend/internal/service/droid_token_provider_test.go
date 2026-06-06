package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type droidTokenProviderRuntimeRepo struct {
	AccountRepository
	account                *Account
	updateCredentialsCalls int
	setErrorCalls          int
	setErrorID             int64
	setErrorMsg            string
}

func (r *droidTokenProviderRuntimeRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	if r.account == nil {
		return nil, errors.New("account not found")
	}
	return r.account, nil
}

func (r *droidTokenProviderRuntimeRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.updateCredentialsCalls++
	if r.account == nil || r.account.ID != id {
		r.account = &Account{ID: id}
	}
	r.account.Credentials = cloneCredentials(credentials)
	return nil
}

func (r *droidTokenProviderRuntimeRepo) SetError(_ context.Context, id int64, errorMsg string) error {
	r.setErrorCalls++
	r.setErrorID = id
	r.setErrorMsg = errorMsg
	return nil
}

type droidTokenProviderRuntimeRefresher struct {
	tokenInfo    *DroidTokenInfo
	err          error
	refreshCalls int32
}

func (s *droidTokenProviderRuntimeRefresher) RefreshAccountToken(context.Context, *Account) (*DroidTokenInfo, error) {
	atomic.AddInt32(&s.refreshCalls, 1)
	if s.err != nil {
		return nil, s.err
	}
	return s.tokenInfo, nil
}

func (s *droidTokenProviderRuntimeRefresher) BuildAccountCredentials(tokenInfo *DroidTokenInfo) map[string]any {
	if tokenInfo == nil {
		return nil
	}
	return map[string]any{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   tokenInfo.ExpiresAt,
	}
}

func TestDroidTokenProviderGetAccessTokenRefreshesMissingAccessTokenRuntime(t *testing.T) {
	account := &Account{
		ID:       42,
		Platform: PlatformDroid,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "old-refresh",
			"expires_at":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &droidTokenProviderRuntimeRepo{account: account}
	cache := &kiroTokenProviderRuntimeCache{lockResult: true}
	refresher := &droidTokenProviderRuntimeRefresher{tokenInfo: &DroidTokenInfo{
		AccessToken: "fresh-access",
		ExpiresAt:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}}
	provider := NewDroidTokenProvider(repo, cache, nil)
	provider.droidOAuthService = refresher
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), NewDroidTokenRefresher(refresher))

	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "fresh-access", token)
	require.Equal(t, int32(1), atomic.LoadInt32(&refresher.refreshCalls))
	require.Equal(t, 1, repo.updateCredentialsCalls)
}

func TestDroidTokenProviderGetAccessTokenMissingTokensRequiresReauthRuntime(t *testing.T) {
	account := &Account{
		ID:       42,
		Platform: PlatformDroid,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &droidTokenProviderRuntimeRepo{account: account}
	cache := &kiroTokenProviderRuntimeCache{lockResult: true}
	provider := NewDroidTokenProvider(repo, cache, nil)

	token, err := provider.GetAccessToken(context.Background(), account)
	require.Error(t, err)
	require.Empty(t, token)
	require.Contains(t, err.Error(), "reauthorize Droid account")
	require.Equal(t, 1, repo.setErrorCalls)
}
