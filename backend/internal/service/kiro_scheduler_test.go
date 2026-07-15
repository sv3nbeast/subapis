package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/stretchr/testify/require"
)

type mappedKiroCooldownStore struct {
	states map[string]*kirocooldown.State
}

type cooldownFilteredKiroAccountRepo struct {
	stubOpenAIAccountRepo
	all []Account
}

type mixedKiroAccountRepo struct {
	stubOpenAIAccountRepo
}

func (r mixedKiroAccountRepo) ListSchedulableUngroupedByPlatforms(_ context.Context, platforms []string) ([]Account, error) {
	allowed := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowed[platform] = struct{}{}
	}
	result := make([]Account, 0, len(r.accounts))
	for i := range r.accounts {
		if _, ok := allowed[r.accounts[i].Platform]; ok {
			result = append(result, r.accounts[i])
		}
	}
	return result, nil
}

func (r mixedKiroAccountRepo) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	return r.ListSchedulableUngroupedByPlatforms(ctx, platforms)
}

func (r cooldownFilteredKiroAccountRepo) ListSchedulableUngroupedByPlatform(context.Context, string) ([]Account, error) {
	return nil, nil
}

func (r cooldownFilteredKiroAccountRepo) ListSchedulableByPlatform(context.Context, string) ([]Account, error) {
	return nil, nil
}

func (r cooldownFilteredKiroAccountRepo) ListByPlatform(_ context.Context, platform string) ([]Account, error) {
	result := make([]Account, 0, len(r.all))
	for i := range r.all {
		if r.all[i].Platform == platform {
			result = append(result, r.all[i])
		}
	}
	return result, nil
}

func (s *mappedKiroCooldownStore) ReserveRequest(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *mappedKiroCooldownStore) MarkSuccess(context.Context, string) error {
	return nil
}

func (s *mappedKiroCooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *mappedKiroCooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *mappedKiroCooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	return false, nil
}

func (s *mappedKiroCooldownStore) GetState(_ context.Context, tokenKey string) (*kirocooldown.State, error) {
	return s.states[tokenKey], nil
}

func (s *mappedKiroCooldownStore) GetStates(_ context.Context, tokenKeys []string) (map[string]*kirocooldown.State, error) {
	result := make(map[string]*kirocooldown.State)
	for _, key := range tokenKeys {
		if state := s.states[key]; state != nil && state.Active {
			result[key] = state
		}
	}
	return result, nil
}

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

func TestSelectAccountWithLoadAwarenessKiroDefersInitialBindingUntilSuccess(t *testing.T) {
	sessionHash := "new-kiro-session"
	account := Account{ID: 11, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1}
	cache := &stubGatewayCache{sessionBindings: map[string]int64{}}
	svc := &GatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{account}},
		cache:              cache,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{account.ID: true}}),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
			Scheduling:     config.GatewaySchedulingConfig{LoadBatchEnabled: true},
		}},
	}
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, nil, sessionHash, "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.True(t, selection.DeferStickyMigration)
	require.Zero(t, cache.sessionBindings[sessionHash], "Kiro binding must be created only after a complete terminal response")
	selection.ReleaseFunc()
}

func TestSelectAccountWithLoadAwarenessKiroBusyStickyUsesIdleAccountAndPreservesBinding(t *testing.T) {
	kiroSchedulerCursor.Store(0)
	sessionHash := "busy-sticky"
	accounts := []Account{
		{ID: 21, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1},
		{ID: 22, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1},
	}
	cache := &stubGatewayCache{sessionBindings: map[string]int64{sessionHash: 21}}
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{21: false, 22: true},
		loadMap: map[int64]*AccountLoadInfo{
			21: {AccountID: 21, LoadRate: 100},
			22: {AccountID: 22, LoadRate: 0},
		},
	}
	svc := &GatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: accounts},
		cache:              cache,
		concurrencyService: NewConcurrencyService(concurrencyCache),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
			Scheduling: config.GatewaySchedulingConfig{
				LoadBatchEnabled:         true,
				StickySessionWaitTimeout: 30 * time.Second,
				StickySessionMaxWaiting:  10,
				FallbackWaitTimeout:      30 * time.Second,
				FallbackMaxWaiting:       10,
			},
		}},
	}
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, nil, sessionHash, "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(22), selection.Account.ID)
	require.True(t, selection.Acquired)
	require.True(t, selection.PreserveStickyBinding)
	require.Equal(t, int64(21), cache.sessionBindings[sessionHash])
	selection.ReleaseFunc()
}

func TestSelectAccountWithLoadAwarenessMixedKiroBusyStickyPreservesBinding(t *testing.T) {
	sessionHash := "mixed-kiro-busy"
	accounts := []Account{
		{ID: 31, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Extra: map[string]any{"mixed_scheduling": true}},
		{ID: 32, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Extra: map[string]any{"mixed_scheduling": true}},
	}
	cache := &stubGatewayCache{sessionBindings: map[string]int64{sessionHash: 31}}
	svc := &GatewayService{
		accountRepo: mixedKiroAccountRepo{stubOpenAIAccountRepo{accounts: accounts}},
		cache:       cache,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{
			acquireResults: map[int64]bool{31: false, 32: true},
			loadMap: map[int64]*AccountLoadInfo{
				31: {AccountID: 31, LoadRate: 100},
				32: {AccountID: 32, LoadRate: 0},
			},
		}),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
			Scheduling:     config.GatewaySchedulingConfig{LoadBatchEnabled: true},
		}},
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, sessionHash, "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.Equal(t, int64(32), selection.Account.ID)
	require.True(t, selection.PreserveStickyBinding)
	require.Equal(t, int64(31), cache.sessionBindings[sessionHash])
	selection.ReleaseFunc()
}

func TestSelectAccountWithLoadAwarenessMixedKiroBusyStickyScansWhenLoadBatchDisabled(t *testing.T) {
	sessionHash := "mixed-kiro-busy-no-batch"
	accounts := []Account{
		{ID: 36, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Extra: map[string]any{"mixed_scheduling": true}},
		{ID: 37, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Extra: map[string]any{"mixed_scheduling": true}},
	}
	cache := &stubGatewayCache{sessionBindings: map[string]int64{sessionHash: 36}}
	svc := &GatewayService{
		accountRepo: mixedKiroAccountRepo{stubOpenAIAccountRepo{accounts: accounts}},
		cache:       cache,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{
			acquireResults: map[int64]bool{36: false, 37: true},
			loadMap: map[int64]*AccountLoadInfo{
				36: {AccountID: 36, LoadRate: 100},
				37: {AccountID: 37, LoadRate: 0},
			},
		}),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
			Scheduling:     config.GatewaySchedulingConfig{LoadBatchEnabled: false},
		}},
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, sessionHash, "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(37), selection.Account.ID)
	require.True(t, selection.PreserveStickyBinding)
	require.Equal(t, int64(36), cache.sessionBindings[sessionHash])
	selection.ReleaseFunc()
}

func TestSelectAccountWithLoadAwarenessMixedKiroFailoverDefersStickyMigration(t *testing.T) {
	sessionHash := "mixed-kiro-failover"
	accounts := []Account{
		{ID: 41, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Extra: map[string]any{"mixed_scheduling": true}},
		{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Extra: map[string]any{"mixed_scheduling": true}},
	}
	cache := &stubGatewayCache{sessionBindings: map[string]int64{sessionHash: 41}}
	svc := &GatewayService{
		accountRepo:        mixedKiroAccountRepo{stubOpenAIAccountRepo{accounts: accounts}},
		cache:              cache,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{42: true}}),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
			Scheduling:     config.GatewaySchedulingConfig{LoadBatchEnabled: true},
		}},
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, sessionHash, "claude-sonnet-4-6", map[int64]struct{}{41: {}}, "", 0)
	require.NoError(t, err)
	require.Equal(t, int64(42), selection.Account.ID)
	require.False(t, selection.PreserveStickyBinding)
	require.True(t, selection.DeferStickyMigration)
	require.Equal(t, int64(41), cache.sessionBindings[sessionHash], "failed-account migration must wait for a complete downstream success")
	selection.ReleaseFunc()
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

func TestSelectAccountWithLoadAwarenessKiroSkipsSharedCooldown(t *testing.T) {
	accounts := []Account{
		{ID: 1, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"refresh_token": "shared-a"}},
		{ID: 2, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"refresh_token": "shared-b"}},
	}
	store := &mappedKiroCooldownStore{states: map[string]*kirocooldown.State{
		buildKiroCooldownKey(&accounts[0]): {
			Active:        true,
			Reason:        kirocooldown.CooldownReason429,
			CooldownUntil: time.Now().Add(time.Minute),
			Remaining:     time.Minute,
		},
	}}
	svc := &GatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: accounts},
		cache:              &stubGatewayCache{},
		kiroCooldownStore:  store,
		concurrencyService: nil,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
			Scheduling:     config.GatewaySchedulingConfig{LoadBatchEnabled: true, FallbackWaitTimeout: time.Second, FallbackMaxWaiting: 10},
		}},
	}
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID)
}

func TestSelectAccountWithLoadAwarenessKiroAllCoolingReturnsRetryAfter(t *testing.T) {
	accounts := []Account{
		{ID: 1, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"refresh_token": "shared-a"}},
		{ID: 2, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"refresh_token": "shared-b"}},
	}
	store := &mappedKiroCooldownStore{states: map[string]*kirocooldown.State{}}
	for i := range accounts {
		remaining := time.Duration(i+1) * time.Minute
		store.states[buildKiroCooldownKey(&accounts[i])] = &kirocooldown.State{
			Active: true, Reason: kirocooldown.CooldownReason429,
			CooldownUntil: time.Now().Add(remaining), Remaining: remaining,
		}
	}
	svc := &GatewayService{
		accountRepo:       stubOpenAIAccountRepo{accounts: accounts},
		cache:             &stubGatewayCache{},
		kiroCooldownStore: store,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
			Scheduling:     config.GatewaySchedulingConfig{LoadBatchEnabled: true, FallbackWaitTimeout: time.Second, FallbackMaxWaiting: 10},
		}},
	}
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-sonnet-4-6", nil, "", 0)
	require.Nil(t, selection)
	var cooldownErr *KiroCooldownExhaustedError
	require.ErrorAs(t, err, &cooldownErr)
	require.Equal(t, 429, cooldownErr.StatusCode)
	require.Greater(t, cooldownErr.RetryAfter, 55*time.Second)
	require.LessOrEqual(t, cooldownErr.RetryAfter, time.Minute)
}

func TestSelectAccountWithLoadAwarenessKiroAllCoolingAfterSnapshotRemovalReturnsRetryAfter(t *testing.T) {
	resetAt := time.Now().Add(time.Minute)
	accounts := []Account{
		{ID: 11, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, RateLimitResetAt: &resetAt, Credentials: map[string]any{"refresh_token": "snapshot-a"}},
		{ID: 12, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, RateLimitResetAt: &resetAt, Credentials: map[string]any{"refresh_token": "snapshot-b"}},
	}
	store := &mappedKiroCooldownStore{states: map[string]*kirocooldown.State{}}
	for i := range accounts {
		remaining := time.Duration(i+1) * 30 * time.Second
		store.states[buildKiroCooldownKey(&accounts[i])] = &kirocooldown.State{
			Active: true, Reason: kirocooldown.CooldownReason429,
			CooldownUntil: time.Now().Add(remaining), Remaining: remaining,
		}
	}
	repo := cooldownFilteredKiroAccountRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: accounts}, all: accounts}
	svc := &GatewayService{
		accountRepo:       repo,
		cache:             &stubGatewayCache{},
		kiroCooldownStore: store,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
			Scheduling:     config.GatewaySchedulingConfig{LoadBatchEnabled: true},
		}},
	}
	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformKiro)

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "", nil, "", 0)
	require.Nil(t, selection)
	var cooldownErr *KiroCooldownExhaustedError
	require.ErrorAs(t, err, &cooldownErr)
	require.Equal(t, http.StatusTooManyRequests, cooldownErr.StatusCode)
	require.Greater(t, cooldownErr.RetryAfter, 25*time.Second)
	require.LessOrEqual(t, cooldownErr.RetryAfter, 30*time.Second)
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
