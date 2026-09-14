package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

const (
	// cursorTokenRefreshSkew 是提前刷新窗口：access_token 剩余寿命低于该值即轮换。
	cursorTokenRefreshSkew = 10 * time.Minute
	// cursorTokenCacheSkew 让缓存比凭证本身早失效，避免缓存命中到临期 token。
	cursorTokenCacheSkew = 5 * time.Minute
)

// CursorCredentialsFromAccount 把账号凭证映射为 Cursor 协议层所需的认证数据。
// machine_id / mac_machine_id 必须是 Cursor 安装的 telemetry ID，checksum 依赖它们。
func CursorCredentialsFromAccount(account *Account) cursor.Credentials {
	if account == nil {
		return cursor.Credentials{}
	}
	return cursor.Credentials{
		AccessToken:   normalizeCursorAccessToken(account.GetCredential("access_token")),
		MachineID:     strings.TrimSpace(account.GetCredential("machine_id")),
		MacMachineID:  strings.TrimSpace(account.GetCredential("mac_machine_id")),
		ClientVersion: strings.TrimSpace(account.GetCredential("client_version")),
		ClientCommit:  strings.TrimSpace(account.GetCredential("client_commit")),
		GhostMode:     account.GetCredential("ghost_mode") == "true",
	}
}

// normalizeCursorAccessToken 去掉 Cursor 客户端导出的 `{userId}::{jwt}` 前缀，
// 上游只接受裸 JWT。
func normalizeCursorAccessToken(token string) string {
	token = strings.TrimSpace(token)
	if i := strings.Index(token, "::"); i >= 0 {
		token = strings.TrimSpace(token[i+2:])
	}
	return token
}

// CursorTokenCacheKey 以 refresh_token 为主键，使同一份凭证在多账号记录间共享缓存。
func CursorTokenCacheKey(account *Account) string {
	if account == nil {
		return "cursor:account:0"
	}
	if refreshToken := strings.TrimSpace(account.GetCredential("refresh_token")); refreshToken != "" {
		return "cursor:refresh:" + shortTokenHash(refreshToken)
	}
	return "cursor:account:" + strconv.FormatInt(account.ID, 10)
}

// buildCursorCredentials 由一次刷新结果生成待持久化的凭证增量。
func buildCursorCredentials(result *cursor.TokenRefreshResult) map[string]any {
	if result == nil {
		return nil
	}
	credentials := map[string]any{
		"access_token": result.AccessToken,
		"expires_at":   result.ExpiresAt(time.Now()).Format(time.RFC3339),
	}
	// Cursor 的轮换是可选的：响应省略 refresh_token 时保留原值。
	if refreshToken := strings.TrimSpace(result.RefreshToken); refreshToken != "" {
		credentials["refresh_token"] = refreshToken
	}
	return credentials
}

// CursorTokenRefresher 实现后台与请求路径共用的 OAuth 刷新契约。
type CursorTokenRefresher struct{}

func NewCursorTokenRefresher() *CursorTokenRefresher {
	return &CursorTokenRefresher{}
}

func (r *CursorTokenRefresher) CacheKey(account *Account) string {
	return CursorTokenCacheKey(account)
}

func (r *CursorTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil &&
		account.Platform == PlatformCursor &&
		account.Type == AccountTypeOAuth &&
		strings.TrimSpace(account.GetCredential("refresh_token")) != ""
}

func (r *CursorTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if !r.CanRefresh(account) {
		return false
	}
	if strings.TrimSpace(account.GetCredential("access_token")) == "" {
		return true
	}
	if refreshWindow <= 0 {
		refreshWindow = cursorTokenRefreshSkew
	}
	expiresAt := cursorAccessTokenExpiry(account)
	if expiresAt == nil {
		// 无法判定寿命时不主动轮换：上游 401 会触发强制刷新。
		return false
	}
	return time.Until(*expiresAt) <= refreshWindow
}

func (r *CursorTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if account == nil {
		return nil, errors.New("cursor refresh: account is nil")
	}
	refreshToken := strings.TrimSpace(account.GetCredential("refresh_token"))
	if refreshToken == "" {
		return nil, errors.New("cursor refresh: refresh_token not found in credentials")
	}
	result, err := cursor.RefreshSessionViaProxy(ctx, refreshToken, cursorAccountProxyURL(account))
	if err != nil {
		return nil, err
	}
	return MergeCredentials(account.Credentials, buildCursorCredentials(result)), nil
}

// cursorAccessTokenExpiry 优先采用凭证里记录的过期时间，回落到 access_token 的
// JWT exp 声明——手工导入的账号通常只有 token 本身。
func cursorAccessTokenExpiry(account *Account) *time.Time {
	if account == nil {
		return nil
	}
	if expiresAt := account.GetCredentialAsTime("expires_at"); expiresAt != nil {
		return expiresAt
	}
	return cursor.AccessTokenExpiry(normalizeCursorAccessToken(account.GetCredential("access_token")))
}

// CursorTokenProvider 在请求路径上交付可用的 access_token，并复用共享的刷新锁。
type CursorTokenProvider struct {
	accountRepo   AccountRepository
	tokenCache    GeminiTokenCache
	refreshAPI    *OAuthRefreshAPI
	executor      OAuthRefreshExecutor
	refreshPolicy ProviderRefreshPolicy
}

func NewCursorTokenProvider(accountRepo AccountRepository, tokenCache GeminiTokenCache) *CursorTokenProvider {
	return &CursorTokenProvider{
		accountRepo:   accountRepo,
		tokenCache:    tokenCache,
		refreshPolicy: GeminiProviderRefreshPolicy(),
	}
}

func (p *CursorTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

// GetAccessToken 返回账号当前可用的 access_token，必要时先轮换。
// 与 Droid 一致，它返回刷新后的 account 以便调用方拿到最新凭证。
func (p *CursorTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, *Account, error) {
	if account == nil {
		return "", nil, errors.New("account is nil")
	}
	if account.Platform != PlatformCursor || account.Type != AccountTypeOAuth {
		return "", account, errors.New("not a cursor oauth account")
	}

	cacheKey := CursorTokenCacheKey(account)
	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			return token, account, nil
		}
	}

	if p.needsRefresh(account) && p.refreshAPI != nil && p.executor != nil {
		result, err := p.refreshAPI.RefreshIfNeeded(ctx, account, p.executor, cursorTokenRefreshSkew)
		switch {
		case err != nil:
			if p.refreshPolicy.OnRefreshError == ProviderRefreshErrorReturn {
				return "", account, err
			}
		case result.LockHeld:
			if p.refreshPolicy.OnLockHeld == ProviderLockHeldWaitForCache && p.tokenCache != nil {
				if token, cacheErr := p.tokenCache.GetAccessToken(ctx, cacheKey); cacheErr == nil && strings.TrimSpace(token) != "" {
					return token, account, nil
				}
			}
		case result.Account != nil:
			account = result.Account
		}
	}

	accessToken := normalizeCursorAccessToken(account.GetCredential("access_token"))
	if accessToken == "" {
		if strings.TrimSpace(account.GetCredential("refresh_token")) == "" {
			const reason = "cursor access_token and refresh_token missing in credentials; reauthorize Cursor account"
			if p.accountRepo != nil {
				_ = p.accountRepo.SetError(ctx, account.ID, reason)
			}
			if p.tokenCache != nil {
				_ = p.tokenCache.DeleteAccessToken(ctx, cacheKey)
			}
			return "", account, errors.New(reason)
		}
		return "", account, errors.New("access_token not found in credentials")
	}

	p.cacheAccessToken(ctx, account, accessToken)
	return accessToken, account, nil
}

// ForceRefreshAccessToken 在上游返回 401 后强制轮换凭证。它必须走共享的
// RefreshNow 而不是裸调 Refresh：并发请求各自刷新会用同一个 refresh_token
// 去竞争，在轮换型供应商上必然打成 invalid_grant，其余 worker 也会继续用旧凭证。
func (p *CursorTokenProvider) ForceRefreshAccessToken(ctx context.Context, account *Account) (string, *Account, error) {
	if account == nil {
		return "", nil, errors.New("account is nil")
	}
	if account.Platform != PlatformCursor || account.Type != AccountTypeOAuth {
		return "", account, errors.New("not a cursor oauth account")
	}
	if p.refreshAPI == nil || p.executor == nil {
		return "", account, errors.New("cursor refresh api is not configured")
	}

	if p.tokenCache != nil {
		_ = p.tokenCache.DeleteAccessToken(ctx, CursorTokenCacheKey(account))
	}

	result, err := p.refreshAPI.RefreshNow(ctx, account, p.executor)
	if err != nil {
		return "", account, err
	}
	if result.Account != nil {
		account = result.Account
	}

	accessToken := normalizeCursorAccessToken(account.GetCredential("access_token"))
	if accessToken == "" {
		return "", account, errors.New("access_token not found after cursor refresh")
	}
	p.cacheAccessToken(ctx, account, accessToken)
	return accessToken, account, nil
}

func (p *CursorTokenProvider) needsRefresh(account *Account) bool {
	if strings.TrimSpace(account.GetCredential("refresh_token")) == "" {
		return false
	}
	if normalizeCursorAccessToken(account.GetCredential("access_token")) == "" {
		return true
	}
	expiresAt := cursorAccessTokenExpiry(account)
	return expiresAt != nil && time.Until(*expiresAt) <= cursorTokenRefreshSkew
}

func (p *CursorTokenProvider) cacheAccessToken(ctx context.Context, account *Account, accessToken string) {
	if p.tokenCache == nil || account == nil || strings.TrimSpace(accessToken) == "" {
		return
	}
	ttl := 30 * time.Minute
	if expiresAt := cursorAccessTokenExpiry(account); expiresAt != nil {
		switch until := time.Until(*expiresAt); {
		case until > cursorTokenCacheSkew:
			ttl = until - cursorTokenCacheSkew
		case until > 0:
			ttl = until
		default:
			ttl = time.Minute
		}
	}
	_ = p.tokenCache.SetAccessToken(ctx, CursorTokenCacheKey(account), accessToken, ttl)
}
