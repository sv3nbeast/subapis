// Source-faithful, namespaced integration of kiro_token_refresher.go from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.

package service

import (
	"context"
	"strings"
	"time"
)

const nianzsKiroRefreshWindow = 15 * time.Minute

type NianzsKiroTokenRefresher struct {
	kiroOAuthService *NianzsKiroOAuthService
}

func NianzsNewKiroTokenRefresher(kiroOAuthService *NianzsKiroOAuthService) *NianzsKiroTokenRefresher {
	return &NianzsKiroTokenRefresher{
		kiroOAuthService: kiroOAuthService,
	}
}

func (r *NianzsKiroTokenRefresher) CacheKey(account *Account) string {
	return NianzsKiroTokenCacheKey(account)
}

func (r *NianzsKiroTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil && account.Platform == PlatformKiro && account.Type == AccountTypeOAuth
}

func (r *NianzsKiroTokenRefresher) NeedsRefresh(account *Account, _ time.Duration) bool {
	if !r.CanRefresh(account) {
		return false
	}
	if strings.TrimSpace(account.GetCredential("refresh_token")) == "" {
		return false
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return true
	}
	return time.Until(*expiresAt) <= nianzsKiroRefreshWindow
}

func (r *NianzsKiroTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	tokenInfo, err := r.kiroOAuthService.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}

	newCredentials := r.kiroOAuthService.BuildAccountCredentials(tokenInfo)
	return MergeCredentials(account.Credentials, newCredentials), nil
}
