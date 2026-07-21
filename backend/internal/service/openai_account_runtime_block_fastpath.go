package service

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	openAIAccountStateUpdateTimeout       = 5 * time.Second
	openAIOAuth429FallbackCooldown        = 5 * time.Second
	openAIStopSchedulingBridgeCooldown    = 2 * time.Minute
	openAIOAuth429StormWindow             = 10 * time.Second
	openAIOAuth429StormThreshold          = 20
	openAIOAuth429StormMaxAccountSwitches = 1
	// Grok quota responses are normally immediate. Give the request a bounded
	// discovery window to try every still-unknown credential, while preventing a
	// pathological upstream from consuming the whole client request timeout.
	grokQuotaFailoverProbeBudget = 15 * time.Second
)

type grokQuotaFailoverDeadlineContextKey struct{}

type grokQuotaFailoverBudgetState struct {
	mu       sync.Mutex
	deadline time.Time
}

func (s *grokQuotaFailoverBudgetState) exceeded(now time.Time) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deadline.IsZero() {
		// Start the budget at the first confirmed quota failure, not at request
		// admission. A slow first upstream response must not consume all account
		// discovery time before the scheduler knows failover is needed.
		s.deadline = now.Add(grokQuotaFailoverProbeBudget)
		return false
	}
	return !now.Before(s.deadline)
}

// WithGrokQuotaFailoverBudget attaches a request-local quota discovery budget.
// It does not cancel the request context; successful generation may continue
// past the discovery window after a healthy account has been selected.
func WithGrokQuotaFailoverBudget(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(grokQuotaFailoverDeadlineContextKey{}).(*grokQuotaFailoverBudgetState); ok {
		return ctx
	}
	return context.WithValue(ctx, grokQuotaFailoverDeadlineContextKey{}, &grokQuotaFailoverBudgetState{})
}

func grokQuotaFailoverBudgetExceeded(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	state, ok := ctx.Value(grokQuotaFailoverDeadlineContextKey{}).(*grokQuotaFailoverBudgetState)
	return ok && state.exceeded(time.Now())
}

type openAIAccountRuntimeBlock struct {
	Until     time.Time
	StartedAt time.Time
	Reason    string
}

const maxOpenAIOAuthModelUnsupportedEntries = 128

type openAIOAuthModelUnsupportedCache struct {
	mu                  sync.Mutex
	untilByAccountModel map[string]time.Time
}

func (c *openAIOAuthModelUnsupportedCache) Mark(entryKey string, until time.Time) {
	if c == nil || entryKey == "" || !until.After(time.Now()) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.untilByAccountModel == nil {
		c.untilByAccountModel = make(map[string]time.Time)
	}
	now := time.Now()
	for key, currentUntil := range c.untilByAccountModel {
		if !now.Before(currentUntil) {
			delete(c.untilByAccountModel, key)
		}
	}
	if currentUntil, exists := c.untilByAccountModel[entryKey]; exists {
		if until.After(currentUntil) {
			c.untilByAccountModel[entryKey] = until
		}
		return
	}
	if len(c.untilByAccountModel) >= maxOpenAIOAuthModelUnsupportedEntries {
		var oldestKey string
		var oldestUntil time.Time
		for key, currentUntil := range c.untilByAccountModel {
			if oldestKey == "" || currentUntil.Before(oldestUntil) {
				oldestKey = key
				oldestUntil = currentUntil
			}
		}
		delete(c.untilByAccountModel, oldestKey)
	}
	c.untilByAccountModel[entryKey] = until
}

func (c *openAIOAuthModelUnsupportedCache) IsActive(entryKey string) bool {
	if c == nil || entryKey == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	until, exists := c.untilByAccountModel[entryKey]
	if !exists {
		return false
	}
	if !time.Now().Before(until) {
		delete(c.untilByAccountModel, entryKey)
		return false
	}
	return true
}

func openAIAccountStateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, openAIAccountStateUpdateTimeout)
}

func isOpenAIOAuthAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeOAuth
}

func isGrokOAuthAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformGrok && account.Type == AccountTypeOAuth
}

func isOpenAIAccount(account *Account) bool {
	return account != nil && (account.Platform == PlatformOpenAI || account.Platform == PlatformGrok)
}

func (s *OpenAIGatewayService) markOpenAIOAuthModelUnsupported(account *Account, upstreamModel string, responseBody []byte) {
	if s == nil || !isOpenAIOAuthAccount(account) || !isUpstreamChatGPTCodexModelUnsupportedError(http.StatusBadRequest, responseBody) {
		return
	}
	entryKey := openAIOAuthModelUnsupportedKey(account.ID, upstreamModel)
	if entryKey == "" {
		return
	}
	s.openaiOAuthModelUnsupported.Mark(entryKey, time.Now().Add(upstreamModelNotFoundCooldown))
}

func (s *OpenAIGatewayService) isOpenAIOAuthModelUnsupportedForRequest(account *Account, requestedModel string, requireCompact bool) bool {
	return s.isOpenAIOAuthModelUnsupportedForRequestWithPassthrough(account, requestedModel, requireCompact, account != nil && account.IsOpenAIPassthroughEnabled())
}

func (s *OpenAIGatewayService) isOpenAIOAuthModelUnsupportedForSchedule(account *Account, req OpenAIAccountScheduleRequest) bool {
	usePassthroughModel := account != nil && account.IsOpenAIPassthroughEnabled()
	// Native WS passthrough relays the raw frame model rather than applying the
	// account model mapping used by normal Responses forwarding.
	if !usePassthroughModel && req.RequiredTransport == OpenAIUpstreamTransportResponsesWebsocketV2Ingress &&
		s.isOpenAIWSIngressPassthroughAccount(account) {
		usePassthroughModel = true
	}
	return s.isOpenAIOAuthModelUnsupportedForRequestWithPassthrough(account, req.RequestedModel, req.RequireCompact, usePassthroughModel)
}

func (s *OpenAIGatewayService) isOpenAIOAuthModelUnsupportedForRequestWithPassthrough(account *Account, requestedModel string, requireCompact bool, usePassthroughModel bool) bool {
	if s == nil || !isOpenAIOAuthAccount(account) {
		return false
	}
	entryKey := openAIOAuthModelUnsupportedKey(account.ID, openAIOAuthUpstreamModelForRequest(account, requestedModel, requireCompact, usePassthroughModel))
	if entryKey == "" {
		return false
	}
	return s.openaiOAuthModelUnsupported.IsActive(entryKey)
}

func openAIOAuthUpstreamModelForRequest(account *Account, requestedModel string, requireCompact bool, usePassthroughModel bool) string {
	if usePassthroughModel {
		upstreamModel := strings.TrimSpace(requestedModel)
		if requireCompact {
			return resolveOpenAICompactForwardModel(account, upstreamModel)
		}
		return upstreamModel
	}
	return resolveOpenAIAccountUpstreamModelForRequest(account, requestedModel, requireCompact)
}

func (s *OpenAIGatewayService) isOpenAIWSIngressPassthroughAccount(account *Account) bool {
	if s == nil || s.cfg == nil || account == nil || !s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled {
		return false
	}
	return account.ResolveOpenAIResponsesWebSocketV2Mode(s.cfg.Gateway.OpenAIWS.IngressModeDefault) == OpenAIWSIngressModePassthrough
}

func openAIOAuthModelUnsupportedKey(accountID int64, model string) string {
	modelKey := strings.ToLower(strings.TrimSpace(model))
	if accountID <= 0 || modelKey == "" {
		return ""
	}
	return strconv.FormatInt(accountID, 10) + ":" + modelKey
}

func (s *OpenAIGatewayService) handleOpenAIAccountUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte, requestedModel ...string) bool {
	if s == nil {
		return false
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	if len(requestedModel) > 0 {
		s.markOpenAIOAuthModelUnsupported(account, requestedModel[0], responseBody)
	}

	// Grok has provider-specific quota semantics. Keep all Grok account state
	// changes in the Grok handler so the generic 429 fallback cannot overwrite a
	// rolling 24-hour free-usage cooldown with a short retry window.
	if account != nil && account.Platform == PlatformGrok {
		s.handleGrokAccountUpstreamError(stateCtx, account, statusCode, headers, responseBody)
		if s.rateLimitService != nil && len(requestedModel) > 0 && s.rateLimitService.HandleUpstreamModelNotFound(stateCtx, account, requestedModel[0], statusCode, responseBody) {
			return true
		}
		return false
	}

	if account != nil && account.Platform == PlatformOpenAI && isOpenAIContextWindowError("", responseBody) {
		return false
	}

	if isOpenAIImageRateLimitError(statusCode, responseBody) {
		if s.rateLimitService != nil {
			_ = s.rateLimitService.HandleOpenAIImageRateLimit(stateCtx, account, statusCode, headers, responseBody)
		}
		return false
	}

	if statusCode == http.StatusTooManyRequests {
		s.markOpenAIOAuth429RateLimited(stateCtx, account, headers, responseBody)
	}
	if s == nil || account == nil || s.rateLimitService == nil {
		return false
	}
	if len(requestedModel) > 0 && s.rateLimitService.HandleUpstreamModelNotFound(stateCtx, account, requestedModel[0], statusCode, responseBody) {
		return true
	}
	shouldDisable := s.rateLimitService.HandleUpstreamError(stateCtx, account, statusCode, headers, responseBody)
	if shouldDisable {
		s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
	}
	return shouldDisable
}

// shouldRetryOpenAIAccountOnSameAccount centralizes pool-mode retry policy for
// OpenAI-compatible providers. Deterministic model-capability and Grok quota
// failures must not be repeated even when the pool-mode status list includes
// their HTTP status.
func shouldRetryOpenAIAccountOnSameAccount(account *Account, statusCode int, responseBody []byte, transient bool) bool {
	if account == nil || !account.IsPoolMode() {
		return false
	}
	// Model capability errors and provider quota errors are deterministic for
	// the selected credential. Retrying the same credential only adds latency;
	// the handler will either apply a model-level cooldown or rotate accounts.
	if isUpstreamModelUnavailableError(statusCode, responseBody) || isGrokQuotaExhaustedForStatus(account, statusCode, responseBody) {
		return false
	}
	return account.IsPoolModeRetryableStatus(statusCode) || transient
}

func isGrokQuotaExhaustedForStatus(account *Account, statusCode int, responseBody []byte) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusPaymentRequired && statusCode != http.StatusForbidden && statusCode != http.StatusTooManyRequests {
		return false
	}
	if account == nil || account.Platform != PlatformGrok {
		return false
	}
	// A zero model RPM is credential-scoped and deterministic. It is not global
	// credit exhaustion, but it needs the same bounded multi-account discovery.
	if statusCode == http.StatusTooManyRequests && isGrokZeroRPMRateLimitResponse(responseBody) {
		return true
	}
	// A 400 is also used for model/schema errors. Only an explicit quota code or
	// message may classify it as spending-limit exhaustion; a billing snapshot
	// alone is insufficient for that status.
	if statusCode == http.StatusBadRequest {
		return IsGrokQuotaExhaustedResponse(responseBody)
	}
	return isGrokQuotaExhausted(account, responseBody)
}

// IsGrokQuotaExhaustedForFailover exposes the deterministic quota classifier
// to handlers that need to bypass their generic max-switch cap. It deliberately
// remains body/status based; an unknown account is still probed normally.
func IsGrokQuotaExhaustedForFailover(account *Account, statusCode int, responseBody []byte) bool {
	return isGrokQuotaExhaustedForStatus(account, statusCode, responseBody)
}

func (s *OpenAIGatewayService) markOpenAIOAuth429RateLimited(ctx context.Context, account *Account, headers http.Header, responseBody []byte) {
	if s == nil || !isOpenAIOAuthAccount(account) {
		return
	}
	// Spark 影子：不按 /responses 429 的 global x-codex-* 信号做内存运行时熔断(同 handle429,外审第8轮 P1)。
	// 同时避免把 spark 的 429 计入全局 429 storm 计数(recordOpenAIOAuth429),否则会误伤母账号 failover 决策。
	if account.IsShadow() {
		return
	}
	s.recordOpenAIOAuth429()

	cooldownUntil := time.Now().Add(openAIOAuth429FallbackCooldown)
	if s.rateLimitService != nil {
		if resetAt := s.rateLimitService.calculateOpenAI429ResetTime(headers); resetAt != nil && resetAt.After(time.Now()) {
			cooldownUntil = *resetAt
		} else if resetUnix := parseOpenAIRateLimitResetTime(responseBody); resetUnix != nil {
			if resetAt := time.Unix(*resetUnix, 0); resetAt.After(time.Now()) {
				cooldownUntil = resetAt
			}
		} else if cooldown, ok := s.rateLimitService.get429FallbackCooldown(ctx, account); ok && cooldown > 0 {
			cooldownUntil = time.Now().Add(cooldown)
		}
	}
	s.BlockAccountScheduling(account, cooldownUntil, "429")
}

func (s *OpenAIGatewayService) BlockAccountScheduling(account *Account, until time.Time, reason string) {
	if s == nil || !isOpenAIAccount(account) {
		return
	}
	now := time.Now()
	blockUntil := until
	if blockUntil.IsZero() || !blockUntil.After(now) {
		blockUntil = now.Add(openAIStopSchedulingBridgeCooldown)
	}
	next := openAIAccountRuntimeBlock{
		Until:     blockUntil,
		StartedAt: now,
		Reason:    reason,
	}

	for {
		current, loaded := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
		if !loaded {
			_, alreadyLoaded := s.openaiAccountRuntimeBlockUntil.LoadOrStore(account.ID, next)
			if !alreadyLoaded {
				return
			}
			continue
		}

		currentBlock, ok := openAIAccountRuntimeBlockFromValue(current)
		if ok && currentBlock.Until.After(blockUntil) {
			return
		}
		if s.openaiAccountRuntimeBlockUntil.CompareAndSwap(account.ID, current, next) {
			return
		}
	}
}

func (s *OpenAIGatewayService) ClearAccountSchedulingBlock(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	s.openaiAccountRuntimeBlockUntil.Delete(accountID)
}

func (s *OpenAIGatewayService) isOpenAIAccountRuntimeBlocked(account *Account) bool {
	if s == nil || !isOpenAIAccount(account) {
		return false
	}
	value, ok := s.openaiAccountRuntimeBlockUntil.Load(account.ID)
	if !ok {
		return false
	}
	block, ok := openAIAccountRuntimeBlockFromValue(value)
	if !ok || block.Until.IsZero() {
		s.openaiAccountRuntimeBlockUntil.Delete(account.ID)
		return false
	}
	now := time.Now()
	if now.Before(block.Until) {
		if account.Platform == PlatformGrok && shouldReconcileStaleGrokRuntimeBlock(account, block, now) {
			if s.openaiAccountRuntimeBlockUntil.CompareAndDelete(account.ID, value) {
				slog.Warn("grok_runtime_block_reconciled",
					"account_id", account.ID,
					"runtime_block_until", block.Until,
					"reason", block.Reason,
				)
				return false
			}
		}
		return true
	}
	s.openaiAccountRuntimeBlockUntil.CompareAndDelete(account.ID, value)
	return false
}

func openAIAccountRuntimeBlockFromValue(value any) (openAIAccountRuntimeBlock, bool) {
	switch block := value.(type) {
	case openAIAccountRuntimeBlock:
		return block, true
	case time.Time:
		// Compatibility for tests or rolling upgrades that still hold the old value shape.
		return openAIAccountRuntimeBlock{Until: block}, true
	default:
		return openAIAccountRuntimeBlock{}, false
	}
}

func shouldReconcileStaleGrokRuntimeBlock(account *Account, block openAIAccountRuntimeBlock, now time.Time) bool {
	if block.StartedAt.IsZero() || now.Sub(block.StartedAt) < openAIStopSchedulingBridgeCooldown {
		return false
	}
	return !hasPersistedAccountSchedulingBlock(account, now)
}

func hasPersistedAccountSchedulingBlock(account *Account, now time.Time) bool {
	if account == nil || !account.IsActive() || !account.Schedulable {
		return true
	}
	if account.AutoPauseOnExpired && account.ExpiresAt != nil && !now.Before(*account.ExpiresAt) {
		return true
	}
	for _, until := range []*time.Time{
		account.RateLimitResetAt,
		account.OverloadUntil,
		account.TempUnschedulableUntil,
	} {
		if until != nil && now.Before(*until) {
			return true
		}
	}
	return false
}

func (s *OpenAIGatewayService) recordOpenAIOAuth429() {
	if s == nil {
		return
	}
	now := time.Now()
	windowStart := s.openaiOAuth429WindowStartUnixNano.Load()
	if windowStart == 0 || now.Sub(time.Unix(0, windowStart)) >= openAIOAuth429StormWindow {
		if s.openaiOAuth429WindowStartUnixNano.CompareAndSwap(windowStart, now.UnixNano()) {
			s.openaiOAuth429WindowCount.Store(1)
			return
		}
	}
	s.openaiOAuth429WindowCount.Add(1)
}

func (s *OpenAIGatewayService) isOpenAIOAuth429Storm() bool {
	if s == nil {
		return false
	}
	windowStart := s.openaiOAuth429WindowStartUnixNano.Load()
	if windowStart == 0 || time.Since(time.Unix(0, windowStart)) >= openAIOAuth429StormWindow {
		return false
	}
	return s.openaiOAuth429WindowCount.Load() >= openAIOAuth429StormThreshold
}

func (s *OpenAIGatewayService) ShouldStopOpenAIOAuth429Failover(account *Account, statusCode int, failedSwitches int) bool {
	if statusCode != http.StatusTooManyRequests || failedSwitches < openAIOAuth429StormMaxAccountSwitches {
		return false
	}
	if isGrokOAuthAccount(account) {
		return true
	}
	if !isOpenAIOAuthAccount(account) {
		return false
	}
	return s.isOpenAIOAuth429Storm()
}

// ShouldStopOpenAIFailover is the compatibility entry point for callers that
// do not carry the request-local Grok discovery budget.
func (s *OpenAIGatewayService) ShouldStopOpenAIFailover(account *Account, statusCode int, responseBody []byte, failedSwitches int) bool {
	return s.ShouldStopOpenAIFailoverWithContext(context.Background(), account, statusCode, responseBody, failedSwitches)
}

// ShouldStopOpenAIFailoverWithContext applies the provider-specific failover
// policy. Grok quota failures use the request discovery budget instead of a
// fixed account count, so an available third/fourth account is actually tried.
// The context-less wrapper retains the historical two-account safety fallback
// for callers outside the HTTP request loop.
func (s *OpenAIGatewayService) ShouldStopOpenAIFailoverWithContext(ctx context.Context, account *Account, statusCode int, responseBody []byte, failedSwitches int) bool {
	if isGrokQuotaExhaustedForStatus(account, statusCode, responseBody) {
		if ctx != nil {
			if _, configured := ctx.Value(grokQuotaFailoverDeadlineContextKey{}).(*grokQuotaFailoverBudgetState); configured {
				return grokQuotaFailoverBudgetExceeded(ctx)
			}
		}
		return failedSwitches >= 2
	}
	return s.ShouldStopOpenAIOAuth429Failover(account, statusCode, failedSwitches)
}
