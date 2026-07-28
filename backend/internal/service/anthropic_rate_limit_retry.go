package service

import (
	"context"
	"net/http"
	"time"
)

const anthropicSoftRateLimitRetryDelay = 5 * time.Second

// AnthropicSoftRateLimitRetryDelay is the single-account retry delay for an
// Anthropic 429 that has no usable provider reset window. Keeping this policy
// in the service package prevents the protocol handlers from choosing
// different delays.
func AnthropicSoftRateLimitRetryDelay() time.Duration {
	return anthropicSoftRateLimitRetryDelay
}

// IsAnthropicSoftRateLimitResponse returns true only for the narrow, recoverable
// 429 class. A valid 5h/7d (or aggregate/7d_oi) reset is a hard provider window
// and must be persisted immediately instead of replayed on the same account.
func IsAnthropicSoftRateLimitResponse(account *Account, statusCode int, headers http.Header, body []byte) bool {
	if account == nil || account.Platform != PlatformAnthropic || statusCode != http.StatusTooManyRequests {
		return false
	}
	// Explicit per-account error-code policy remains authoritative. If the
	// account owner chose to ignore 429, do not bypass that choice through the
	// soft-retry path.
	if account.IsCustomErrorCodesEnabled() && !account.ShouldHandleErrorCode(statusCode) {
		return false
	}
	if !isAnthropicRateLimitErrorBody(body) {
		return false
	}

	now := time.Now()
	// Model-scoped Fable exhaustion must never be promoted to an account-wide
	// same-account retry, even when the provider omits its reset timestamp.
	if isAnthropicWindowRejected(headers, "7d_oi") || isAnthropicWindowExceeded(headers, "7d_oi") {
		return false
	}
	for _, window := range []string{"5h", "7d", "7d_oi"} {
		if _, ok := parseAnthropicWindowReset(headers, window, now); ok {
			return false
		}
	}
	if _, ok := parseAnthropicAggregateReset(headers, now); ok {
		return false
	}
	return true
}

// IsAnthropicSoftRateLimitFailover identifies an error previously classified by
// IsAnthropicSoftRateLimitResponse. It is intentionally based on the explicit
// marker rather than re-parsing a potentially truncated response body.
func IsAnthropicSoftRateLimitFailover(failoverErr *UpstreamFailoverError) bool {
	return failoverErr != nil && failoverErr.AnthropicSoftRateLimit &&
		failoverErr.StatusCode == http.StatusTooManyRequests
}

// CommitAnthropicSoftRateLimit persists the adaptive cooldown only after the
// dedicated same-account retry has failed. The marker is kept on the error so
// callers can still report the original upstream classification.
func (s *GatewayService) CommitAnthropicSoftRateLimit(
	ctx context.Context,
	accountID int64,
	failoverErr *UpstreamFailoverError,
	selectedAccount ...*Account,
) {
	if s == nil || accountID <= 0 || !IsAnthropicSoftRateLimitFailover(failoverErr) ||
		failoverErr.AnthropicSoftRateLimitCommitted || s.rateLimitService == nil {
		return
	}
	var account *Account
	if len(selectedAccount) > 0 {
		account = selectedAccount[0]
	}
	if account == nil && s.accountRepo != nil {
		var err error
		account, err = s.accountRepo.GetByID(ctx, accountID)
		if err != nil {
			return
		}
	}
	if account == nil {
		return
	}
	s.rateLimitService.HandleUpstreamError(ctx, account, failoverErr.StatusCode, failoverErr.ResponseHeaders, failoverErr.ResponseBody)
	failoverErr.AnthropicSoftRateLimitCommitted = true
}
