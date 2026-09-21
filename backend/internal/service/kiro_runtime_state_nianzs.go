// Source-faithful, namespaced integration of kiro_runtime_state.go from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.
// Only package identifiers and the kiro package import are rewritten so the
// legacy engine remains available for an immediate rollback.

package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	nianzscooldown "github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown_nianzs"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

var nianzsErrKiroCooldownStoreUnavailable = errors.New("kiro cooldown store unavailable")

type NianzsKiroCooldownStore interface {
	CheckCooldown(ctx context.Context, tokenKey string) error
	MarkSuccess(ctx context.Context, tokenKey string) error
	Mark429(ctx context.Context, tokenKey string) (time.Duration, error)
	MarkSuspended(ctx context.Context, tokenKey string) (time.Duration, error)
	GetState(ctx context.Context, tokenKey string) (*nianzscooldown.State, error)
	ClearEarliestTransientCooldown(ctx context.Context, tokenKeys []string) (bool, error)
}

// nianzsKiroCooldownBaseStore 是 NianzsKiroCooldownStore 的可选能力：允许调用方
// 指定首次冷却时长（对应 gateway.kiro_resilience.cooldown_429_seconds）。未实现
// 该能力的实现回退到 Mark429 的内置 ShortCooldown。
type nianzsKiroCooldownBaseStore interface {
	Mark429WithBase(ctx context.Context, tokenKey string, base time.Duration) (time.Duration, error)
}

func nianzsAsKiroCooldownFailoverError(err error) *UpstreamFailoverError {
	if err == nil {
		return nil
	}
	var cooldownErr *nianzscooldown.Error
	if !errors.As(err, &cooldownErr) {
		return nil
	}
	return &UpstreamFailoverError{
		StatusCode:   http.StatusTooManyRequests,
		ResponseBody: []byte(cooldownErr.Error()),
	}
}

func (s *GatewayService) checkKiroCooldownNianzs(ctx context.Context, tokenKey string) error {
	if s == nil || s.nianzsKiroCooldownStore == nil {
		return nianzsErrKiroCooldownStoreUnavailable
	}
	return s.nianzsKiroCooldownStore.CheckCooldown(ctx, tokenKey)
}

// markKiroSuccessNianzs records a successful Kiro response. Pair with markKiro429Nianzs:
// because markKiro429Nianzs writes account.rate_limit_reset_at into DB, an account
// that recovers (success) must also clear that DB field, otherwise the
// scheduler keeps filtering the now-healthy account until the stale
// rate_limit_reset_at naturally expires (up to 5min). accountID may be 0 for
// callers that don't have it (Redis-only clear).
func (s *GatewayService) markKiroSuccessNianzs(ctx context.Context, accountID int64, tokenKey string) error {
	if s == nil || s.nianzsKiroCooldownStore == nil {
		return nianzsErrKiroCooldownStoreUnavailable
	}
	if err := s.nianzsKiroCooldownStore.MarkSuccess(ctx, tokenKey); err != nil {
		return err
	}
	if s.accountRepo != nil && accountID > 0 {
		if dbErr := s.accountRepo.ClearRateLimit(ctx, accountID); dbErr != nil {
			logger.L().Warn("kiro.mark_success_db_clear_failed",
				zap.Int64("account_id", accountID),
				zap.Error(dbErr),
			)
		}
	}
	return nil
}

// markKiro429Nianzs records a Kiro 429 in both Redis (kiroCooldownStore) and DB
// (account.rate_limit_reset_at). Syncing to DB is critical: without it,
// ListSchedulable* still returns this account as schedulable, so the failover
// loop keeps re-picking it just to bounce off the Redis gate (nianzsAsKiroCooldownFailoverError)
// — burning failover slots and amplifying retry storms. accountID may be 0 for
// callers that don't have it; we fall back to Redis-only.
func (s *GatewayService) markKiro429Nianzs(ctx context.Context, accountID int64, tokenKey string) (time.Duration, error) {
	if s == nil || s.nianzsKiroCooldownStore == nil {
		return 0, nianzsErrKiroCooldownStoreUnavailable
	}
	base, enabled := s.kiro429CooldownBase()
	if !enabled {
		// 运维决策（2026-09-21）：短时 429 不做账号级冷却——共享 Kiro 池只剩少量
		// 活跃账号时，逐个 60s 冷却会让全员同时缺席、混合池被掏空。重试与切号
		// 由请求内退避 + failover 承担；月度 402 与 suspended 403 不走本函数。
		return 0, nil
	}
	var (
		cooldown time.Duration
		err      error
	)
	if extended, ok := s.nianzsKiroCooldownStore.(nianzsKiroCooldownBaseStore); ok {
		cooldown, err = extended.Mark429WithBase(ctx, tokenKey, base)
	} else {
		cooldown, err = s.nianzsKiroCooldownStore.Mark429(ctx, tokenKey)
	}
	if err != nil {
		return 0, err
	}
	if s.accountRepo != nil && accountID > 0 && cooldown > 0 {
		resetAt := time.Now().Add(cooldown)
		if dbErr := s.accountRepo.SetRateLimited(ctx, accountID, resetAt); dbErr != nil {
			logger.L().Warn("kiro.mark_429_db_sync_failed",
				zap.Int64("account_id", accountID),
				zap.Duration("cooldown", cooldown),
				zap.Error(dbErr),
			)
		}
	}
	return cooldown, nil
}

func (s *GatewayService) markKiroSuspendedNianzs(ctx context.Context, tokenKey string) (time.Duration, error) {
	if s == nil || s.nianzsKiroCooldownStore == nil {
		return 0, nianzsErrKiroCooldownStoreUnavailable
	}
	return s.nianzsKiroCooldownStore.MarkSuspended(ctx, tokenKey)
}

func (s *GatewayService) getKiroCooldownStateNianzs(ctx context.Context, tokenKey string) (*nianzscooldown.State, error) {
	if s == nil || s.nianzsKiroCooldownStore == nil {
		return nil, nianzsErrKiroCooldownStoreUnavailable
	}
	return s.nianzsKiroCooldownStore.GetState(ctx, tokenKey)
}

func nianzsKiroRuntimeStateSnapshot(state *nianzscooldown.State) (string, string, *time.Time) {
	if state == nil || !state.Active {
		return "", "", nil
	}
	resetAt := state.CooldownUntil
	switch state.Reason {
	case nianzscooldown.CooldownReasonSuspended:
		return "suspended", state.Reason, &resetAt
	default:
		return "cooldown", state.Reason, &resetAt
	}
}
