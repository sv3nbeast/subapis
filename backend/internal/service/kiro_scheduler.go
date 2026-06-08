package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

var kiroSchedulerCursor atomic.Uint64

const kiroSchedulerTokenRefreshSkew = 120 * time.Second

func (s *GatewayService) selectKiroAccountWithLoadAwareness(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	accounts []Account,
	useMixed bool,
) (*AccountSelectionResult, error) {
	cfg := s.schedulingConfig()
	candidates := s.filterSelectableAccounts(ctx, accounts, PlatformKiro, useMixed, requestedModel, excludedIDs, false)
	candidates = s.filterKiroSchedulerCandidates(ctx, candidates, requestedModel)
	if len(candidates) == 0 {
		return nil, s.noAvailableSelectionErrorForModel(ctx, groupID, sessionHash, requestedModel, PlatformKiro, accounts, excludedIDs, useMixed)
	}

	ordered := orderKiroSchedulerCandidates(candidates)
	for _, account := range ordered {
		result, err := s.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
		if err == nil && result.Acquired {
			if !s.checkAndRegisterSession(ctx, account, sessionHash) {
				result.ReleaseFunc()
				continue
			}
			if sessionHash != "" && s.cache != nil {
				_ = s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), sessionHash, account.ID, stickySessionTTL)
			}
			slog.Debug("kiro_scheduler_account_selected",
				"group_id", derefGroupID(groupID),
				"model", requestedModel,
				"account_id", account.ID,
				"mode", "kiro_go_weighted_round_robin",
			)
			return s.newSelectionResult(ctx, account, true, result.ReleaseFunc, nil)
		}
	}

	for _, account := range ordered {
		if !s.checkAndRegisterSession(ctx, account, sessionHash) {
			continue
		}
		return s.newSelectionResult(ctx, account, false, nil, &AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: account.Concurrency,
			Timeout:        cfg.FallbackWaitTimeout,
			MaxWaiting:     cfg.FallbackMaxWaiting,
		})
	}
	return nil, s.noAvailableSelectionErrorForModel(ctx, groupID, sessionHash, requestedModel, PlatformKiro, accounts, excludedIDs, useMixed)
}

func (s *GatewayService) filterKiroSchedulerCandidates(ctx context.Context, accounts []*Account, requestedModel string) []*Account {
	if len(accounts) == 0 {
		return accounts
	}
	filtered := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if err := s.validateKiroSchedulerCandidate(ctx, account, requestedModel); err != nil {
			slog.Debug("kiro_scheduler_candidate_skipped",
				"account_id", func() int64 {
					if account == nil {
						return 0
					}
					return account.ID
				}(),
				"model", requestedModel,
				"reason", err.Error(),
			)
			continue
		}
		filtered = append(filtered, account)
	}
	return filtered
}

func orderKiroSchedulerCandidates(accounts []*Account) []*Account {
	if len(accounts) <= 1 {
		return accounts
	}
	weighted := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		for i := 0; i < effectiveKiroSchedulerWeight(account); i++ {
			weighted = append(weighted, account)
		}
	}
	if len(weighted) == 0 {
		return nil
	}

	ordered := make([]*Account, 0, len(accounts))
	seen := make(map[int64]struct{}, len(accounts))
	start := int(kiroSchedulerCursor.Add(1) % uint64(len(weighted)))
	for i := 0; i < len(weighted); i++ {
		account := weighted[(start+i)%len(weighted)]
		if account == nil {
			continue
		}
		if _, ok := seen[account.ID]; ok {
			continue
		}
		seen[account.ID] = struct{}{}
		ordered = append(ordered, account)
	}
	return ordered
}

func effectiveKiroSchedulerWeight(account *Account) int {
	if account == nil {
		return 1
	}
	if account.LoadFactor != nil && *account.LoadFactor > 0 {
		return *account.LoadFactor
	}
	return 1
}

func kiroAccountHasUsableToken(account *Account, now time.Time) bool {
	if account == nil || account.Type != AccountTypeOAuth {
		return true
	}
	expiresAt := account.ExpiresAt
	if expiresAt == nil {
		expiresAt = account.GetCredentialAsTime("expires_at")
	}
	if expiresAt == nil {
		return true
	}
	return now.Before(expiresAt.Add(-kiroSchedulerTokenRefreshSkew))
}

func (s *GatewayService) validateKiroSchedulerCandidate(ctx context.Context, account *Account, requestedModel string) error {
	if account == nil {
		return fmt.Errorf("nil kiro scheduler account")
	}
	if account.Platform != PlatformKiro {
		return fmt.Errorf("non-kiro scheduler account: %s", account.Platform)
	}
	if !kiroAccountHasUsableToken(account, time.Now()) {
		return fmt.Errorf("kiro oauth token is near expiry")
	}
	if requestedModel != "" && !s.isModelSupportedByAccountWithContext(ctx, account, requestedModel) {
		return fmt.Errorf("kiro account does not support model %s", requestedModel)
	}
	return nil
}
