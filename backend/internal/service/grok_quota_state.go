package service

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/tidwall/gjson"
)

const (
	// Spending-limit responses usually describe a billing window that is not
	// present in the inference response. Keep the account out of the hot path
	// for a full day in that case; a short fallback caused the same exhausted
	// accounts to be probed again every 30 minutes.
	grokQuotaExhaustedFallbackCooldown = 24 * time.Hour
	grokFreeUsageExhaustedCooldown     = 24 * time.Hour
)

func isGrokBillingExhausted(billing *xai.BillingSnapshot) bool {
	if billing == nil {
		return false
	}
	return billing.CreditUsagePercent >= 100 ||
		(billing.CreditUsagePercent > 0 && billing.CreditRemainingPercent <= 0)
}

// isGrokQuotaExhausted distinguishes xAI's quota/spending-limit exhaustion from a
// real entitlement or account ban. xAI reports exhausted subscription credits as
// HTTP 403 on api.x.ai, HTTP 402 (personal-team-blocked:spending-limit), or HTTP
// 429 (subscription:free-usage-exhausted) on the free Build path, so detection
// is body-based and status-agnostic.
func isGrokQuotaExhausted(account *Account, responseBody []byte) bool {
	if account == nil || account.Platform != PlatformGrok {
		return false
	}

	if isGrokQuotaExhaustedBody(responseBody) {
		return true
	}

	// Billing is the strongest fallback signal when xAI returns a generic 403
	// body. Do not use subscription text alone because a genuine entitlement
	// failure may contain the same wording.
	if billing, err := grokBillingSnapshotFromExtra(account.Extra); err == nil && billing != nil {
		return isGrokBillingExhaustionActive(billing, time.Now())
	}
	return false
}

// IsGrokQuotaExhaustedResponse exposes the body-only portion of quota
// classification to the HTTP handlers. The handlers do not have the selected
// account after a failover loop is exhausted, so they can only rely on the
// explicit xAI error code/message when choosing the client status.
func IsGrokQuotaExhaustedResponse(responseBody []byte) bool {
	return isGrokQuotaExhaustedBody(responseBody)
}

func isGrokQuotaExhaustedBody(responseBody []byte) bool {
	// xAI has emitted the same condition under several envelope/code shapes
	// (CLI Build, api.x.ai, and the OpenAI-compatible proxy). Read all known
	// locations instead of relying on one preferred field: a missing nested
	// field used to make an exhausted account look schedulable again.
	code := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		gjson.GetBytes(responseBody, "code").String(),
		gjson.GetBytes(responseBody, "error.code").String(),
		gjson.GetBytes(responseBody, "error.type").String(),
		gjson.GetBytes(responseBody, "type").String(),
		gjson.GetBytes(responseBody, "detail.code").String(),
	}, " ")))
	normalizedCode := strings.NewReplacer("_", "-", ":", "-").Replace(code)
	for _, marker := range []string{
		"spending-limit",
		"credit-limit",
		"free-usage-exhausted",
		"insufficient-quota",
		"quota-exceeded",
		"quota-exhausted",
		"billing-hard-limit",
		"usage-limit-reached",
	} {
		if strings.Contains(normalizedCode, marker) {
			return true
		}
	}

	message := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		gjson.GetBytes(responseBody, "error").String(),
		gjson.GetBytes(responseBody, "message").String(),
		gjson.GetBytes(responseBody, "error.message").String(),
		gjson.GetBytes(responseBody, "detail").String(),
		string(responseBody),
	}, " ")))
	for _, marker := range []string{
		"run out of credits",
		"ran out of credits",
		"no credits left",
		"not enough credits",
		"insufficient credits",
		"credits exhausted",
		"credit balance is insufficient",
		"used all available credits",
		"used all the included free usage",
		"free usage has been exhausted",
		"quota exceeded",
		"quota exhausted",
		"usage limit reached",
		"usage limit exceeded",
		"billing hard limit",
		"billing limit",
		"spending limit",
		"reached its monthly spending limit",
		"purchase more credits",
		"raise your spending limit",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isGrokFreeUsageExhausted(responseBody []byte) bool {
	code := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "code").String(),
		gjson.GetBytes(responseBody, "error.code").String(),
	)))
	if strings.Contains(code, "free-usage-exhausted") {
		return true
	}

	message := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "error").String(),
		gjson.GetBytes(responseBody, "message").String(),
		gjson.GetBytes(responseBody, "error.message").String(),
		string(responseBody),
	)))
	return strings.Contains(message, "used all the included free usage") ||
		(strings.Contains(message, "included free usage") && strings.Contains(message, "rolling 24-hour window"))
}

// grokQuotaWindowResetAt returns the latest reset among exhausted windows.
// A healthy window may reset much later and must not extend the cooldown.
func grokQuotaWindowResetAt(snapshot *xai.QuotaSnapshot, now time.Time) time.Time {
	if snapshot == nil {
		return time.Time{}
	}
	resetAt := time.Time{}
	for _, window := range []*xai.QuotaWindow{snapshot.Requests, snapshot.Tokens} {
		if window == nil || window.Remaining == nil || *window.Remaining > 0 {
			continue
		}
		if window.ResetUnix != nil && *window.ResetUnix > 0 {
			candidate := time.Unix(*window.ResetUnix, 0)
			if candidate.After(now) && candidate.After(resetAt) {
				resetAt = candidate
			}
		}
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(window.ResetAt)); err == nil && parsed.After(now) && parsed.After(resetAt) {
			resetAt = parsed
		}
	}
	if snapshot.RetryAfterSeconds != nil && *snapshot.RetryAfterSeconds > 0 {
		updatedAt := now
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(snapshot.UpdatedAt)); err == nil {
			updatedAt = parsed
		}
		candidate := updatedAt.Add(time.Duration(*snapshot.RetryAfterSeconds) * time.Second)
		if candidate.After(now) && candidate.After(resetAt) {
			resetAt = candidate
		}
	}
	return resetAt
}

// isGrokQuotaSnapshotExhausted reports an explicit zero-window/retry-after
// condition. A snapshot without an active reset is not treated as permanent;
// callers may apply the bounded 24-hour fallback when they need to quarantine
// the credential after a definitive upstream error.
func isGrokQuotaSnapshotExhausted(snapshot *xai.QuotaSnapshot, now time.Time) (bool, time.Time) {
	if snapshot == nil {
		return false, time.Time{}
	}
	if snapshot.UpdatedAt != "" {
		updatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(snapshot.UpdatedAt))
		if err == nil && now.Sub(updatedAt) >= openAICodexAutoPauseStaleAfter {
			return false, time.Time{}
		}
	}
	if snapshot.RetryAfterSeconds != nil && *snapshot.RetryAfterSeconds > 0 {
		resetAt := grokQuotaWindowResetAt(snapshot, now)
		if resetAt.IsZero() || resetAt.After(now) {
			return true, resetAt
		}
	}
	for _, window := range []*xai.QuotaWindow{snapshot.Requests, snapshot.Tokens} {
		if window == nil || window.Remaining == nil || *window.Remaining > 0 {
			continue
		}
		resetAt := grokQuotaWindowResetAt(snapshot, now)
		if resetAt.IsZero() || resetAt.After(now) {
			return true, resetAt
		}
	}
	return false, time.Time{}
}

func isGrokBillingSnapshotFresh(billing *xai.BillingSnapshot, now time.Time) bool {
	if billing == nil || strings.TrimSpace(billing.UpdatedAt) == "" {
		return false
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(billing.UpdatedAt))
	if err != nil || now.Sub(updatedAt) >= openAIQuotaHeadroomSnapshotStaleAfter {
		return false
	}
	return true
}

func isGrokBillingExhaustionActive(billing *xai.BillingSnapshot, now time.Time) bool {
	if !isGrokBillingSnapshotFresh(billing, now) || !isGrokBillingExhausted(billing) {
		return false
	}
	for _, raw := range []string{billing.CurrentPeriodEnd, billing.BillingPeriodEnd} {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if resetAt, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return now.Before(resetAt)
		}
	}
	return true
}

// resolveGrokQuotaResetAtForResponse keeps rolling free usage separate from the
// billing period. The billing endpoint can report 100% remaining while the
// model-specific rolling token allowance is exhausted.
func resolveGrokQuotaResetAtForResponse(account *Account, responseBody []byte, now time.Time) time.Time {
	if !isGrokFreeUsageExhausted(responseBody) {
		return resolveGrokQuotaResetAt(account, now)
	}
	resetAt := now.Add(grokFreeUsageExhaustedCooldown)
	// A generic 429 handler may have already written a short fallback reset.
	// Never let that shorter window downgrade the rolling free-usage cooldown;
	// preserve an existing later reset when it is a real provider window.
	if account != nil && account.RateLimitResetAt != nil && account.RateLimitResetAt.After(resetAt) {
		return *account.RateLimitResetAt
	}
	return resetAt
}

// resolveGrokQuotaResetAt uses the observed xAI billing period whenever
// possible. If the billing snapshot is missing/stale, use a long bounded
// cooldown rather than permanently disabling the account or repeatedly
// hammering an exhausted account.
func resolveGrokQuotaResetAt(account *Account, now time.Time) time.Time {
	if account != nil {
		if billing, err := grokBillingSnapshotFromExtra(account.Extra); err == nil && billing != nil {
			for _, raw := range []string{billing.CurrentPeriodEnd, billing.BillingPeriodEnd} {
				if resetAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); err == nil && resetAt.After(now) {
					return resetAt
				}
			}
		}
		if account.RateLimitResetAt != nil && account.RateLimitResetAt.After(now) {
			return *account.RateLimitResetAt
		}
	}
	return now.Add(grokQuotaExhaustedFallbackCooldown)
}

func isLegacyGrokQuotaExhaustedError(errorMessage string) bool {
	message := strings.ToLower(strings.TrimSpace(errorMessage))
	if message == "" {
		return false
	}
	for _, marker := range []string{
		"personal-team-blocked:spending-limit",
		"run out of credits",
		"used all available credits",
		"reached its monthly spending limit",
		"purchase more credits",
		"raise your spending limit",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
