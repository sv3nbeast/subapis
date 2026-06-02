package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type kiroTokenProviderRuntimeRepo struct {
	AccountRepository
	account                *Account
	updateCredentialsCalls int
	setErrorCalls          int
	setErrorID             int64
	setErrorMsg            string
}

func (r *kiroTokenProviderRuntimeRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	if r.account == nil {
		return nil, errors.New("account not found")
	}
	return r.account, nil
}

func (r *kiroTokenProviderRuntimeRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.updateCredentialsCalls++
	if r.account == nil || r.account.ID != id {
		r.account = &Account{ID: id}
	}
	r.account.Credentials = cloneCredentials(credentials)
	return nil
}

func (r *kiroTokenProviderRuntimeRepo) SetError(_ context.Context, id int64, errorMsg string) error {
	r.setErrorCalls++
	r.setErrorID = id
	r.setErrorMsg = errorMsg
	return nil
}

type kiroTokenProviderRuntimeCache struct {
	tokens           map[string]string
	lockResult       bool
	setCalls         int
	releaseLockCalls int
}

func (c *kiroTokenProviderRuntimeCache) GetAccessToken(_ context.Context, cacheKey string) (string, error) {
	if c.tokens == nil {
		return "", nil
	}
	return c.tokens[cacheKey], nil
}

func (c *kiroTokenProviderRuntimeCache) SetAccessToken(_ context.Context, cacheKey string, token string, _ time.Duration) error {
	c.setCalls++
	if c.tokens == nil {
		c.tokens = map[string]string{}
	}
	c.tokens[cacheKey] = token
	return nil
}

func (c *kiroTokenProviderRuntimeCache) DeleteAccessToken(_ context.Context, cacheKey string) error {
	delete(c.tokens, cacheKey)
	return nil
}

func (c *kiroTokenProviderRuntimeCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return c.lockResult, nil
}

func (c *kiroTokenProviderRuntimeCache) ReleaseRefreshLock(context.Context, string) error {
	c.releaseLockCalls++
	return nil
}

type kiroTokenProviderRuntimeRefresher struct {
	tokenInfo    *KiroTokenInfo
	err          error
	refreshCalls int32
}

func (s *kiroTokenProviderRuntimeRefresher) RefreshAccountToken(context.Context, *Account) (*KiroTokenInfo, error) {
	atomic.AddInt32(&s.refreshCalls, 1)
	if s.err != nil {
		return nil, s.err
	}
	return s.tokenInfo, nil
}

func (s *kiroTokenProviderRuntimeRefresher) BuildAccountCredentials(tokenInfo *KiroTokenInfo) map[string]any {
	if tokenInfo == nil {
		return nil
	}
	return map[string]any{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   tokenInfo.ExpiresAt,
	}
}

func TestKiroTokenProviderGetAccessTokenRefreshesMissingAccessTokenRuntime(t *testing.T) {
	account := &Account{
		ID:       42,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "old-refresh",
			"expires_at":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &kiroTokenProviderRuntimeRepo{account: account}
	cache := &kiroTokenProviderRuntimeCache{lockResult: true}
	refresher := &kiroTokenProviderRuntimeRefresher{tokenInfo: &KiroTokenInfo{
		AccessToken: "fresh-access",
		ExpiresAt:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}}
	provider := NewKiroTokenProvider(repo, cache, nil)
	provider.kiroOAuthService = refresher
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), NewKiroTokenRefresher(refresher))

	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "fresh-access", token)
	require.Equal(t, int32(1), atomic.LoadInt32(&refresher.refreshCalls))
	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.Equal(t, "fresh-access", repo.account.GetCredential("access_token"))
}

func TestKiroTokenProviderGetAccessTokenMissingTokensRequiresReauthRuntime(t *testing.T) {
	account := &Account{
		ID:       42,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &kiroTokenProviderRuntimeRepo{account: account}
	cache := &kiroTokenProviderRuntimeCache{lockResult: true}
	refresher := &kiroTokenProviderRuntimeRefresher{err: errors.New("kiro refresh_token is empty; reauthorize Kiro account")}
	provider := NewKiroTokenProvider(repo, cache, nil)
	provider.kiroOAuthService = refresher
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), NewKiroTokenRefresher(refresher))

	token, err := provider.GetAccessToken(context.Background(), account)
	require.Error(t, err)
	require.Empty(t, token)
	require.Contains(t, err.Error(), "reauthorize Kiro account")
	require.Equal(t, int32(0), atomic.LoadInt32(&refresher.refreshCalls))
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, account.ID, repo.setErrorID)
	require.Contains(t, repo.setErrorMsg, "reauthorize Kiro account")
}
