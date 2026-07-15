package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
)

var errKiroCooldownStoreUnavailable = errors.New("kiro cooldown store unavailable")

type KiroCooldownStore interface {
	ReserveRequest(ctx context.Context, tokenKey string) (time.Duration, error)
	MarkSuccess(ctx context.Context, tokenKey string) error
	Mark429(ctx context.Context, tokenKey string) (time.Duration, error)
	MarkSuspended(ctx context.Context, tokenKey string) (time.Duration, error)
	GetState(ctx context.Context, tokenKey string) (*kirocooldown.State, error)
	ClearEarliestTransientCooldown(ctx context.Context, tokenKeys []string) (bool, error)
}

type kiroCooldownRetryAfterStore interface {
	Mark429WithRetryAfter(ctx context.Context, tokenKey string, retryAfter time.Duration) (time.Duration, error)
}

type kiroCooldownUnresponsiveStore interface {
	MarkUnresponsive(ctx context.Context, tokenKey string, base, maximum time.Duration) (time.Duration, error)
}

type kiroCooldownSuccessPreservingStore interface {
	MarkSuccessPreservingCooldown(ctx context.Context, tokenKey string) error
}

type kiroCooldownBatchStore interface {
	GetStates(ctx context.Context, tokenKeys []string) (map[string]*kirocooldown.State, error)
}

func asKiroCooldownFailoverError(err error) *UpstreamFailoverError {
	if err == nil {
		return nil
	}
	var cooldownErr *kirocooldown.Error
	if !errors.As(err, &cooldownErr) {
		return nil
	}
	statusCode := http.StatusServiceUnavailable
	failureKind := UpstreamFailureTransportError
	kiroRateLimited := false
	if cooldownErr.Reason() == kirocooldown.CooldownReason429 {
		statusCode = http.StatusTooManyRequests
		failureKind = UpstreamFailureRateLimited
		kiroRateLimited = true
	}
	return &UpstreamFailoverError{
		StatusCode:            statusCode,
		ResponseBody:          []byte(cooldownErr.Error()),
		KiroRateLimited:       kiroRateLimited,
		KiroCooldownCommitted: kiroRateLimited,
		FailureKind:           failureKind,
		RetryAfter:            cooldownErr.Remaining(),
	}
}

func (s *GatewayService) ensureKiro429Cooldown(ctx context.Context, account *Account, groupID *int64, failoverErr *UpstreamFailoverError) {
	if failoverErr == nil || failoverErr.KiroCooldownCommitted {
		return
	}
	failoverErr.RetryAfter = s.markKiroAccount429(ctx, account, groupID, failoverErr.ResponseHeaders)
	failoverErr.KiroCooldownCommitted = true
}

func (s *GatewayService) checkAndWaitKiroCooldown(ctx context.Context, tokenKey string) error {
	return s.checkAndWaitKiroCooldownWithMode(ctx, tokenKey, false)
}

func (s *GatewayService) checkAndWaitKiroCooldownWithMode(ctx context.Context, tokenKey string, enforce bool) error {
	if s == nil || s.kiroCooldownStore == nil {
		if enforce {
			return nil
		}
		return errKiroCooldownStoreUnavailable
	}
	state, err := s.kiroCooldownStore.GetState(ctx, tokenKey)
	if err != nil {
		if enforce {
			slog.Warn("kiro_cooldown_check_failed_open", "error", err)
			return nil
		}
		return err
	}
	if state == nil || !state.Active {
		return nil
	}

	switch state.Reason {
	case kirocooldown.CooldownReason429, kirocooldown.CooldownReasonUnresponsive:
		if enforce {
			return kirocooldown.NewError(state.Remaining, state.Reason)
		}
		// Legacy/observe traffic remains fail-open, but must not erase shared
		// transient cooldowns established by an enforce group.
		return nil
	case kirocooldown.CooldownReasonSuspended:
		return kirocooldown.NewError(state.Remaining, state.Reason)
	default:
		if state.Remaining > 0 {
			return kirocooldown.NewError(state.Remaining, state.Reason)
		}
		return nil
	}
}

func (s *GatewayService) markKiroSuccess(ctx context.Context, tokenKey string) error {
	if s == nil || s.kiroCooldownStore == nil {
		return errKiroCooldownStoreUnavailable
	}
	return s.kiroCooldownStore.MarkSuccess(ctx, tokenKey)
}

func (s *GatewayService) markKiroSuccessPreservingCooldown(ctx context.Context, tokenKey string) error {
	if s == nil || s.kiroCooldownStore == nil {
		return errKiroCooldownStoreUnavailable
	}
	if extended, ok := s.kiroCooldownStore.(kiroCooldownSuccessPreservingStore); ok {
		return extended.MarkSuccessPreservingCooldown(ctx, tokenKey)
	}
	state, err := s.kiroCooldownStore.GetState(ctx, tokenKey)
	if err != nil {
		return err
	}
	if state != nil && state.Active {
		return nil
	}
	return s.kiroCooldownStore.MarkSuccess(ctx, tokenKey)
}

func (s *GatewayService) markKiro429(ctx context.Context, tokenKey string) (time.Duration, error) {
	if s == nil || s.kiroCooldownStore == nil {
		return 0, errKiroCooldownStoreUnavailable
	}
	return s.kiroCooldownStore.Mark429(ctx, tokenKey)
}

func (s *GatewayService) markKiro429WithRetryAfter(ctx context.Context, tokenKey string, retryAfter time.Duration) (time.Duration, error) {
	if s == nil || s.kiroCooldownStore == nil {
		return 0, errKiroCooldownStoreUnavailable
	}
	if extended, ok := s.kiroCooldownStore.(kiroCooldownRetryAfterStore); ok {
		return extended.Mark429WithRetryAfter(ctx, tokenKey, retryAfter)
	}
	return s.kiroCooldownStore.Mark429(ctx, tokenKey)
}

func (s *GatewayService) markKiroUnresponsive(ctx context.Context, tokenKey string, base, maximum time.Duration) (time.Duration, error) {
	if s == nil || s.kiroCooldownStore == nil {
		return 0, errKiroCooldownStoreUnavailable
	}
	if extended, ok := s.kiroCooldownStore.(kiroCooldownUnresponsiveStore); ok {
		return extended.MarkUnresponsive(ctx, tokenKey, base, maximum)
	}
	return s.kiroCooldownStore.Mark429(ctx, tokenKey)
}

func kiroRetryAfterDuration(headers http.Header, now time.Time) time.Duration {
	resetAt := parseRetryAfterResetTime(headers, now)
	if resetAt == nil || !resetAt.After(now) {
		return 0
	}
	retryAfter := resetAt.Sub(now)
	if retryAfter < 5*time.Second {
		return 5 * time.Second
	}
	if retryAfter > kirocooldown.MaxCooldown {
		return kirocooldown.MaxCooldown
	}
	return retryAfter
}

func (s *GatewayService) markKiroAccount429(ctx context.Context, account *Account, groupID *int64, headers http.Header) time.Duration {
	if s == nil || account == nil {
		return 0
	}
	retryAfter := kiroRetryAfterDuration(headers, time.Now())
	mode := s.kiroResilienceMode(groupID)
	if !s.kiroResilienceEnforced(groupID) {
		if mode != "off" {
			slog.Info("kiro_429_cooldown_observed", "group_id", derefGroupID(groupID), "account_id", account.ID, "retry_after_ms", retryAfter.Milliseconds())
		}
		return retryAfter
	}

	stateCtx, stateCancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer stateCancel()
	cooldown, err := s.markKiro429WithRetryAfter(stateCtx, buildKiroCooldownKey(account), retryAfter)
	if err != nil {
		slog.Warn("kiro_429_cooldown_mark_failed", "group_id", derefGroupID(groupID), "account_id", account.ID, "error", err)
		cooldown = retryAfter
		if cooldown <= 0 {
			cooldown = kirocooldown.ShortCooldown
		}
	}
	s.persistKiroRuntimeCooldown(stateCtx, account, cooldown, "kiro_429")
	slog.Info("kiro_429_cooldown_marked",
		"request_id", resolveUsageBillingRequestID(ctx, ""),
		"group_id", derefGroupID(groupID),
		"account_id", account.ID,
		"cooldown_ms", cooldown.Milliseconds(),
	)
	return cooldown
}

func (s *GatewayService) markKiroAccountUnresponsive(ctx context.Context, account *Account, groupID *int64, failureKind UpstreamFailureKind) time.Duration {
	if s == nil || account == nil {
		return 0
	}
	base, maximum := s.kiroUnresponsiveCooldown(groupID)
	if base <= 0 {
		if s.kiroResilienceMode(groupID) != "off" {
			slog.Info("kiro_unresponsive_cooldown_observed", "group_id", derefGroupID(groupID), "account_id", account.ID, "failure_kind", failureKind)
		}
		return 0
	}
	stateCtx, stateCancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer stateCancel()
	cooldown, err := s.markKiroUnresponsive(stateCtx, buildKiroCooldownKey(account), base, maximum)
	if err != nil {
		slog.Warn("kiro_unresponsive_cooldown_mark_failed", "group_id", derefGroupID(groupID), "account_id", account.ID, "failure_kind", failureKind, "error", err)
		cooldown = base
	}
	s.persistKiroRuntimeCooldown(stateCtx, account, cooldown, string(failureKind))
	slog.Info("kiro_unresponsive_cooldown_marked",
		"request_id", resolveUsageBillingRequestID(ctx, ""),
		"group_id", derefGroupID(groupID),
		"account_id", account.ID,
		"failure_kind", failureKind,
		"cooldown_ms", cooldown.Milliseconds(),
	)
	return cooldown
}

func (s *GatewayService) persistKiroRuntimeCooldown(ctx context.Context, account *Account, cooldown time.Duration, reason string) {
	if s == nil || account == nil || cooldown <= 0 {
		return
	}
	resetAt := time.Now().Add(cooldown)
	if account.RateLimitResetAt != nil && account.RateLimitResetAt.After(resetAt) {
		resetAt = *account.RateLimitResetAt
	}
	account.RateLimitResetAt = &resetAt
	if s.rateLimitService != nil {
		s.rateLimitService.notifyAccountSchedulingBlocked(account, resetAt, reason)
	}
	if s.accountRepo != nil {
		if err := s.accountRepo.SetRateLimited(ctx, account.ID, resetAt); err != nil {
			slog.Warn("kiro_runtime_cooldown_persist_failed", "account_id", account.ID, "reason", reason, "reset_at", resetAt, "error", err)
		}
	}
}

func (s *GatewayService) markKiroSuspended(ctx context.Context, tokenKey string) (time.Duration, error) {
	if s == nil || s.kiroCooldownStore == nil {
		return 0, errKiroCooldownStoreUnavailable
	}
	return s.kiroCooldownStore.MarkSuspended(ctx, tokenKey)
}

func (s *GatewayService) getKiroCooldownState(ctx context.Context, tokenKey string) (*kirocooldown.State, error) {
	if s == nil || s.kiroCooldownStore == nil {
		return nil, errKiroCooldownStoreUnavailable
	}
	return s.kiroCooldownStore.GetState(ctx, tokenKey)
}

func kiroRuntimeStateSnapshot(state *kirocooldown.State) (string, string, *time.Time) {
	if state == nil || !state.Active {
		return "", "", nil
	}
	resetAt := state.CooldownUntil
	switch state.Reason {
	case kirocooldown.CooldownReasonSuspended:
		return "suspended", state.Reason, &resetAt
	default:
		return "cooldown", state.Reason, &resetAt
	}
}
