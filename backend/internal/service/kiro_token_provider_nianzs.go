// Source-faithful, namespaced integration of kiro_token_provider.go from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.

package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	nianzsKiroTokenRefreshSkew = 3 * time.Minute
	nianzsKiroTokenCacheSkew   = 5 * time.Minute
)

type NianzsKiroTokenCache = GeminiTokenCache

type nianzsKiroAccountTokenRefresher interface {
	RefreshAccountToken(ctx context.Context, account *Account) (*NianzsKiroTokenInfo, error)
	BuildAccountCredentials(tokenInfo *NianzsKiroTokenInfo) map[string]any
}

type NianzsKiroTokenProvider struct {
	accountRepo      AccountRepository
	tokenCache       NianzsKiroTokenCache
	kiroOAuthService nianzsKiroAccountTokenRefresher
	refreshAPI       *OAuthRefreshAPI
	executor         OAuthRefreshExecutor
	refreshPolicy    ProviderRefreshPolicy
}

func NianzsNewKiroTokenProvider(
	accountRepo AccountRepository,
	tokenCache NianzsKiroTokenCache,
	kiroOAuthService *NianzsKiroOAuthService,
) *NianzsKiroTokenProvider {
	return &NianzsKiroTokenProvider{
		accountRepo:      accountRepo,
		tokenCache:       tokenCache,
		kiroOAuthService: kiroOAuthService,
		refreshPolicy:    GeminiProviderRefreshPolicy(),
	}
}

func (p *NianzsKiroTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

func (p *NianzsKiroTokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

func (p *NianzsKiroTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformKiro || account.Type != AccountTypeOAuth {
		return "", errors.New("not a kiro oauth account")
	}

	cacheKey := NianzsKiroTokenCacheKey(account)
	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
	}

	expiresAt := account.GetCredentialAsTime("expires_at")
	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= nianzsKiroTokenRefreshSkew

	if needsRefresh && p.refreshAPI != nil && p.executor != nil {
		result, err := p.refreshAPI.RefreshIfNeeded(ctx, account, p.executor, nianzsKiroTokenRefreshSkew)
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
			if result.Account != nil {
				account = result.Account
			}
			if len(result.NewCredentials) > 0 {
				account.Credentials = shallowCopyMap(result.NewCredentials)
			}
			expiresAt = account.GetCredentialAsTime("expires_at")
		}
	} else if needsRefresh && p.tokenCache != nil {
		locked, lockErr := p.tokenCache.AcquireRefreshLock(ctx, cacheKey, 30*time.Second)
		if lockErr == nil && locked {
			defer func() { _ = p.tokenCache.ReleaseRefreshLock(ctx, cacheKey) }()
		}
	}

	accessToken := account.GetCredential("access_token")
	if strings.TrimSpace(accessToken) == "" {
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
				case until > nianzsKiroTokenCacheSkew:
					ttl = until - nianzsKiroTokenCacheSkew
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

// NianzsKiroTokenCacheKey 返回账号级 access token 缓存 key。
// 必须账号唯一：曾用 client_id_hash / client_id 作 key，但它们标识的是 Kiro IDE
// 的 OAuth client 注册而非用户身份——同一台机器/同一次 device registration 导入的
// 多个 BuilderId/Enterprise 账号会共用同一个 client_id_hash，导致不同账号的 token、
// 刷新锁、用量查询互相覆盖串用。改用 account.ID，与其它 provider(Claude/OpenAI/Grok)一致。
func NianzsKiroTokenCacheKey(account *Account) string {
	if account == nil {
		return "kiro:account:0"
	}
	return "kiro:account:" + strconv.FormatInt(account.ID, 10)
}

func (p *NianzsKiroTokenProvider) ForceRefreshAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformKiro || account.Type != AccountTypeOAuth {
		return "", errors.New("not a kiro oauth account")
	}
	if p.kiroOAuthService == nil {
		return "", errors.New("kiro oauth service is nil")
	}

	cacheKey := NianzsKiroTokenCacheKey(account)
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

	tokenInfo, err := p.kiroOAuthService.RefreshAccountToken(ctx, account)
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
			errorMsg := "Token refresh failed (non-retryable): " + err.Error()
			_ = p.accountRepo.SetError(ctx, account.ID, errorMsg)
		}
		return "", err
	}

	newCredentials := MergeCredentials(account.Credentials, p.kiroOAuthService.BuildAccountCredentials(tokenInfo))
	newCredentials["_token_version"] = time.Now().UnixMilli()
	if err := persistAccountCredentials(ctx, p.accountRepo, account, newCredentials); err != nil {
		return "", err
	}

	accessToken := strings.TrimSpace(account.GetCredential("access_token"))
	if accessToken == "" {
		accessToken = strings.TrimSpace(tokenInfo.AccessToken)
	}
	if accessToken == "" {
		return "", errors.New("access_token not found after kiro refresh")
	}

	// 刷新成功后解析并回填 profileArn（与后台刷新 postRefreshActions 保持一致）
	_ = nianzsKiroResolveAndPersistProfileArn(ctx, p.accountRepo, account, accessToken)

	if err := p.cacheAccessToken(ctx, account, accessToken); err != nil {
		return "", err
	}
	return accessToken, nil
}

func (p *NianzsKiroTokenProvider) cacheAccessToken(ctx context.Context, account *Account, accessToken string) error {
	if p.tokenCache == nil || account == nil || strings.TrimSpace(accessToken) == "" {
		return nil
	}
	ttl := 30 * time.Minute
	if expiresAt := account.GetCredentialAsTime("expires_at"); expiresAt != nil {
		until := time.Until(*expiresAt)
		switch {
		case until > nianzsKiroTokenCacheSkew:
			ttl = until - nianzsKiroTokenCacheSkew
		case until > 0:
			ttl = until
		default:
			ttl = time.Minute
		}
	}
	return p.tokenCache.SetAccessToken(ctx, NianzsKiroTokenCacheKey(account), accessToken, ttl)
}
