package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestOrderKiroSchedulerCandidatesUsesKiroGoWeightedRoundRobin(t *testing.T) {
	kiroSchedulerCursor.Store(0)
	weight := 3
	accounts := []*Account{
		{ID: 1, Platform: PlatformKiro, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformKiro, Status: StatusActive, Schedulable: true, LoadFactor: &weight},
		{ID: 3, Platform: PlatformKiro, Status: StatusActive, Schedulable: true},
	}

	firstIDs := make(map[int64]int)
	for i := 0; i < 10; i++ {
		ordered := orderKiroSchedulerCandidates(accounts)
		require.ElementsMatch(t, []int64{1, 2, 3}, kiroSchedulerIDs(ordered))
		firstIDs[ordered[0].ID]++
	}

	require.Greater(t, firstIDs[2], firstIDs[1])
	require.Greater(t, firstIDs[2], firstIDs[3])
}

func TestEffectiveKiroSchedulerWeightIgnoresGenericPriority(t *testing.T) {
	highPriority := &Account{ID: 1, Platform: PlatformKiro, Priority: 1}
	lowPriority := &Account{ID: 2, Platform: PlatformKiro, Priority: 99}
	weight := 4
	explicit := &Account{ID: 3, Platform: PlatformKiro, Priority: 99, LoadFactor: &weight}

	require.Equal(t, 1, effectiveKiroSchedulerWeight(highPriority))
	require.Equal(t, 1, effectiveKiroSchedulerWeight(lowPriority))
	require.Equal(t, 4, effectiveKiroSchedulerWeight(explicit))
}

func TestFilterKiroSchedulerCandidatesSkipsNearExpiryOAuthToken(t *testing.T) {
	now := time.Now().UTC()
	nearExpiry := now.Add(kiroSchedulerTokenRefreshSkew / 2).Format(time.RFC3339)
	validExpiry := now.Add(time.Hour).Format(time.RFC3339)
	accounts := []*Account{
		{
			ID:          1,
			Platform:    PlatformKiro,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"expires_at": nearExpiry},
		},
		{
			ID:          2,
			Platform:    PlatformKiro,
			Type:        AccountTypeOAuth,
			Status:      StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"expires_at": validExpiry},
		},
		{
			ID:          3,
			Platform:    PlatformKiro,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
		},
	}

	filtered := (&GatewayService{}).filterKiroSchedulerCandidates(context.Background(), accounts, "")

	require.Equal(t, []int64{2, 3}, kiroSchedulerIDs(filtered))
}

func TestFilterKiroSchedulerCandidatesRejectsNonKiro(t *testing.T) {
	accounts := []*Account{
		{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true},
		{ID: 2, Platform: PlatformKiro, Status: StatusActive, Schedulable: true},
	}

	filtered := (&GatewayService{}).filterKiroSchedulerCandidates(context.Background(), accounts, "")

	require.Equal(t, []int64{2}, kiroSchedulerIDs(filtered))
}

func TestSelectAccountWithLoadAwarenessUsesKiroSchedulerWhenLoadBatchDisabled(t *testing.T) {
	kiroSchedulerCursor.Store(0)
	repo := stubOpenAIAccountRepo{accounts: []Account{
		{ID: 1, Platform: PlatformKiro, Priority: 1, Status: StatusActive, Schedulable: true, Concurrency: 1},
		{ID: 2, Platform: PlatformKiro, Priority: 2, Status: StatusActive, Schedulable: true, Concurrency: 1},
	}}
	svc := &GatewayService{
		accountRepo: repo,
		cache:       &stubGatewayCache{},
		cfg: &config.Config{Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{
			LoadBatchEnabled:    false,
			FallbackWaitTimeout: 30 * time.Second,
			FallbackMaxWaiting:  100,
		}}},
	}
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	first, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	second, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)

	require.Equal(t, int64(2), first.Account.ID)
	require.Equal(t, int64(1), second.Account.ID)
}

func TestSelectAccountWithLoadAwarenessKiroHonorsStickyBinding(t *testing.T) {
	kiroSchedulerCursor.Store(0)
	sessionHash := "same-session"
	repo := stubOpenAIAccountRepo{accounts: []Account{
		{ID: 1, Platform: PlatformKiro, Status: StatusActive, Schedulable: true, Concurrency: 1},
		{ID: 2, Platform: PlatformKiro, Status: StatusActive, Schedulable: true, Concurrency: 1},
	}}
	cache := &stubGatewayCache{sessionBindings: map[string]int64{sessionHash: 1}}
	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
		cfg: &config.Config{Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{
			LoadBatchEnabled:         true,
			StickySessionWaitTimeout: 30 * time.Second,
			StickySessionMaxWaiting:  100,
			FallbackWaitTimeout:      30 * time.Second,
			FallbackMaxWaiting:       100,
		}}},
	}
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	selected, err := svc.SelectAccountWithLoadAwareness(ctx, nil, sessionHash, "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)

	require.Equal(t, int64(1), selected.Account.ID)
	require.Equal(t, int64(1), cache.sessionBindings[sessionHash])
}

func TestSelectAccountWithLoadAwarenessKiroDoesNotOverwriteExcludedStickyBinding(t *testing.T) {
	kiroSchedulerCursor.Store(0)
	sessionHash := "same-session"
	repo := stubOpenAIAccountRepo{accounts: []Account{
		{ID: 1, Platform: PlatformKiro, Status: StatusActive, Schedulable: true, Concurrency: 1},
		{ID: 2, Platform: PlatformKiro, Status: StatusActive, Schedulable: true, Concurrency: 1},
	}}
	cache := &stubGatewayCache{sessionBindings: map[string]int64{sessionHash: 1}}
	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
		cfg: &config.Config{Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{
			LoadBatchEnabled:    true,
			FallbackWaitTimeout: 30 * time.Second,
			FallbackMaxWaiting:  100,
		}}},
	}
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	selected, err := svc.SelectAccountWithLoadAwareness(ctx, nil, sessionHash, "claude-sonnet-4-6", map[int64]struct{}{1: {}}, "", 0)
	require.NoError(t, err)

	require.Equal(t, int64(2), selected.Account.ID)
	require.Equal(t, int64(1), cache.sessionBindings[sessionHash])
}

func TestSelectAccountWithLoadAwarenessKiroClearsStaleStickyBinding(t *testing.T) {
	kiroSchedulerCursor.Store(0)
	sessionHash := "same-session"
	repo := stubOpenAIAccountRepo{accounts: []Account{
		{ID: 2, Platform: PlatformKiro, Status: StatusActive, Schedulable: true, Concurrency: 1},
	}}
	cache := &stubGatewayCache{sessionBindings: map[string]int64{sessionHash: 1}}
	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
		cfg: &config.Config{Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{
			LoadBatchEnabled:    true,
			FallbackWaitTimeout: 30 * time.Second,
			FallbackMaxWaiting:  100,
		}}},
	}
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	selected, err := svc.SelectAccountWithLoadAwareness(ctx, nil, sessionHash, "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)

	require.Equal(t, int64(2), selected.Account.ID)
	require.Equal(t, int64(2), cache.sessionBindings[sessionHash])
	require.Equal(t, 1, cache.deletedSessions[sessionHash])
}

func kiroSchedulerIDs(accounts []*Account) []int64 {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account != nil {
			ids = append(ids, account.ID)
		}
	}
	return ids
}
