// Source-faithful, namespaced integration of kiro_usage_fetcher.go from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/google/uuid"
)

const (
	nianzsKiroUsageOrigin       = "AI_EDITOR"
	nianzsKiroUsageResourceType = "AGENTIC_REQUEST"

	nianzsKiroDefaultRegion = "us-east-1"
)

var nianzsResolveKiroRuntimeEndpoint = nianzsKiroRuntimeEndpoint

type nianzsKiroUsageLimitsResponse struct {
	NextDateReset        any                            `json:"nextDateReset"`
	OverageConfiguration nianzsKiroOverageConfiguration `json:"overageConfiguration"`
	SubscriptionInfo     nianzsKiroSubscriptionInfo     `json:"subscriptionInfo"`
	UsageBreakdownList   []nianzsKiroUsageBreakdown     `json:"usageBreakdownList"`
}

type nianzsKiroOverageConfiguration struct {
	OverageStatus string `json:"overageStatus"`
}

type nianzsKiroSubscriptionInfo struct {
	SubscriptionTitle string `json:"subscriptionTitle"`
	Type              string `json:"type"`
}

type nianzsKiroUsageBreakdown struct {
	Currency                     string                   `json:"currency"`
	CurrentOverages              *float64                 `json:"currentOverages"`
	CurrentOveragesWithPrecision *float64                 `json:"currentOveragesWithPrecision"`
	CurrentUsage                 *float64                 `json:"currentUsage"`
	CurrentUsageWithPrecision    *float64                 `json:"currentUsageWithPrecision"`
	DisplayName                  string                   `json:"displayName"`
	DisplayNamePlural            string                   `json:"displayNamePlural"`
	FreeTrialInfo                *nianzsKiroFreeTrialInfo `json:"freeTrialInfo"`
	NextDateReset                any                      `json:"nextDateReset"`
	OverageCharges               *float64                 `json:"overageCharges"`
	ResourceType                 string                   `json:"resourceType"`
	UsageLimit                   *float64                 `json:"usageLimit"`
	UsageLimitWithPrecision      *float64                 `json:"usageLimitWithPrecision"`
}

type nianzsKiroFreeTrialInfo struct {
	CurrentUsage              *float64 `json:"currentUsage"`
	CurrentUsageWithPrecision *float64 `json:"currentUsageWithPrecision"`
	FreeTrialExpiry           any      `json:"freeTrialExpiry"`
	FreeTrialStatus           string   `json:"freeTrialStatus"`
	UsageLimit                *float64 `json:"usageLimit"`
	UsageLimitWithPrecision   *float64 `json:"usageLimitWithPrecision"`
}

type nianzsKiroUsageHTTPError struct {
	StatusCode int
	Body       string
}

func (e *nianzsKiroUsageHTTPError) Error() string {
	if e == nil {
		return "kiro usage request failed"
	}
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("kiro usage request failed (status %d)", e.StatusCode)
	}
	return fmt.Sprintf("kiro usage request failed (status %d): %s", e.StatusCode, e.Body)
}

func (s *AccountUsageService) getKiroUsageNianzs(ctx context.Context, account *Account, source string, forceRefresh bool) (*UsageInfo, error) {
	now := time.Now()
	if account == nil {
		return &UsageInfo{
			Source:    source,
			UpdatedAt: &now,
			Error:     "account is nil",
			ErrorCode: errorCodeNetworkError,
		}, nil
	}
	if !nianzsIsKiroDirectModeAccount(account) {
		return &UsageInfo{
			Source:    source,
			UpdatedAt: &now,
		}, nil
	}

	cached, hasCached := s.getCachedKiroUsageNianzs(account.ID)
	if hasCached && (cached.ErrorCode != "" || cached.Error != "") {
		cached.Source = source
		s.attachKiroRuntimeStateNianzs(ctx, account, cached)
		return cached, nil
	}
	if !forceRefresh && hasCached {
		cached.Source = source
		s.attachKiroRuntimeStateNianzs(ctx, account, cached)
		return cached, nil
	}

	flightKey := fmt.Sprintf("kiro-usage:%d", account.ID)
	result, fetchErr, _ := s.cache.kiroUsageFlight.Do(flightKey, func() (any, error) {
		if !forceRefresh {
			if usage, ok := s.getCachedKiroUsageNianzs(account.ID); ok {
				return usage, nil
			}
		}
		usage, err := s.fetchAndCacheKiroUsageNianzs(ctx, account, source)
		if err != nil {
			return nil, err
		}
		return usage, nil
	})
	if fetchErr == nil {
		if usage, ok := result.(*UsageInfo); ok && usage != nil {
			usage.Source = source
			s.attachKiroRuntimeStateNianzs(ctx, account, usage)
			if source == "active" {
				s.tryClearRecoverableAccountError(ctx, account)
			}
			return usage, nil
		}
	}

	degraded := nianzsBuildKiroDegradedUsage(fetchErr)
	degraded.Source = source
	if hasCached {
		cached.Error = degraded.Error
		cached.ErrorCode = degraded.ErrorCode
		cached.NeedsReauth = degraded.NeedsReauth
		cached.KiroQuotaState = degraded.KiroQuotaState
		cached.KiroQuotaReason = degraded.KiroQuotaReason
		cached.KiroQuotaResetAt = degraded.KiroQuotaResetAt
		cached.Source = source
		s.attachKiroRuntimeStateNianzs(ctx, account, cached)
		return cached, nil
	}
	s.storeKiroUsageSnapshotNianzs(account.ID, degraded)
	s.attachKiroRuntimeStateNianzs(ctx, account, degraded)
	return degraded, nil
}

func (s *AccountUsageService) fetchAndCacheKiroUsageNianzs(ctx context.Context, account *Account, source string) (*UsageInfo, error) {
	token, err := s.getKiroUsageAccessTokenNianzs(ctx, account)
	if err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("no access token available")
	}

	region := nianzsKiroAPIRegion(account)
	profileArn := nianzsResolveKiroPayloadProfileArn(account)

	resp, err := s.requestKiroUsageLimitsNianzs(ctx, account, region, profileArn, token)
	if err != nil {
		// API Key 账号无可刷新 token,跳过刷新重试。
		if account.Type != AccountTypeAPIKey && s.shouldRetryKiroUsageWithRefreshNianzs(err) {
			refreshedToken, refreshErr := s.nianzsKiroTokenProvider.ForceRefreshAccessToken(ctx, account)
			if refreshErr == nil && strings.TrimSpace(refreshedToken) != "" {
				resp, err = s.requestKiroUsageLimitsNianzs(ctx, account, region, profileArn, strings.TrimSpace(refreshedToken))
				if err == nil {
					usage := nianzsMapKiroUsageToInfo(resp)
					usage.Source = source
					s.storeKiroUsageSnapshotNianzs(account.ID, usage)
					return usage, nil
				}
				return nil, err
			}
		}
		return nil, err
	}

	usage := nianzsMapKiroUsageToInfo(resp)
	usage.Source = source
	s.storeKiroUsageSnapshotNianzs(account.ID, usage)
	return usage, nil
}

func (s *AccountUsageService) getKiroUsageAccessTokenNianzs(ctx context.Context, account *Account) (string, error) {
	// API Key 账号:api_key 即长期 Bearer Token,不经过刷新 provider。
	if account != nil && account.Type == AccountTypeAPIKey {
		return nianzsFirstKiroCredential(account, "kiro_api_key", "kiroApiKey", "api_key"), nil
	}
	if s != nil && s.nianzsKiroTokenProvider != nil {
		return s.nianzsKiroTokenProvider.GetAccessToken(ctx, account)
	}
	return strings.TrimSpace(account.GetCredential("access_token")), nil
}

func (s *AccountUsageService) shouldRetryKiroUsageWithRefreshNianzs(err error) bool {
	if s == nil || s.nianzsKiroTokenProvider == nil || err == nil {
		return false
	}
	return nianzsClassifyKiroError(err).Category == nianzsKiroErrorAuthError
}

func (s *AccountUsageService) storeKiroUsageSnapshotNianzs(accountID int64, usage *UsageInfo) {
	if s == nil || s.cache == nil || accountID <= 0 || usage == nil {
		return
	}
	now := time.Now()
	if usage.UpdatedAt == nil {
		usage.UpdatedAt = &now
	}
	s.cache.kiroUsageCache.Store(accountID, &kiroUsageCache{
		usageInfo: nianzsCloneUsageInfo(usage),
		timestamp: now,
	})
}

func (s *AccountUsageService) getCachedKiroUsageNianzs(accountID int64) (*UsageInfo, bool) {
	if s == nil || s.cache == nil || accountID <= 0 {
		return nil, false
	}
	cached, ok := s.cache.kiroUsageCache.Load(accountID)
	if !ok {
		return nil, false
	}
	cache, ok := cached.(*kiroUsageCache)
	if !ok || cache == nil || cache.usageInfo == nil {
		return nil, false
	}
	if time.Since(cache.timestamp) >= nianzsKiroCacheTTL(cache.usageInfo) {
		return nil, false
	}
	return nianzsCloneUsageInfo(cache.usageInfo), true
}

func nianzsKiroCacheTTL(info *UsageInfo) time.Duration {
	if info == nil {
		return kiroUsageErrorTTL
	}
	if info.ErrorCode != "" || info.Error != "" {
		return kiroUsageErrorTTL
	}
	return apiCacheTTL
}

func nianzsCloneUsageInfo(info *UsageInfo) *UsageInfo {
	if info == nil {
		return nil
	}
	cloned := *info
	return &cloned
}

func (s *AccountUsageService) requestKiroUsageLimitsNianzs(ctx context.Context, account *Account, region, profileArn, token string) (*nianzsKiroUsageLimitsResponse, error) {
	endpoint := nianzsResolveKiroRuntimeEndpoint(region)
	reqURL, err := url.Parse(endpoint + "/getUsageLimits")
	if err != nil {
		return nil, fmt.Errorf("build kiro usage url failed: %w", err)
	}
	q := reqURL.Query()
	q.Set("origin", nianzsKiroUsageOrigin)
	if profileArn = strings.TrimSpace(profileArn); profileArn != "" {
		q.Set("profileArn", profileArn)
	}
	q.Set("resourceType", nianzsKiroUsageResourceType)
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create kiro usage request failed: %w", err)
	}
	s.applyKiroRuntimeHeadersNianzs(req, account, token)

	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:           nianzsAccountProxyURL(account),
		Timeout:            30 * time.Second,
		ValidateResolvedIP: true,
		AllowPrivateHosts:  nianzsIsLoopbackEndpoint(endpoint),
	})
	if err != nil {
		return nil, fmt.Errorf("create kiro usage client failed: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro usage request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read kiro usage response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &nianzsKiroUsageHTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	var parsed nianzsKiroUsageLimitsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode kiro usage response failed: %w", err)
	}
	return &parsed, nil
}

func (s *AccountUsageService) applyKiroRuntimeHeadersNianzs(req *http.Request, account *Account, token string) {
	if req == nil {
		return
	}
	accountKey := nianzsBuildKiroAccountKey(account)
	machineID := nianzsBuildKiroMachineID(account)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("User-Agent", nianzskiro.BuildRuntimeUserAgent(accountKey, machineID))
	req.Header.Set("X-Amz-User-Agent", nianzskiro.BuildRuntimeAmzUserAgent(accountKey, machineID))
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
	req.Header.Set("Amz-Sdk-Invocation-Id", uuid.NewString())

	if account == nil {
		return
	}
	nianzsApplyKiroConditionalHeaders(req, account)
}

func nianzsAccountProxyURL(account *Account) string {
	if account == nil || account.ProxyID == nil || account.Proxy == nil {
		return ""
	}
	return account.Proxy.URL()
}

func nianzsKiroRuntimeEndpoint(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		region = nianzsKiroDefaultRegion
	}
	switch region {
	case "us-east-1":
		return "https://q.us-east-1.amazonaws.com"
	case "eu-central-1":
		return "https://q.eu-central-1.amazonaws.com"
	case "us-gov-east-1":
		return "https://q-fips.us-gov-east-1.amazonaws.com"
	case "us-gov-west-1":
		return "https://q-fips.us-gov-west-1.amazonaws.com"
	case "us-iso-east-1":
		return "https://q.us-iso-east-1.c2s.ic.gov"
	case "us-isob-east-1":
		return "https://q.us-isob-east-1.sc2s.sgov.gov"
	case "us-isof-south-1":
		return "https://q.us-isof-south-1.csp.hci.ic.gov"
	case "us-isof-east-1":
		return "https://q.us-isof-east-1.csp.hci.ic.gov"
	default:
		if strings.HasPrefix(region, "us-gov-") {
			return "https://q-fips." + region + ".amazonaws.com"
		}
		return "https://q." + region + ".amazonaws.com"
	}
}

func nianzsIsLoopbackEndpoint(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func nianzsMapKiroUsageToInfo(resp *nianzsKiroUsageLimitsResponse) *UsageInfo {
	now := time.Now()
	if resp == nil {
		return &UsageInfo{UpdatedAt: &now}
	}
	info := &UsageInfo{
		UpdatedAt:            &now,
		KiroSubscriptionName: strings.TrimSpace(resp.SubscriptionInfo.SubscriptionTitle),
		KiroSubscriptionType: strings.TrimSpace(resp.SubscriptionInfo.Type),
		KiroOveragesEnabled:  strings.EqualFold(strings.TrimSpace(resp.OverageConfiguration.OverageStatus), "ENABLED"),
	}

	resetAt := nianzsParseKiroTimestamp(resp.NextDateReset)
	if credit := nianzsSelectKiroCreditBreakdown(resp.UsageBreakdownList); credit != nil {
		if breakdownReset := nianzsParseKiroTimestamp(credit.NextDateReset); breakdownReset != nil {
			resetAt = breakdownReset
		}
		info.KiroCredit = &KiroCreditProgress{
			CurrentUsage:   nianzsSelectKiroFloat(credit.CurrentUsageWithPrecision, credit.CurrentUsage),
			UsageLimit:     nianzsSelectKiroFloat(credit.UsageLimitWithPrecision, credit.UsageLimit),
			PercentageUsed: nianzsPercentageOrZero(nianzsSelectKiroFloat(credit.CurrentUsageWithPrecision, credit.CurrentUsage), nianzsSelectKiroFloat(credit.UsageLimitWithPrecision, credit.UsageLimit)),
		}
		info.KiroOverage = &KiroOverageInfo{
			CurrentOverages: nianzsSelectKiroFloat(credit.CurrentOveragesWithPrecision, credit.CurrentOverages),
			OverageCharges:  nianzsSelectKiroFloat(credit.OverageCharges, nil),
			CurrencyCode:    strings.TrimSpace(credit.Currency),
			CurrencySymbol:  nianzsKiroCurrencySymbol(strings.TrimSpace(credit.Currency)),
		}
		if ft := credit.FreeTrialInfo; ft != nil && strings.EqualFold(strings.TrimSpace(ft.FreeTrialStatus), "ACTIVE") {
			expiry := nianzsParseKiroTimestamp(ft.FreeTrialExpiry)
			daysRemaining := 0
			if expiry != nil {
				daysRemaining = int(time.Until(*expiry).Hours() / 24)
				if time.Until(*expiry)%(24*time.Hour) != 0 {
					daysRemaining++
				}
				if daysRemaining < 0 {
					daysRemaining = 0
				}
			}
			current := nianzsSelectKiroFloat(ft.CurrentUsageWithPrecision, ft.CurrentUsage)
			limit := nianzsSelectKiroFloat(ft.UsageLimitWithPrecision, ft.UsageLimit)
			info.KiroBonus = &KiroCreditProgress{
				CurrentUsage:   current,
				UsageLimit:     limit,
				PercentageUsed: nianzsPercentageOrZero(current, limit),
				DaysRemaining:  daysRemaining,
				ExpiryDate:     expiry,
			}
		}
	}
	info.KiroResetAt = resetAt
	nianzsSetKiroQuotaStateFromUsage(info)
	return info
}

func nianzsSelectKiroCreditBreakdown(items []nianzsKiroUsageBreakdown) *nianzsKiroUsageBreakdown {
	for i := range items {
		if strings.EqualFold(strings.TrimSpace(items[i].ResourceType), "CREDIT") {
			return &items[i]
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &items[0]
}

func nianzsSelectKiroFloat(preferred *float64, fallback *float64) float64 {
	switch {
	case preferred != nil:
		return *preferred
	case fallback != nil:
		return *fallback
	default:
		return 0
	}
}

func nianzsPercentageOrZero(current, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return current / limit * 100
}

func nianzsParseKiroTimestamp(raw any) *time.Time {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return &parsed
		}
		if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return nianzsUnixishToTime(i)
		}
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return nianzsUnixishFloatToTime(f)
		}
	case float64:
		return nianzsUnixishFloatToTime(v)
	case int64:
		return nianzsUnixishToTime(v)
	case int:
		return nianzsUnixishToTime(int64(v))
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return nianzsUnixishToTime(i)
		}
		if f, err := v.Float64(); err == nil {
			return nianzsUnixishFloatToTime(f)
		}
	}
	return nil
}

func nianzsUnixishFloatToTime(v float64) *time.Time {
	if v <= 0 {
		return nil
	}
	if v >= 1e12 {
		t := time.UnixMilli(int64(v))
		return &t
	}
	t := time.Unix(int64(v), 0)
	return &t
}

func nianzsUnixishToTime(v int64) *time.Time {
	if v <= 0 {
		return nil
	}
	if v >= 1e12 {
		t := time.UnixMilli(v)
		return &t
	}
	t := time.Unix(v, 0)
	return &t
}

func nianzsKiroCurrencySymbol(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "USD":
		return "$"
	default:
		return ""
	}
}

func nianzsBuildKiroDegradedUsage(err error) *UsageInfo {
	now := time.Now()
	info := &UsageInfo{
		UpdatedAt: &now,
		Error:     "usage API error",
		ErrorCode: errorCodeNetworkError,
	}
	if err == nil {
		return info
	}

	info.Error = fmt.Sprintf("usage API error: %v", err)

	classification := nianzsClassifyKiroError(err)
	switch classification.Category {
	case nianzsKiroErrorAuthError:
		info.ErrorCode = errorCodeUnauthenticated
		info.NeedsReauth = true
	case nianzsKiroErrorRateLimited:
		info.ErrorCode = errorCodeRateLimited
	case nianzsKiroErrorQuotaExhausted:
		info.ErrorCode = errorCodeNetworkError
		info.KiroQuotaState = nianzsKiroQuotaStateCreditsExhausted
		info.KiroQuotaReason = classification.Message
	case nianzsKiroErrorOverageExhausted:
		info.ErrorCode = errorCodeNetworkError
		info.KiroQuotaState = nianzsKiroQuotaStateOverageExhausted
		info.KiroQuotaReason = classification.Message
	case nianzsKiroErrorSuspended, nianzsKiroErrorUsageForbidden, nianzsKiroErrorProfileError:
		info.ErrorCode = errorCodeForbidden
	default:
		info.ErrorCode = errorCodeNetworkError
	}
	return info
}

func (s *AccountUsageService) attachKiroRuntimeStateNianzs(ctx context.Context, account *Account, usage *UsageInfo) {
	if s == nil || usage == nil || account == nil || account.Platform != PlatformKiro || s.nianzsKiroCooldownStore == nil {
		return
	}
	usage.KiroRuntimeState = ""
	usage.KiroRuntimeReason = ""
	usage.KiroRuntimeResetAt = nil
	state, err := s.nianzsKiroCooldownStore.GetState(ctx, nianzsBuildKiroAccountKey(account))
	if err != nil || state == nil {
		return
	}
	usage.KiroRuntimeState, usage.KiroRuntimeReason, usage.KiroRuntimeResetAt = nianzsKiroRuntimeStateSnapshot(state)
}

func (s *AccountUsageService) EnrichAccountWithKiroRuntimeStateNianzs(ctx context.Context, account *Account) {
	if s == nil || !nianzsIsKiroDirectModeAccount(account) {
		return
	}
	account.KiroQuotaState = ""
	account.KiroQuotaReason = ""
	account.KiroQuotaResetAt = nil
	account.KiroRuntimeState = ""
	account.KiroRuntimeReason = ""
	account.KiroRuntimeResetAt = nil
	if usage, ok := s.getCachedKiroUsageNianzs(account.ID); ok {
		account.KiroQuotaState = usage.KiroQuotaState
		account.KiroQuotaReason = usage.KiroQuotaReason
		account.KiroQuotaResetAt = usage.KiroQuotaResetAt
	}
	if s.nianzsKiroCooldownStore == nil {
		return
	}
	state, err := s.nianzsKiroCooldownStore.GetState(ctx, nianzsBuildKiroAccountKey(account))
	if err != nil || state == nil {
		return
	}
	account.KiroRuntimeState, account.KiroRuntimeReason, account.KiroRuntimeResetAt = nianzsKiroRuntimeStateSnapshot(state)
}

func nianzsSetKiroQuotaStateFromUsage(info *UsageInfo) {
	if info == nil {
		return
	}
	info.KiroQuotaState = ""
	info.KiroQuotaReason = ""
	info.KiroQuotaResetAt = nil

	creditExhausted := false
	if info.KiroCredit != nil && info.KiroCredit.UsageLimit > 0 {
		creditExhausted = info.KiroCredit.CurrentUsage >= info.KiroCredit.UsageLimit
	}
	overageActive := info.KiroOverage != nil &&
		(info.KiroOverage.CurrentOverages > 0 || info.KiroOverage.OverageCharges > 0)

	switch {
	case info.KiroOveragesEnabled && (overageActive || creditExhausted):
		info.KiroQuotaState = nianzsKiroQuotaStateOverageActive
		info.KiroQuotaReason = "overages_enabled"
		info.KiroQuotaResetAt = info.KiroResetAt
	case creditExhausted:
		info.KiroQuotaState = nianzsKiroQuotaStateCreditsExhausted
		info.KiroQuotaReason = "credits_exhausted"
		info.KiroQuotaResetAt = info.KiroResetAt
	default:
		info.KiroQuotaState = nianzsKiroQuotaStateNormal
	}
}
