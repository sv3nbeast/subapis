package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

func normalizeGrokExhaustedWindowResets(snapshot *xai.QuotaSnapshot, resetAt, now time.Time) {
	if snapshot == nil || !resetAt.After(now) {
		return
	}
	for _, window := range []*xai.QuotaWindow{snapshot.Requests, snapshot.Tokens} {
		if window == nil || window.Remaining == nil || *window.Remaining > 0 {
			continue
		}
		candidate := time.Time{}
		if window.ResetUnix != nil && *window.ResetUnix > 0 {
			candidate = time.Unix(*window.ResetUnix, 0)
		} else if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(window.ResetAt)); err == nil {
			candidate = parsed
		}
		if !candidate.After(now) {
			candidate = resetAt
		}
		resetUnix := candidate.Unix()
		window.ResetUnix = &resetUnix
		window.ResetAt = candidate.UTC().Format(time.RFC3339)
	}
}

func grokRateLimitResetAt(snapshot *xai.QuotaSnapshot, now time.Time) (time.Time, bool) {
	if snapshot == nil {
		return time.Time{}, false
	}
	retryAfterExpired := false
	var resetAt time.Time
	if snapshot.RetryAfterSeconds != nil && *snapshot.RetryAfterSeconds > 0 {
		observedAt := now
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(snapshot.UpdatedAt)); err == nil {
			observedAt = parsed
		}
		candidate := observedAt.Add(time.Duration(*snapshot.RetryAfterSeconds) * time.Second)
		if candidate.After(now) {
			resetAt = candidate
		} else {
			retryAfterExpired = true
		}
	}
	exhausted := false
	for _, window := range []*xai.QuotaWindow{snapshot.Requests, snapshot.Tokens} {
		if window == nil || window.Remaining == nil || *window.Remaining > 0 {
			continue
		}
		exhausted = true
		candidate := time.Time{}
		if window.ResetUnix != nil && *window.ResetUnix > 0 {
			candidate = time.Unix(*window.ResetUnix, 0)
		} else if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(window.ResetAt)); err == nil {
			candidate = parsed
		}
		if candidate.After(now) && candidate.After(resetAt) {
			resetAt = candidate
		}
	}
	if !resetAt.IsZero() {
		return resetAt, true
	}
	if retryAfterExpired {
		return time.Time{}, false
	}
	if exhausted || snapshot.StatusCode == http.StatusTooManyRequests {
		return now.Add(grokRateLimitFallbackCooldown), true
	}
	return time.Time{}, false
}

func grokRateLimitResetAtForAccount(account *Account, snapshot *xai.QuotaSnapshot, now time.Time) (time.Time, bool) {
	resetAt, limited := grokRateLimitResetAt(snapshot, now)
	if !limited || !isGrokOAuthAccount(account) || snapshot == nil || snapshot.StatusCode != http.StatusTooManyRequests {
		return resetAt, limited
	}
	if account.RateLimitedAt == nil || account.RateLimitResetAt == nil {
		return resetAt, true
	}
	previousResetAt := *account.RateLimitResetAt
	if previousResetAt.After(now) || now.Sub(previousResetAt) > grokRateLimitBackoffQuietPeriod {
		return resetAt, true
	}
	previousCooldown := previousResetAt.Sub(*account.RateLimitedAt)
	if previousCooldown <= 0 {
		return resetAt, true
	}
	adaptiveCooldown := grokRateLimitRepeatCooldown
	if previousCooldown >= grokRateLimitSustainedCooldown {
		adaptiveCooldown = grokRateLimitMaxAdaptiveCooldown
	} else if previousCooldown >= grokRateLimitRepeatCooldown {
		adaptiveCooldown = grokRateLimitSustainedCooldown
	}
	if adaptive := now.Add(adaptiveCooldown); adaptive.After(resetAt) {
		resetAt = adaptive
	}
	return resetAt, true
}

func normalizeGrokRateLimitResetAt(account *Account, resetAt, now time.Time) time.Time {
	if !resetAt.After(now) {
		resetAt = now.Add(grokRateLimitFallbackCooldown)
	}
	if account != nil && account.RateLimitResetAt != nil && account.RateLimitResetAt.After(resetAt) {
		resetAt = *account.RateLimitResetAt
	}
	return resetAt
}

type grokRateLimitExtendingRepository interface {
	SetRateLimitedIfLater(context.Context, int64, time.Time) error
}

type grokRateLimitRecoveryRepository interface {
	ClearRateLimitIfObserved(context.Context, int64, time.Time, time.Time) (bool, error)
}

func isSuccessfulGrokRateLimitRecovery(account *Account, snapshot *xai.QuotaSnapshot) bool {
	return isGrokOAuthAccount(account) && account.RateLimitedAt != nil && account.RateLimitResetAt != nil && snapshot != nil && snapshot.StatusCode >= 200 && snapshot.StatusCode < 300
}

func clearGrokRateLimitAfterRecovery(ctx context.Context, repo AccountRepository, account *Account) {
	if repo == nil || account == nil || account.RateLimitedAt == nil || account.RateLimitResetAt == nil || ctx.Err() != nil {
		return
	}
	recoveryRepo, ok := repo.(grokRateLimitRecoveryRepository)
	if !ok {
		return
	}
	if _, err := recoveryRepo.ClearRateLimitIfObserved(ctx, account.ID, *account.RateLimitedAt, *account.RateLimitResetAt); err != nil {
		slog.Warn("grok_rate_limit_recovery_clear_failed", "account_id", account.ID, "error", err)
	}
}

func persistGrokRateLimit(ctx context.Context, repo AccountRepository, account *Account, resetAt time.Time) {
	if repo == nil || account == nil || account.ID <= 0 {
		return
	}
	resetAt = normalizeGrokRateLimitResetAt(account, resetAt, time.Now())
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	var err error
	if extendingRepo, ok := repo.(grokRateLimitExtendingRepository); ok {
		err = extendingRepo.SetRateLimitedIfLater(stateCtx, account.ID, resetAt)
	} else {
		err = repo.SetRateLimited(stateCtx, account.ID, resetAt)
	}
	if err != nil {
		slog.Warn("persist_grok_rate_limit_failed", "account_id", account.ID, "reset_at", resetAt.UTC(), "error", err)
	}
}

func grokLocalUsageForQuota(ctx context.Context, repo UsageLogRepository, accountID int64, billing *xai.BillingSummary, now time.Time) (*WindowStats, *WindowStats, *WindowStats) {
	if grokBillingHasAuthoritativeQuota(billing) {
		weekly, monthly := grokLocalUsageForBilling(ctx, repo, accountID, billing, now)
		return nil, weekly, monthly
	}
	return grokLocalUsage24h(ctx, repo, accountID, now), nil, nil
}

func grokLocalUsage24h(ctx context.Context, repo UsageLogRepository, accountID int64, now time.Time) *WindowStats {
	if repo == nil || accountID <= 0 {
		return nil
	}
	start := now.UTC().Add(-grokFreeQuotaWindow)
	stats, err := repo.GetAccountWindowStats(ctx, accountID, start)
	if err != nil {
		slog.Warn("grok_rolling_24h_usage_query_failed", "account_id", accountID, "window_start", start, "error", err)
		return nil
	}
	return windowStatsFromAccountStats(stats)
}

func grokLocalUsageForBilling(ctx context.Context, repo UsageLogRepository, accountID int64, billing *xai.BillingSummary, now time.Time) (*WindowStats, *WindowStats) {
	if repo == nil || accountID <= 0 {
		return nil, nil
	}
	var weekly, monthly *WindowStats
	if start, ok := currentGrokBillingWindow(billing, true, now); ok {
		if stats, err := repo.GetAccountWindowStats(ctx, accountID, start); err == nil {
			weekly = windowStatsFromAccountStats(stats)
		}
	}
	if start, ok := currentGrokBillingWindow(billing, false, now); ok {
		if stats, err := repo.GetAccountWindowStats(ctx, accountID, start); err == nil {
			monthly = windowStatsFromAccountStats(stats)
		}
	}
	return weekly, monthly
}

func currentGrokBillingWindow(billing *xai.BillingSummary, weekly bool, now time.Time) (time.Time, bool) {
	if billing == nil {
		return time.Time{}, false
	}
	startRaw, endRaw := billing.BillingPeriodStart, billing.BillingPeriodEnd
	if weekly {
		if billing.PeriodType != "weekly" {
			return time.Time{}, false
		}
		startRaw, endRaw = billing.PeriodStart, billing.PeriodEnd
	}
	start, startErr := parseTime(strings.TrimSpace(startRaw))
	end, endErr := parseTime(strings.TrimSpace(endRaw))
	if startErr != nil || endErr != nil || now.Before(start) || !now.Before(end) {
		return time.Time{}, false
	}
	return start, true
}
