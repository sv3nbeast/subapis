package service

import (
	"context"
	"strings"
	"time"
)

const droidRefreshWindow = 15 * time.Minute

type DroidTokenRefresher struct {
	droidOAuthService droidAccountTokenRefresher
}

func NewDroidTokenRefresher(droidOAuthService droidAccountTokenRefresher) *DroidTokenRefresher {
	return &DroidTokenRefresher{droidOAuthService: droidOAuthService}
}

func (r *DroidTokenRefresher) CacheKey(account *Account) string {
	return DroidTokenCacheKey(account)
}

func (r *DroidTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil && account.Platform == PlatformDroid && account.Type == AccountTypeOAuth
}

func (r *DroidTokenRefresher) NeedsRefresh(account *Account, _ time.Duration) bool {
	if !r.CanRefresh(account) {
		return false
	}
	if strings.TrimSpace(account.GetCredential("access_token")) == "" && strings.TrimSpace(account.GetCredential("refresh_token")) != "" {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return false
	}
	return time.Until(*expiresAt) <= droidRefreshWindow
}

func (r *DroidTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	tokenInfo, err := r.droidOAuthService.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}
	newCredentials := r.droidOAuthService.BuildAccountCredentials(tokenInfo)
	return MergeCredentials(account.Credentials, newCredentials), nil
}
