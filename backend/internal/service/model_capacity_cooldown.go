package service

import (
	"context"
	"strings"
	"sync"
	"time"
)

const modelCapacityCooldownsKey = "model_capacity_cooldowns"

type ModelCapacityCooldownLookup struct {
	AccountID int64
	ModelKey  string
}

type ModelCapacityCooldownCache interface {
	SetModelCapacityCooldown(ctx context.Context, accountID int64, modelKey string, until time.Time) error
	DeleteModelCapacityCooldown(ctx context.Context, accountID int64, modelKey string) error
	BatchGetModelCapacityCooldownRemaining(ctx context.Context, lookups []ModelCapacityCooldownLookup) (map[ModelCapacityCooldownLookup]time.Duration, error)
}

type accountModelCapacityCooldownKey struct {
	accountID int64
	modelKey  string
}

type modelCapacityCooldownPrefetchContextKeyType struct{}

var (
	modelCapacityCooldownPrefetchContextKey = modelCapacityCooldownPrefetchContextKeyType{}
	accountModelCapacityCooldownMu          sync.RWMutex
	accountModelCapacityCooldownUntil       = make(map[accountModelCapacityCooldownKey]time.Time)
)

func resolveRequestedModelKey(ctx context.Context, account *Account, requestedModel string) string {
	if account == nil {
		return ""
	}

	modelKey := account.GetMappedModel(requestedModel)
	if account.Platform == PlatformAntigravity {
		modelKey = resolveFinalAntigravityModelKey(ctx, account, requestedModel)
	}
	return strings.TrimSpace(modelKey)
}

func modelCapacityCooldownMapKey(accountID int64, modelKey string) accountModelCapacityCooldownKey {
	return accountModelCapacityCooldownKey{
		accountID: accountID,
		modelKey:  strings.TrimSpace(modelKey),
	}
}

func modelCapacityCooldownLookupKey(accountID int64, modelKey string) ModelCapacityCooldownLookup {
	return ModelCapacityCooldownLookup{
		AccountID: accountID,
		ModelKey:  strings.TrimSpace(modelKey),
	}
}

func setAccountModelCapacityCooldown(accountID int64, modelKey string, until time.Time) bool {
	if accountID <= 0 || strings.TrimSpace(modelKey) == "" || !until.After(time.Now()) {
		return false
	}

	key := modelCapacityCooldownMapKey(accountID, modelKey)

	accountModelCapacityCooldownMu.Lock()
	defer accountModelCapacityCooldownMu.Unlock()

	prev, exists := accountModelCapacityCooldownUntil[key]
	accountModelCapacityCooldownUntil[key] = until
	return !exists || until.After(prev)
}

func clearAccountModelCapacityCooldown(accountID int64, modelKey string) bool {
	if accountID <= 0 || strings.TrimSpace(modelKey) == "" {
		return false
	}

	key := modelCapacityCooldownMapKey(accountID, modelKey)

	accountModelCapacityCooldownMu.Lock()
	defer accountModelCapacityCooldownMu.Unlock()

	if _, exists := accountModelCapacityCooldownUntil[key]; !exists {
		return false
	}
	delete(accountModelCapacityCooldownUntil, key)
	return true
}

func getAccountModelCapacityCooldownRemaining(accountID int64, modelKey string) time.Duration {
	if accountID <= 0 || strings.TrimSpace(modelKey) == "" {
		return 0
	}

	key := modelCapacityCooldownMapKey(accountID, modelKey)

	accountModelCapacityCooldownMu.RLock()
	until, exists := accountModelCapacityCooldownUntil[key]
	accountModelCapacityCooldownMu.RUnlock()
	if !exists {
		return 0
	}

	remaining := time.Until(until)
	if remaining > 0 {
		return remaining
	}

	accountModelCapacityCooldownMu.Lock()
	if currentUntil, ok := accountModelCapacityCooldownUntil[key]; ok && !time.Now().Before(currentUntil) {
		delete(accountModelCapacityCooldownUntil, key)
	}
	accountModelCapacityCooldownMu.Unlock()
	return 0
}

func modelCapacityCooldownFromPrefetchContext(ctx context.Context, accountID int64) (time.Duration, bool) {
	if ctx == nil || accountID <= 0 {
		return 0, false
	}
	m, ok := ctx.Value(modelCapacityCooldownPrefetchContextKey).(map[int64]time.Duration)
	if !ok || len(m) == 0 {
		return 0, false
	}
	v, exists := m[accountID]
	return v, exists
}

func modelCapacityCooldownResetAtFromExtra(extra map[string]any, modelKey string) *time.Time {
	if len(extra) == 0 || strings.TrimSpace(modelKey) == "" {
		return nil
	}
	rawCooldowns, ok := extra[modelCapacityCooldownsKey].(map[string]any)
	if !ok {
		return nil
	}
	rawCooldown, ok := rawCooldowns[modelKey].(map[string]any)
	if !ok {
		return nil
	}
	resetAtRaw, ok := rawCooldown["cooldown_reset_at"].(string)
	if !ok || strings.TrimSpace(resetAtRaw) == "" {
		return nil
	}
	resetAt, err := time.Parse(time.RFC3339, resetAtRaw)
	if err != nil {
		return nil
	}
	return &resetAt
}

func getAccountModelCapacityCooldownRemainingFromExtra(extra map[string]any, modelKey string) time.Duration {
	resetAt := modelCapacityCooldownResetAtFromExtra(extra, modelKey)
	if resetAt == nil {
		return 0
	}
	remaining := time.Until(*resetAt)
	if remaining > 0 {
		return remaining
	}
	return 0
}

func setAccountModelCapacityCooldownExtra(account *Account, modelKey string, until time.Time) bool {
	if account == nil || strings.TrimSpace(modelKey) == "" || !until.After(time.Now()) {
		return false
	}
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	cooldowns, _ := account.Extra[modelCapacityCooldownsKey].(map[string]any)
	if cooldowns == nil {
		cooldowns = make(map[string]any)
		account.Extra[modelCapacityCooldownsKey] = cooldowns
	}

	prevRemaining := getAccountModelCapacityCooldownRemainingFromExtra(account.Extra, modelKey)
	cooldowns[modelKey] = map[string]any{
		"cooldown_at":       time.Now().UTC().Format(time.RFC3339),
		"cooldown_reset_at": until.UTC().Format(time.RFC3339),
	}
	return prevRemaining <= 0 || until.After(time.Now().Add(prevRemaining))
}

func clearAccountModelCapacityCooldownExtra(account *Account, modelKey string) bool {
	if account == nil || account.Extra == nil || strings.TrimSpace(modelKey) == "" {
		return false
	}
	rawCooldowns, ok := account.Extra[modelCapacityCooldownsKey].(map[string]any)
	if !ok || rawCooldowns == nil {
		return false
	}
	if _, exists := rawCooldowns[modelKey]; !exists {
		return false
	}
	delete(rawCooldowns, modelKey)
	if len(rawCooldowns) == 0 {
		delete(account.Extra, modelCapacityCooldownsKey)
	} else {
		account.Extra[modelCapacityCooldownsKey] = rawCooldowns
	}
	return true
}

func (a *Account) isModelCapacityCoolingDownWithContext(ctx context.Context, requestedModel string) bool {
	if a == nil {
		return false
	}
	return a.GetModelCapacityCooldownRemainingTimeWithContext(ctx, requestedModel) > 0
}

func (a *Account) GetModelCapacityCooldownRemainingTimeWithContext(ctx context.Context, requestedModel string) time.Duration {
	if a == nil {
		return 0
	}

	if remaining, ok := modelCapacityCooldownFromPrefetchContext(ctx, a.ID); ok {
		if remaining > 0 {
			return remaining
		}
	}
	modelKey := resolveRequestedModelKey(ctx, a, requestedModel)
	rawRequestedModel := strings.TrimSpace(requestedModel)
	if modelKey == "" {
		modelKey = rawRequestedModel
	}
	if remaining := getAccountModelCapacityCooldownRemainingFromExtra(a.Extra, modelKey); remaining > 0 {
		return remaining
	}
	if remaining := getAccountModelCapacityCooldownRemaining(a.ID, modelKey); remaining > 0 {
		return remaining
	}
	if rawRequestedModel != "" && rawRequestedModel != modelKey {
		if remaining := getAccountModelCapacityCooldownRemainingFromExtra(a.Extra, rawRequestedModel); remaining > 0 {
			return remaining
		}
		return getAccountModelCapacityCooldownRemaining(a.ID, rawRequestedModel)
	}
	return 0
}
