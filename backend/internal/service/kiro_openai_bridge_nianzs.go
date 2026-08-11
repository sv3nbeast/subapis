package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	nianzscooldown "github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown_nianzs"
)

// withNianzsKiroCooldownPrefetch adapts the pinned nianzs cooldown state to
// the existing mixed OpenAI/Kiro scheduler context. The adapter is deliberately
// one-way: nianzs state is read from its isolated keyspace and is never written
// into, or combined with, the legacy cooldown state machine.
func (s *GatewayService) withNianzsKiroCooldownPrefetch(ctx context.Context, accounts []Account, groupID *int64) context.Context {
	if s == nil || s.nianzsKiroCooldownStore == nil || len(accounts) == 0 {
		return ctx
	}

	states := make(map[int64]*kirocooldown.State)
	for i := range accounts {
		account := &accounts[i]
		if !nianzsIsKiroDirectModeAccount(account) {
			continue
		}
		key := nianzsBuildKiroAccountKey(account)
		if key == "" {
			continue
		}
		state, err := s.nianzsKiroCooldownStore.GetState(ctx, key)
		if err != nil {
			// Match the pinned scheduler: cooldown-store failures are fail-open so
			// a Redis incident cannot take the whole upstream pool offline.
			slog.Warn("kiro_cooldown_prefetch_failed",
				"engine", string(KiroEngineNianzs),
				"group_id", derefGroupID(groupID),
				"account_id", account.ID,
				"error", err,
			)
			continue
		}
		if state == nil || !state.Active {
			continue
		}
		states[account.ID] = legacyKiroCooldownStateFromNianzs(state)
		slog.Info("kiro_scheduler_cooldown_observed",
			"engine", string(KiroEngineNianzs),
			"group_id", derefGroupID(groupID),
			"account_id", account.ID,
			"reason", state.Reason,
			"remaining_ms", state.Remaining.Milliseconds(),
			"enforced", true,
		)
	}

	return context.WithValue(ctx, kiroCooldownPrefetchContextKey{}, &kiroCooldownPrefetch{
		states:   states,
		enforced: true,
	})
}

// legacyKiroCooldownStateFromNianzs converts only the transport-neutral state
// fields consumed by the shared OpenAI scheduler. It does not invoke or emulate
// any legacy cooldown transition.
func legacyKiroCooldownStateFromNianzs(state *nianzscooldown.State) *kirocooldown.State {
	if state == nil {
		return nil
	}
	return &kirocooldown.State{
		Active:        state.Active,
		Reason:        state.Reason,
		CooldownUntil: state.CooldownUntil,
		Remaining:     state.Remaining,
		FailCount:     state.FailCount,
	}
}

// tryRecoverOpenAIKiroCooldownPoolNianzs mirrors the pinned scheduler's
// exhausted-pool recovery for the local mixed OpenAI/Kiro bridge: only when all
// otherwise eligible Kiro candidates are in transient 429 cooldown do we clear
// the earliest one and let the scheduler re-evaluate once.
func (s *GatewayService) tryRecoverOpenAIKiroCooldownPoolNianzs(
	ctx context.Context,
	groupID *int64,
	accounts []Account,
	cooldownAccountIDs map[int64]struct{},
) bool {
	if s == nil || !s.useNianzsKiroEngine(groupID) || s.nianzsKiroCooldownStore == nil ||
		len(cooldownAccountIDs) == 0 {
		return false
	}

	tokenKeys := make([]string, 0, len(cooldownAccountIDs))
	for i := range accounts {
		account := &accounts[i]
		if _, ok := cooldownAccountIDs[account.ID]; !ok {
			continue
		}
		if !nianzsIsKiroDirectModeAccount(account) {
			return false
		}
		state := kiroCooldownStateFromContext(ctx, account)
		if state == nil || !state.Active || state.Reason != nianzscooldown.CooldownReason429 {
			return false
		}
		key := nianzsBuildKiroAccountKey(account)
		if key == "" {
			return false
		}
		tokenKeys = append(tokenKeys, key)
	}
	if len(tokenKeys) != len(cooldownAccountIDs) {
		return false
	}

	cleared, err := s.nianzsKiroCooldownStore.ClearEarliestTransientCooldown(ctx, tokenKeys)
	if err != nil {
		slog.Warn("kiro_cooldown_pool_recovery_failed",
			"engine", string(KiroEngineNianzs),
			"group_id", derefGroupID(groupID),
			"error", err,
		)
		return false
	}
	if cleared {
		slog.Info("kiro_cooldown_pool_recovery_cleared",
			"engine", string(KiroEngineNianzs),
			"group_id", derefGroupID(groupID),
		)
	}
	return cleared
}

// nianzsKiroCooldownExhaustedFromRepository restores Retry-After diagnostics
// when the repository/snapshot already filtered DB-synchronized cooldowns out
// of the OpenAI bridge pool. It is read-only and deliberately does not clear DB
// state; recovery remains the same one-shot, in-memory path as pinned nianzs.
func (s *GatewayService) nianzsKiroCooldownExhaustedFromRepository(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	excludedIDs map[int64]struct{},
	eligible func(*Account) bool,
) error {
	var accounts []Account
	var err error
	if groupID != nil {
		accounts, err = s.accountRepo.ListByGroup(ctx, *groupID)
	} else {
		accounts, err = s.accountRepo.ListByPlatform(ctx, PlatformKiro)
	}
	if err != nil {
		slog.Warn("kiro_cooldown_fallback_candidates_failed",
			"engine", string(KiroEngineNianzs),
			"group_id", derefGroupID(groupID),
			"error", err,
		)
		return nil
	}

	now := time.Now()
	candidates := make([]Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if !nianzsIsKiroDirectModeAccount(account) || !s.isAccountInGroupNianzs(account, groupID) {
			continue
		}
		if _, excluded := excludedIDs[account.ID]; excluded {
			continue
		}
		if !s.isAccountSchedulableForModelSelectionIgnoringAccountRateLimit(ctx, account, requestedModel, now) {
			continue
		}
		if eligible != nil && !eligible(account) {
			continue
		}
		candidates = append(candidates, *account)
	}
	if len(candidates) == 0 {
		return nil
	}

	prefetchedCtx := s.withNianzsKiroCooldownPrefetch(ctx, candidates, groupID)
	accountIDs := make(map[int64]struct{}, len(candidates))
	for i := range candidates {
		if kiroCooldownStateFromContext(prefetchedCtx, &candidates[i]) != nil {
			accountIDs[candidates[i].ID] = struct{}{}
		}
	}
	if len(accountIDs) == 0 {
		return nil
	}
	return kiroCooldownExhaustedErrorFromContext(prefetchedCtx, accountIDs)
}
