package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const (
	kiroListAvailableProfilesTarget = "AmazonCodeWhispererService.ListAvailableProfiles"
	kiroProfileResolveMaxAttempts   = 3
	kiroProfileResolveBodyByteSize  = 2 << 20
)

var (
	kiroProfileResolutionFlight singleflight.Group
	kiroProfileResolutionLocks  sync.Map // map[string]*sync.Mutex
)

func (s *GatewayService) resolveKiroPayloadProfileArn(ctx context.Context, account *Account, accessToken string) string {
	if account == nil || account.Platform != PlatformKiro {
		return ""
	}
	if account.Type == AccountTypeOAuth {
		if profileArn := s.resolveAndPersistKiroProfileArnOnly(ctx, account, accessToken); profileArn != "" {
			return profileArn
		}
	} else if profileArn := strings.TrimSpace(account.GetCredential("profile_arn")); profileArn != "" && !kiroIsPlaceholderProfileARN(profileArn) {
		return profileArn
	}

	if s.kiroTokenProvider != nil && account.Type == AccountTypeOAuth && strings.TrimSpace(account.GetCredential("refresh_token")) != "" {
		if refreshedToken, err := s.kiroTokenProvider.ForceRefreshAccessToken(ctx, account); err == nil {
			if strings.TrimSpace(refreshedToken) != "" {
				if account.Credentials == nil {
					account.Credentials = map[string]any{}
				}
				account.Credentials["access_token"] = refreshedToken
			}
			if s.accountRepo != nil {
				if latest, latestErr := s.accountRepo.GetByID(ctx, account.ID); latestErr == nil && latest != nil {
					account.Credentials = cloneCredentials(latest.Credentials)
				}
			}
			if profileArn := strings.TrimSpace(account.GetCredential("profile_arn")); profileArn != "" {
				return profileArn
			}
		} else {
			logger.L().Warn("kiro profile arn refresh fallback failed",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
		}
	}

	return ""
}

func (s *GatewayService) resolveAndPersistKiroProfileArnOnly(ctx context.Context, account *Account, accessToken string) string {
	if account == nil || account.Platform != PlatformKiro || account.Type != AccountTypeOAuth {
		return ""
	}
	flightKey := kiroProfileResolutionFlightKey(account)
	if profileArn := cachedKiroProfileArnWithLock(flightKey, account); profileArn != "" {
		return profileArn
	}
	value, err, _ := kiroProfileResolutionFlight.Do(flightKey, func() (any, error) {
		if profileArn := cachedKiroProfileArnWithLock(flightKey, account); profileArn != "" {
			return profileArn, nil
		}
		profileArn, err := s.resolveKiroProfileArnFromAvailableProfiles(ctx, account, accessToken)
		if err != nil {
			return "", err
		}
		if profileArn == "" {
			return "", nil
		}
		if persistErr := s.persistKiroResolvedProfileArnWithLock(ctx, flightKey, account, profileArn); persistErr != nil {
			logger.L().Warn("kiro profile arn cache failed",
				zap.Int64("account_id", account.ID),
				zap.Error(persistErr),
			)
		}
		return profileArn, nil
	})
	if err != nil {
		logger.L().Warn("kiro profile arn list failed",
			zap.Int64("account_id", account.ID),
			zap.Error(err),
		)
		return ""
	}
	profileArn, _ := value.(string)
	if profileArn == "" {
		return ""
	}
	return profileArn
}

func cachedKiroProfileArnWithLock(flightKey string, account *Account) string {
	lockKiroProfileResolution(flightKey)
	defer unlockKiroProfileResolution(flightKey)
	return cachedKiroProfileArn(account)
}

func cachedKiroProfileArn(account *Account) string {
	if account == nil {
		return ""
	}
	profileArn := strings.TrimSpace(account.GetCredential("profile_arn"))
	if profileArn == "" || kiroIsPlaceholderProfileARN(profileArn) {
		return ""
	}
	return profileArn
}

func kiroProfileResolutionFlightKey(account *Account) string {
	if account == nil {
		return "account:nil"
	}
	if account.ID > 0 {
		return fmt.Sprintf("account:%d", account.ID)
	}
	return fmt.Sprintf("ptr:%p", account)
}

func lockKiroProfileResolution(key string) {
	if key == "" {
		key = "account:unknown"
	}
	value, _ := kiroProfileResolutionLocks.LoadOrStore(key, &sync.Mutex{})
	value.(*sync.Mutex).Lock()
}

func unlockKiroProfileResolution(key string) {
	if key == "" {
		key = "account:unknown"
	}
	value, ok := kiroProfileResolutionLocks.Load(key)
	if !ok {
		return
	}
	value.(*sync.Mutex).Unlock()
}

func (s *GatewayService) ensureKiroProfileArnForRequest(ctx context.Context, account *Account, token string, mode string) {
	if s == nil || account == nil || account.Platform != PlatformKiro || account.Type != AccountTypeOAuth {
		return
	}
	if mode != KiroEndpointModeKRS && mode != KiroEndpointModeAuto {
		return
	}
	_ = s.resolveAndPersistKiroProfileArnOnly(ctx, account, token)
}

func (s *GatewayService) resolveKiroProfileArnFromAvailableProfiles(ctx context.Context, account *Account, accessToken string) (string, error) {
	if s == nil || s.httpUpstream == nil || account == nil {
		return "", fmt.Errorf("kiro profile resolver unavailable")
	}
	token := strings.TrimSpace(accessToken)
	if token == "" {
		token = strings.TrimSpace(account.GetCredential("access_token"))
	}
	if token == "" {
		return "", fmt.Errorf("kiro access token missing")
	}

	var lastErr error
	for _, region := range kiroAPIRegionCandidates(account) {
		for attempt := 1; attempt <= kiroProfileResolveMaxAttempts; attempt++ {
			profileArn, err := s.listKiroAvailableProfileArn(ctx, account, token, region, attempt)
			if err == nil {
				return profileArn, nil
			}
			lastErr = err
			if !isTransientKiroProfileFetchError(err) || attempt == kiroProfileResolveMaxAttempts {
				break
			}
			if sleepErr := sleepKiroRetry(ctx, attempt-1); sleepErr != nil {
				return "", sleepErr
			}
		}
	}
	return "", lastErr
}

func (s *GatewayService) listKiroAvailableProfileArn(ctx context.Context, account *Account, accessToken, region string, attempt int) (string, error) {
	endpointURL := fmt.Sprintf("https://q.%s.amazonaws.com/", strings.TrimSpace(region))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader([]byte(`{"maxResults":10}`)))
	if err != nil {
		return "", err
	}
	applyKiroListAvailableProfilesHeaders(req, account, accessToken, attempt, kiroProfileResolveMaxAttempts)

	resp, err := s.httpUpstream.DoWithTLS(req, kiroProxyURL(account), account.ID, account.Concurrency, s.resolveKiroTLSProfile(account))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, kiroProfileResolveBodyByteSize))
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode != http.StatusOK {
		return "", &kiroProfileFetchError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var result struct {
		Profiles []struct {
			Arn string `json:"arn"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	for _, profile := range result.Profiles {
		if profileArn := strings.TrimSpace(profile.Arn); profileArn != "" {
			return profileArn, nil
		}
	}
	return "", errKiroProfileListEmpty
}

func (s *GatewayService) persistKiroResolvedProfileArn(ctx context.Context, account *Account, profileArn string) error {
	if account == nil || strings.TrimSpace(profileArn) == "" {
		return nil
	}
	credentials := MergeCredentials(account.Credentials, map[string]any{
		"profile_arn": strings.TrimSpace(profileArn),
	})
	credentials["_token_version"] = time.Now().UnixMilli()
	account.Credentials = cloneCredentials(credentials)
	if err := persistAccountCredentials(ctx, s.accountRepo, account, credentials); err != nil {
		return err
	}
	logger.L().Info("kiro profile arn resolved",
		zap.Int64("account_id", account.ID),
		zap.Bool("persisted", s.accountRepo != nil),
	)
	return nil
}

func (s *GatewayService) persistKiroResolvedProfileArnWithLock(ctx context.Context, flightKey string, account *Account, profileArn string) error {
	lockKiroProfileResolution(flightKey)
	defer unlockKiroProfileResolution(flightKey)
	return s.persistKiroResolvedProfileArn(ctx, account, profileArn)
}

var errKiroProfileListEmpty = fmt.Errorf("empty profile list")

type kiroProfileFetchError struct {
	StatusCode int
	Body       string
}

func (e *kiroProfileFetchError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

func isTransientKiroProfileFetchError(err error) bool {
	if err == nil || err == errKiroProfileListEmpty {
		return false
	}
	var fetchErr *kiroProfileFetchError
	if !errors.As(err, &fetchErr) {
		return true
	}
	return fetchErr.StatusCode == http.StatusTooManyRequests || (fetchErr.StatusCode >= 500 && fetchErr.StatusCode < 600)
}

func (s *GatewayService) resolveKiroTLSProfile(account *Account) *tlsfingerprint.Profile {
	if s == nil || s.tlsFPProfileService == nil {
		return nil
	}
	return s.tlsFPProfileService.ResolveTLSProfile(account)
}

func applyKiroRestHeaders(req *http.Request, account *Account, token string) {
	if req == nil {
		return
	}
	accountKey := buildKiroAccountKey(account)
	machineID := buildKiroMachineID(account)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("User-Agent", kiropkg.BuildRestUserAgent(accountKey, machineID))
	req.Header.Set("X-Amz-User-Agent", kiropkg.BuildRestAmzUserAgent(accountKey, machineID))
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	if req.URL != nil && req.URL.Host != "" {
		req.Host = req.URL.Host
	}
	applyKiroConditionalHeaders(req, account)
}

func applyKiroListAvailableProfilesHeaders(req *http.Request, account *Account, token string, attempt, maxAttempts int) {
	if req == nil {
		return
	}
	accountKey := buildKiroAccountKey(account)
	machineID := buildKiroMachineID(account)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", kiroListAvailableProfilesTarget)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("User-Agent", kiropkg.BuildRuntimeUserAgent(accountKey, machineID))
	req.Header.Set("X-Amz-User-Agent", kiropkg.BuildRuntimeAmzUserAgent(accountKey, machineID))
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	if attempt <= 0 {
		attempt = 1
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	req.Header.Set("Amz-Sdk-Request", fmt.Sprintf("attempt=%d; max=%d", attempt, maxAttempts))
	if req.URL != nil && req.URL.Host != "" {
		req.Host = req.URL.Host
	}
	applyKiroConditionalHeaders(req, account)
}
