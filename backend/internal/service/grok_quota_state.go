package service

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/tidwall/gjson"
)

const (
	grokQuotaExhaustedFallbackCooldown = 30 * time.Minute
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

	code := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "code").String(),
		gjson.GetBytes(responseBody, "error.code").String(),
	)))
	if strings.Contains(code, "spending-limit") ||
		strings.Contains(code, "credit-limit") ||
		strings.Contains(code, "free-usage-exhausted") {
		return true
	}

	message := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "error").String(),
		gjson.GetBytes(responseBody, "message").String(),
		gjson.GetBytes(responseBody, "error.message").String(),
		string(responseBody),
	)))
	for _, marker := range []string{
		"run out of credits",
		"used all available credits",
		"used all the included free usage",
		"reached its monthly spending limit",
		"purchase more credits",
		"raise your spending limit",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}

	// Billing is the strongest fallback signal when xAI returns a generic 403
	// body. Do not use subscription text alone because a genuine entitlement
	// failure may contain the same wording.
	if billing, err := grokBillingSnapshotFromExtra(account.Extra); err == nil && billing != nil {
		return isGrokBillingExhausted(billing)
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

// resolveGrokQuotaResetAtForResponse keeps rolling free usage separate from the
// billing period. The billing endpoint can report 100% remaining while the
// model-specific rolling token allowance is exhausted.
func resolveGrokQuotaResetAtForResponse(account *Account, responseBody []byte, now time.Time) time.Time {
	if !isGrokFreeUsageExhausted(responseBody) {
		return resolveGrokQuotaResetAt(account, now)
	}
	if account != nil && account.RateLimitResetAt != nil && account.RateLimitResetAt.After(now) {
		return *account.RateLimitResetAt
	}
	return now.Add(grokFreeUsageExhaustedCooldown)
}

// resolveGrokQuotaResetAt uses the observed xAI billing period whenever
// possible. If the billing snapshot is missing/stale, use a short bounded
// cooldown rather than permanently disabling the account.
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
