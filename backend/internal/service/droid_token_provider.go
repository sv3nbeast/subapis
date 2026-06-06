package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	droidTokenRefreshSkew = 3 * time.Minute
	droidTokenCacheSkew   = 5 * time.Minute
)

type droidAccountTokenRefresher interface {
	RefreshAccountToken(ctx context.Context, account *Account) (*DroidTokenInfo, error)
	BuildAccountCredentials(tokenInfo *DroidTokenInfo) map[string]any
}

type DroidTokenProvider struct {
	accountRepo       AccountRepository
	tokenCache        GeminiTokenCache
	droidOAuthService droidAccountTokenRefresher
	refreshAPI        *OAuthRefreshAPI
	executor          OAuthRefreshExecutor
	refreshPolicy     ProviderRefreshPolicy
}

func NewDroidTokenProvider(
	accountRepo AccountRepository,
	tokenCache GeminiTokenCache,
	droidOAuthService *DroidOAuthService,
) *DroidTokenProvider {
	return &DroidTokenProvider{
		accountRepo:       accountRepo,
		tokenCache:        tokenCache,
		droidOAuthService: droidOAuthService,
		refreshPolicy:     GeminiProviderRefreshPolicy(),
	}
}

func (p *DroidTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

func (p *DroidTokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

func (p *DroidTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformDroid || account.Type != AccountTypeOAuth {
		return "", errors.New("not a droid oauth account")
	}

	cacheKey := DroidTokenCacheKey(account)
	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
	}

	accessToken := strings.TrimSpace(account.GetCredential("access_token"))
	expiresAt := account.GetCredentialAsTime("expires_at")
	needsRefresh := accessToken == "" || expiresAt == nil || time.Until(*expiresAt) <= droidTokenRefreshSkew

	if needsRefresh && p.refreshAPI != nil && p.executor != nil {
		result, err := p.refreshAPI.RefreshIfNeeded(ctx, account, p.executor, droidTokenRefreshSkew)
		if err != nil {
			if p.refreshPolicy.OnRefreshError == ProviderRefreshErrorReturn {
				return "", err
			}
		} else if result.LockHeld {
			if p.refreshPolicy.OnLockHeld == ProviderLockHeldWaitForCache && p.tokenCache != nil {
				if token, cacheErr := p.tokenCache.GetAccessToken(ctx, cacheKey); cacheErr == nil && strings.TrimSpace(token) != "" {
					return token, nil
				}
			}
		} else {
			account = result.Account
			expiresAt = account.GetCredentialAsTime("expires_at")
		}
	} else if needsRefresh && p.tokenCache != nil {
		locked, lockErr := p.tokenCache.AcquireRefreshLock(ctx, cacheKey, 30*time.Second)
		if lockErr == nil && locked {
			defer func() { _ = p.tokenCache.ReleaseRefreshLock(ctx, cacheKey) }()
		}
	}

	accessToken = strings.TrimSpace(account.GetCredential("access_token"))
	if accessToken == "" {
		if strings.TrimSpace(account.GetCredential("refresh_token")) == "" {
			const reason = "droid access_token and refresh_token missing in credentials; reauthorize Droid account"
			if p.accountRepo != nil {
				_ = p.accountRepo.SetError(ctx, account.ID, reason)
			}
			if p.tokenCache != nil {
				_ = p.tokenCache.DeleteAccessToken(ctx, cacheKey)
			}
			return "", errors.New(reason)
		}
		return "", errors.New("access_token not found in credentials")
	}

	if p.tokenCache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, p.accountRepo)
		if isStale && latestAccount != nil {
			accessToken = latestAccount.GetCredential("access_token")
			if strings.TrimSpace(accessToken) == "" {
				return "", errors.New("access_token not found after version check")
			}
		} else {
			ttl := 30 * time.Minute
			if expiresAt != nil {
				until := time.Until(*expiresAt)
				switch {
				case until > droidTokenCacheSkew:
					ttl = until - droidTokenCacheSkew
				case until > 0:
					ttl = until
				default:
					ttl = time.Minute
				}
			}
			_ = p.tokenCache.SetAccessToken(ctx, cacheKey, accessToken, ttl)
		}
	}

	return accessToken, nil
}

func (p *DroidTokenProvider) ForceRefreshAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformDroid || account.Type != AccountTypeOAuth {
		return "", errors.New("not a droid oauth account")
	}
	if p.droidOAuthService == nil {
		return "", errors.New("droid oauth service is nil")
	}

	cacheKey := DroidTokenCacheKey(account)
	lockHeld := false
	if p.tokenCache != nil {
		locked, lockErr := p.tokenCache.AcquireRefreshLock(ctx, cacheKey, 30*time.Second)
		if lockErr == nil && locked {
			lockHeld = true
			defer func() { _ = p.tokenCache.ReleaseRefreshLock(ctx, cacheKey) }()
		}
	}

	if p.accountRepo != nil {
		if latestAccount, err := p.accountRepo.GetByID(ctx, account.ID); err == nil && latestAccount != nil {
			account = latestAccount
		}
	}

	tokenInfo, err := p.droidOAuthService.RefreshAccountToken(ctx, account)
	if err != nil {
		if !lockHeld {
			if latestAccount, stale := CheckTokenVersion(ctx, account, p.accountRepo); stale && latestAccount != nil {
				account = latestAccount
				if accessToken := strings.TrimSpace(account.GetCredential("access_token")); accessToken != "" {
					_ = p.cacheAccessToken(ctx, account, accessToken)
					return accessToken, nil
				}
			}
		}
		if isNonRetryableRefreshError(err) && p.accountRepo != nil {
			_ = p.accountRepo.SetError(ctx, account.ID, "Token refresh failed (non-retryable): "+err.Error())
		}
		return "", err
	}

	newCredentials := MergeCredentials(account.Credentials, p.droidOAuthService.BuildAccountCredentials(tokenInfo))
	newCredentials["_token_version"] = time.Now().UnixMilli()
	if err := persistAccountCredentials(ctx, p.accountRepo, account, newCredentials); err != nil {
		return "", err
	}

	accessToken := strings.TrimSpace(account.GetCredential("access_token"))
	if accessToken == "" {
		accessToken = strings.TrimSpace(tokenInfo.AccessToken)
	}
	if accessToken == "" {
		return "", errors.New("access_token not found after droid refresh")
	}
	if err := p.cacheAccessToken(ctx, account, accessToken); err != nil {
		return "", err
	}
	return accessToken, nil
}

func (p *DroidTokenProvider) cacheAccessToken(ctx context.Context, account *Account, accessToken string) error {
	if p.tokenCache == nil || account == nil || strings.TrimSpace(accessToken) == "" {
		return nil
	}
	ttl := 30 * time.Minute
	if expiresAt := account.GetCredentialAsTime("expires_at"); expiresAt != nil {
		until := time.Until(*expiresAt)
		switch {
		case until > droidTokenCacheSkew:
			ttl = until - droidTokenCacheSkew
		case until > 0:
			ttl = until
		default:
			ttl = time.Minute
		}
	}
	return p.tokenCache.SetAccessToken(ctx, DroidTokenCacheKey(account), accessToken, ttl)
}

func DroidTokenCacheKey(account *Account) string {
	if account == nil {
		return "droid:account:0"
	}
	if refreshToken := strings.TrimSpace(account.GetCredential("refresh_token")); refreshToken != "" {
		return "droid:refresh:" + shortTokenHash(refreshToken)
	}
	return "droid:account:" + strconv.FormatInt(account.ID, 10)
}

func shortTokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}
