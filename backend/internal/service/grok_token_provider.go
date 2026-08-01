package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const (
	grokTokenCacheSkew          = 5 * time.Minute
	grokRequestRefreshTimeout   = 8 * time.Second
	grokRefreshLockWaitTimeout  = 2 * time.Second
	grokRefreshLockPollInterval = 25 * time.Millisecond
)

var (
	errGrokOAuthRefreshNotConfigured = errors.New("grok oauth refresh is not configured")
	errGrokOAuthRefreshTokenMissing  = errors.New("grok oauth refresh token is missing")
	errGrokOAuthAccessTokenMissing   = errors.New("grok oauth access token is missing")
	errGrokOAuthAccessTokenExpired   = errors.New("grok oauth access token is expired")
	errGrokOAuthConfiguredProxyMiss  = errors.New("grok oauth configured proxy is missing")
)

type GrokTokenCache = GeminiTokenCache

type GrokTokenProvider struct {
	accountRepo      AccountRepository
	tokenCache       GrokTokenCache
	refreshAPI       *OAuthRefreshAPI
	executor         OAuthRefreshExecutor
	refreshPolicy    ProviderRefreshPolicy
	tempUnschedCache TempUnschedCache
}

func NewGrokTokenProvider(
	accountRepo AccountRepository,
	tokenCache GrokTokenCache,
) *GrokTokenProvider {
	return &GrokTokenProvider{
		accountRepo:   accountRepo,
		tokenCache:    tokenCache,
		refreshPolicy: GrokProviderRefreshPolicy(),
	}
}

func (p *GrokTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

func (p *GrokTokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

func (p *GrokTokenProvider) SetTempUnschedCache(cache TempUnschedCache) {
	p.tempUnschedCache = cache
}

func (p *GrokTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformGrok || account.Type != AccountTypeOAuth {
		return "", errors.New("not a grok oauth account")
	}
	selectedProxyID := cloneGrokProxyID(account.ProxyID)
	if eligibilityErr := grokOAuthRequestAccountEligibilityError(account); eligibilityErr != nil {
		return "", withGrokCredentialFailureSnapshot(eligibilityErr, account)
	}

	expiresAt := account.GetCredentialAsTime("expires_at")
	accountAccessToken := strings.TrimSpace(account.GetGrokAccessToken())
	refreshToken := strings.TrimSpace(account.GetGrokRefreshToken())
	cacheKey := GrokTokenCacheKey(account)
	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil {
			cachedToken := strings.TrimSpace(token)
			if cachedToken != "" && accountAccessToken != "" && refreshToken != "" && cachedToken == accountAccessToken &&
				grokOAuthAccessTokenHardValidAt(account, time.Now()) {
				return cachedToken, nil
			}
		}
	}

	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= grokTokenRefreshSkew
	if needsRefresh {
		if refreshToken == "" {
			return "", withGrokCredentialFailureSnapshot(errGrokOAuthRefreshTokenMissing, account)
		}
		if p.refreshAPI == nil || p.executor == nil {
			return "", errGrokOAuthRefreshNotConfigured
		}
	}
	if needsRefresh {
		refreshCtx, cancel := context.WithTimeout(ctx, grokRequestRefreshTimeout)
		defer cancel()
		result, err := p.refreshAPI.RefreshIfNeeded(withOAuthRefreshRequestPath(refreshCtx), account, p.executor, grokTokenRefreshSkew)
		if result != nil && result.Account != nil {
			if eligibilityErr := grokOAuthRequestAccountEligibilityError(result.Account); eligibilityErr != nil {
				return "", withGrokCredentialFailureSnapshot(eligibilityErr, result.Account)
			}
			if !grokCredentialProxyIDsEqual(result.Account.ProxyID, selectedProxyID) {
				return "", withGrokCredentialFailureSnapshot(errOAuthRefreshAccountStateChanged, result.Account)
			}
			account = result.Account
			expiresAt = account.GetCredentialAsTime("expires_at")
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", ctxErr
			}
			// RefreshIfNeeded only returns the attempted credential snapshot when
			// the provider refresh call actually ran. State reread, eligibility,
			// and persistence errors return no snapshot and must remain fail-closed.
			if result != nil && result.Account != nil && grokOAuthAccessTokenHardValidAt(account, time.Now()) {
				logGrokWireValidTokenFallback(account, expiresAt, "proactive_refresh_failed", err)
			} else if p.refreshPolicy.OnRefreshError == ProviderRefreshErrorReturn {
				return "", err
			}
		} else if result != nil && result.LockHeld {
			latestAccount, latestErr := p.getCurrentGrokWireValidAccount(refreshCtx, account, selectedProxyID)
			if latestErr == nil {
				account = latestAccount
				expiresAt = account.GetCredentialAsTime("expires_at")
				logGrokWireValidTokenFallback(account, expiresAt, "refresh_lock_held", nil)
			} else if p.refreshPolicy.OnLockHeld == ProviderLockHeldWaitForCache {
				token, waitErr := p.waitForRefreshedToken(refreshCtx, account, cacheKey)
				return token, withGrokCredentialFailureSnapshot(waitErr, account)
			} else {
				return "", withGrokCredentialFailureSnapshot(errGrokOAuthAccessTokenExpired, account)
			}
		}
	} else if accountAccessToken == "" {
		return "", withGrokCredentialFailureSnapshot(errGrokOAuthAccessTokenMissing, account)
	}

	accessToken := account.GetGrokAccessToken()
	if strings.TrimSpace(accessToken) == "" {
		return "", withGrokCredentialFailureSnapshot(errGrokOAuthAccessTokenMissing, account)
	}
	if expiresAt != nil && !time.Now().Before(*expiresAt) {
		return "", withGrokCredentialFailureSnapshot(errGrokOAuthAccessTokenExpired, account)
	}

	if p.tokenCache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, p.accountRepo)
		if isStale && latestAccount != nil {
			if eligibilityErr := grokOAuthRequestAccountEligibilityError(latestAccount); eligibilityErr != nil {
				return "", withGrokCredentialFailureSnapshot(eligibilityErr, latestAccount)
			}
			if !grokCredentialProxyIDsEqual(latestAccount.ProxyID, selectedProxyID) {
				return "", withGrokCredentialFailureSnapshot(errOAuthRefreshAccountStateChanged, latestAccount)
			}
			accessToken = latestAccount.GetGrokAccessToken()
			if strings.TrimSpace(accessToken) == "" {
				return "", withGrokCredentialFailureSnapshot(errGrokOAuthAccessTokenMissing, latestAccount)
			}
			latestExpiry := latestAccount.GetCredentialAsTime("expires_at")
			if latestExpiry == nil || !time.Now().Before(*latestExpiry) {
				return "", withGrokCredentialFailureSnapshot(errGrokOAuthAccessTokenExpired, latestAccount)
			}
		} else {
			ttl := 30 * time.Minute
			if expiresAt != nil {
				until := time.Until(*expiresAt)
				switch {
				case until > grokTokenCacheSkew:
					ttl = until - grokTokenCacheSkew
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

func (p *GrokTokenProvider) getCurrentGrokWireValidAccount(ctx context.Context, selected *Account, selectedProxyID *int64) (*Account, error) {
	if p == nil || p.accountRepo == nil || selected == nil {
		return nil, errOAuthRefreshAccountRereadFailed
	}
	latest, err := p.accountRepo.GetByID(ctx, selected.ID)
	if err != nil || latest == nil {
		return nil, errOAuthRefreshAccountRereadFailed
	}
	if eligibilityErr := grokOAuthRequestAccountEligibilityError(latest); eligibilityErr != nil {
		return nil, eligibilityErr
	}
	if !grokCredentialProxyIDsEqual(latest.ProxyID, selectedProxyID) {
		return nil, errOAuthRefreshAccountStateChanged
	}
	if !grokOAuthAccessTokenHardValidAt(latest, time.Now()) {
		return nil, errGrokOAuthAccessTokenExpired
	}
	return latest, nil
}

func grokOAuthAccessTokenHardValidAt(account *Account, now time.Time) bool {
	if account == nil || !account.IsGrokOAuth() || strings.TrimSpace(account.GetGrokAccessToken()) == "" {
		return false
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	return expiresAt != nil && now.Before(*expiresAt)
}

func logGrokWireValidTokenFallback(account *Account, expiresAt *time.Time, reason string, refreshErr error) {
	attrs := []any{
		"account_id", account.ID,
		"reason", reason,
	}
	if expiresAt != nil {
		attrs = append(attrs, "expires_at", expiresAt.UTC().Format(time.RFC3339), "remaining_seconds", max(0, int(time.Until(*expiresAt).Seconds())))
	}
	if refreshErr != nil {
		attrs = append(attrs, "error", logredact.RedactText(refreshErr.Error()))
	}
	slog.Warn("grok_token_provider.proactive_refresh_failed_using_wire_valid_token", attrs...)
}

// GetAccessTokenForManualTest returns an access token for an admin-initiated
// "test connection" probe. Unlike GetAccessToken it does not apply the
// request-path scheduling eligibility gate (manual Schedulable switch,
// rate-limit / overload / temp-unschedulable cooldowns): a manual test exists
// precisely to check accounts in those states, matching how Codex/OpenAI
// account tests read credentials regardless of scheduling state (#4598).
//
// Credential integrity still applies: the configured-proxy-missing check, the
// shared refresh lock protocol, and the refresh API's own account re-read.
// Credential rotation for non-active (disabled/error) accounts remains
// blocked inside RefreshIfNeeded; their still-valid tokens are probed as-is.
func (p *GrokTokenProvider) GetAccessTokenForManualTest(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformGrok || account.Type != AccountTypeOAuth {
		return "", errors.New("not a grok oauth account")
	}
	if account.ProxyID != nil && account.Proxy == nil {
		return "", errGrokOAuthConfiguredProxyMiss
	}
	accessToken := strings.TrimSpace(account.GetGrokAccessToken())
	expiresAt := account.GetCredentialAsTime("expires_at")
	tokenValid := accessToken != "" && expiresAt != nil && time.Now().Before(*expiresAt)
	if strings.TrimSpace(account.GetGrokRefreshToken()) == "" {
		if tokenValid {
			return accessToken, nil
		}
		return "", errGrokOAuthRefreshTokenMissing
	}
	if accessToken != "" && expiresAt != nil && time.Until(*expiresAt) > grokTokenRefreshSkew {
		return accessToken, nil
	}

	if p.refreshAPI == nil || p.executor == nil {
		if tokenValid {
			return accessToken, nil
		}
		return "", errGrokOAuthRefreshNotConfigured
	}

	// Deliberately not marked as a request-path refresh: the request path
	// re-applies scheduling eligibility inside RefreshIfNeeded, which is
	// exactly what a manual test must bypass.
	refreshCtx, cancel := context.WithTimeout(ctx, grokRequestRefreshTimeout)
	defer cancel()
	result, err := p.refreshAPI.RefreshIfNeeded(refreshCtx, account, p.executor, grokTokenRefreshSkew)
	if err != nil {
		if tokenValid {
			return accessToken, nil
		}
		return "", err
	}
	if result != nil && result.LockHeld {
		if tokenValid {
			return accessToken, nil
		}
		return "", errors.New("token refresh is already in progress on another worker; retry in a few seconds")
	}
	if result != nil && result.Account != nil {
		account = result.Account
	}

	accessToken = strings.TrimSpace(account.GetGrokAccessToken())
	if accessToken == "" {
		return "", errGrokOAuthAccessTokenMissing
	}
	if latestExpiry := account.GetCredentialAsTime("expires_at"); latestExpiry != nil && !time.Now().Before(*latestExpiry) {
		return "", errGrokOAuthAccessTokenExpired
	}
	return accessToken, nil
}

// ForceRefreshAccessToken performs a one-shot refresh after xAI rejects an
// otherwise unexpired access token.
func (p *GrokTokenProvider) ForceRefreshAccessToken(ctx context.Context, account *Account) (string, error) {
	if p == nil {
		return "", errors.New("grok token provider not configured")
	}
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformGrok || account.Type != AccountTypeOAuth {
		return "", errors.New("not a grok oauth account")
	}
	if strings.TrimSpace(account.GetGrokRefreshToken()) == "" {
		return "", withGrokCredentialFailureSnapshot(errGrokOAuthRefreshTokenMissing, account)
	}
	if p.refreshAPI == nil || p.executor == nil {
		return "", errGrokOAuthRefreshNotConfigured
	}

	selectedProxyID := cloneGrokProxyID(account.ProxyID)
	refreshCtx, cancel := context.WithTimeout(ctx, grokRequestRefreshTimeout)
	defer cancel()
	result, err := p.refreshAPI.RefreshIfNeeded(
		withOAuthRefreshRequestPath(refreshCtx),
		account,
		forceOAuthRefreshExecutor{OAuthRefreshExecutor: p.executor},
		0,
	)
	if err != nil {
		return "", withGrokCredentialFailureSnapshot(err, account)
	}
	if result == nil {
		return "", errors.New("grok token refresh returned empty result")
	}
	if result.LockHeld {
		return p.waitForRefreshedToken(refreshCtx, account, GrokTokenCacheKey(account))
	}
	if result.Account != nil {
		if !grokCredentialProxyIDsEqual(result.Account.ProxyID, selectedProxyID) {
			return "", withGrokCredentialFailureSnapshot(errOAuthRefreshAccountStateChanged, result.Account)
		}
		account = result.Account
	}
	accessToken := strings.TrimSpace(account.GetGrokAccessToken())
	if accessToken == "" {
		return "", withGrokCredentialFailureSnapshot(errGrokOAuthAccessTokenMissing, account)
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil || !time.Now().Before(*expiresAt) {
		return "", withGrokCredentialFailureSnapshot(errGrokOAuthAccessTokenExpired, account)
	}
	if p.tokenCache != nil {
		ttl := time.Until(*expiresAt)
		if ttl > grokTokenCacheSkew {
			ttl -= grokTokenCacheSkew
		}
		_ = p.tokenCache.SetAccessToken(ctx, GrokTokenCacheKey(account), accessToken, ttl)
	}
	return accessToken, nil
}

func (p *GrokTokenProvider) waitForRefreshedToken(ctx context.Context, account *Account, cacheKey string) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, grokRefreshLockWaitTimeout)
	defer cancel()

	initialToken := strings.TrimSpace(account.GetGrokAccessToken())
	initialVersion := account.GetCredentialAsInt64("_token_version")
	selectedProxyID := cloneGrokProxyID(account.ProxyID)
	sawAuthoritativeState := false
	var lastAccountReadErr error
	ticker := time.NewTicker(grokRefreshLockPollInterval)
	defer ticker.Stop()

	for {
		cachedToken := ""
		if p.tokenCache != nil {
			if token, err := p.tokenCache.GetAccessToken(waitCtx, cacheKey); err == nil {
				cachedToken = strings.TrimSpace(token)
			}
		}

		if p.accountRepo != nil {
			latest, err := p.accountRepo.GetByID(waitCtx, account.ID)
			if err != nil {
				lastAccountReadErr = err
			} else if latest == nil {
				return "", errOAuthRefreshAccountStateChanged
			} else {
				sawAuthoritativeState = true
				if eligibilityErr := grokOAuthRequestAccountEligibilityError(latest); eligibilityErr != nil {
					return "", withGrokCredentialFailureSnapshot(eligibilityErr, latest)
				}
				if !grokCredentialProxyIDsEqual(latest.ProxyID, selectedProxyID) {
					return "", withGrokCredentialFailureSnapshot(errOAuthRefreshAccountStateChanged, latest)
				}
				token := strings.TrimSpace(latest.GetGrokAccessToken())
				version := latest.GetCredentialAsInt64("_token_version")
				expiresAt := latest.GetCredentialAsTime("expires_at")
				changed := token != initialToken || (version > 0 && version > initialVersion)
				valid := expiresAt != nil && time.Now().Before(*expiresAt)
				if token != "" && changed && valid {
					// The versioned DB credential is authoritative. A stale cache must
					// not hold the request on the old expired token; repair it best-effort.
					if cachedToken != "" && cachedToken != token {
						ttl := time.Until(*expiresAt)
						if ttl > grokTokenCacheSkew {
							ttl -= grokTokenCacheSkew
						}
						_ = p.tokenCache.SetAccessToken(waitCtx, cacheKey, token, ttl)
					}
					return token, nil
				}
			}
		}

		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if !sawAuthoritativeState {
				if lastAccountReadErr == nil {
					lastAccountReadErr = waitCtx.Err()
				}
				return "", fmt.Errorf("%w: %v", errOAuthRefreshAccountRereadFailed, lastAccountReadErr)
			}
			// Another worker still owns the refresh and the authoritative row is
			// unchanged. Do not quarantine the old credential: its refresh may
			// commit immediately after this bounded wait.
			return "", errOAuthRefreshAccountStateChanged
		case <-ticker.C:
		}
	}
}

func grokOAuthRequestAccountEligibilityError(account *Account) error {
	if account == nil || !account.IsGrokOAuth() {
		return errOAuthRefreshAccountStateChanged
	}
	// Persisted accounts always carry status/schedulable values. Unit callers
	// and refresh fixtures may omit those zero-value fields; treat that fixture
	// shape as the active baseline while still enforcing explicit state flags.
	if account.Status != "" && !account.IsActive() {
		return errOAuthRefreshAccountStateChanged
	}
	if account.Status != "" && !account.Schedulable {
		return errOAuthRefreshAccountStateChanged
	}
	now := time.Now()
	if account.OverloadUntil != nil && now.Before(*account.OverloadUntil) {
		return errOAuthRefreshAccountStateChanged
	}
	if account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt) {
		return errOAuthRefreshAccountStateChanged
	}
	if account.TempUnschedulableUntil != nil && now.Before(*account.TempUnschedulableUntil) {
		return errOAuthRefreshAccountStateChanged
	}
	if account.ProxyID != nil && account.Proxy == nil {
		return errGrokOAuthConfiguredProxyMiss
	}
	return nil
}

func cloneGrokProxyID(proxyID *int64) *int64 {
	if proxyID == nil {
		return nil
	}
	value := *proxyID
	return &value
}

func (p *GrokTokenProvider) InvalidateToken(ctx context.Context, account *Account) error {
	if p == nil || p.tokenCache == nil || account == nil {
		return nil
	}
	return p.tokenCache.DeleteAccessToken(ctx, GrokTokenCacheKey(account))
}

func GrokTokenCacheKey(account *Account) string {
	if account == nil {
		return "grok:account:0"
	}
	return "grok:account:" + strconv.FormatInt(account.ID, 10)
}
