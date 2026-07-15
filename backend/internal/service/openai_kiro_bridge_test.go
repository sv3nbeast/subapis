package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/stretchr/testify/require"
)

type openAIKiroBridgeAccountRepo struct {
	schedulerTestOpenAIAccountRepo
}

func (r openAIKiroBridgeAccountRepo) ListSchedulableByGroupIDAndPlatforms(_ context.Context, groupID int64, platforms []string) ([]Account, error) {
	allowed := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowed[platform] = struct{}{}
	}
	result := make([]Account, 0, len(r.accounts))
	for i := range r.accounts {
		account := r.accounts[i]
		if _, ok := allowed[account.Platform]; ok && openAIStickyAccountMatchesGroup(&account, &groupID) {
			result = append(result, account)
		}
	}
	return result, nil
}

func bridgeTestAccount(id int64, platform string, priority int, groupID int64) Account {
	modelMapping := make(map[string]any, len(OpenAIKiroBridgeModels))
	for _, model := range OpenAIKiroBridgeModels {
		modelMapping[model] = model
	}
	account := Account{
		ID:          id,
		Name:        platform,
		Platform:    platform,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    priority,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{
			"model_mapping": modelMapping,
		},
	}
	if platform == PlatformKiro {
		account.Type = AccountTypeOAuth
		account.Extra = map[string]any{"openai_kiro_bridge_enabled": true}
	} else {
		account.Type = AccountTypeAPIKey
	}
	return account
}

func TestAccountOpenAIKiroBridgeEligibilityRequiresEveryGate(t *testing.T) {
	base := bridgeTestAccount(1, PlatformKiro, 1, 7)
	for _, model := range OpenAIKiroBridgeModels {
		require.True(t, base.IsEligibleForOpenAIKiroBridge(model), model)
	}

	withoutAccountMapping := base
	withoutAccountMapping.Credentials = map[string]any{}
	for _, model := range OpenAIKiroBridgeModels {
		require.True(t, withoutAccountMapping.IsEligibleForOpenAIKiroBridge(model), model)
	}

	customClaudeMapping := base
	customClaudeMapping.Credentials = map[string]any{
		"model_mapping": map[string]any{"claude-sonnet-5": "claude-sonnet-5"},
	}
	for _, model := range OpenAIKiroBridgeModels {
		require.True(t, customClaudeMapping.IsEligibleForOpenAIKiroBridge(model), model)
	}

	tests := []struct {
		name   string
		mutate func(*Account)
	}{
		{name: "account opt-in disabled", mutate: func(a *Account) { a.Extra["openai_kiro_bridge_enabled"] = false }},
		{name: "not OAuth", mutate: func(a *Account) { a.Type = AccountTypeAPIKey }},
		{name: "mapping target differs", mutate: func(a *Account) { a.Credentials["model_mapping"] = map[string]any{OpenAIKiroBridgeModel: "auto"} }},
		{name: "different model", mutate: func(a *Account) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := base
			account.Extra = map[string]any{"openai_kiro_bridge_enabled": true}
			account.Credentials = map[string]any{"model_mapping": map[string]any{OpenAIKiroBridgeModel: OpenAIKiroBridgeModel}}
			tt.mutate(&account)
			model := OpenAIKiroBridgeModel
			if tt.name == "different model" {
				model = "gpt-5.4"
			}
			require.False(t, account.IsEligibleForOpenAIKiroBridge(model))
		})
	}
}

func TestSelectAccountWithSchedulerForKiroBridgeUsesSharedPriority(t *testing.T) {
	groupID := int64(7)
	openAIAccount := bridgeTestAccount(10, PlatformOpenAI, 20, groupID)
	kiroAccount := bridgeTestAccount(20, PlatformKiro, 1, groupID)
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{openAIAccount, kiroAccount}}}
	svc := &OpenAIGatewayService{
		accountRepo: repo,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAIKiroBridgeEnabled: true,
			OpenAIWS: config.GatewayOpenAIWSConfig{
				LBTopK:                1,
				SchedulerScoreWeights: config.GatewayOpenAIWSSchedulerScoreWeights{Priority: 1},
			},
		}},
	}
	candidates, err := svc.listSchedulableAccountsForSchedule(context.Background(), &groupID, PlatformOpenAI, true)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.True(t, svc.isAccountEligibleForOpenAISchedule(context.Background(), &candidates[1], OpenAIAccountScheduleRequest{
		Platform: PlatformOpenAI, RequestedModel: OpenAIKiroBridgeModel, RequiredTransport: OpenAIUpstreamTransportAny,
		RequiredCapability: OpenAIEndpointCapabilityChatCompletions, AllowKiroBridge: true, KiroBridgeModel: OpenAIKiroBridgeModel,
	}))

	var selection *AccountSelectionResult
	for _, model := range OpenAIKiroBridgeModels {
		selection, _, err = svc.SelectAccountWithSchedulerForKiroBridge(
			context.Background(), &groupID, "", model, model, nil,
		)
		require.NoError(t, err, model)
		require.NotNil(t, selection, model)
		require.NotNil(t, selection.Account, model)
		require.Equal(t, kiroAccount.ID, selection.Account.ID, model)
	}

	kiroAccount.Extra["openai_kiro_bridge_enabled"] = false
	repo.accounts = []Account{openAIAccount, kiroAccount}
	svc.accountRepo = repo
	selection, _, err = svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, nil,
	)
	require.NoError(t, err)
	require.Equal(t, openAIAccount.ID, selection.Account.ID)
}

func TestSelectAccountWithSchedulerForKiroBridgeGlobalSwitchFailsClosed(t *testing.T) {
	groupID := int64(8)
	openAIAccount := bridgeTestAccount(11, PlatformOpenAI, 20, groupID)
	kiroAccount := bridgeTestAccount(21, PlatformKiro, 1, groupID)
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{openAIAccount, kiroAccount}}}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}

	selection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, nil,
	)
	require.NoError(t, err)
	require.Equal(t, openAIAccount.ID, selection.Account.ID)
}

func TestRegularOpenAISelectorNeverIncludesKiroBridgeAccounts(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(9)
	kiroAccount := bridgeTestAccount(22, PlatformKiro, 1, groupID)
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{kiroAccount}}}
	svc := &OpenAIGatewayService{
		accountRepo: repo,
		cfg:         &config.Config{Gateway: config.GatewayConfig{OpenAIKiroBridgeEnabled: true}},
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(), &groupID, "", "", OpenAIKiroBridgeModel, nil,
		OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, PlatformOpenAI,
	)
	require.Error(t, err)
	require.Nil(t, selection)
}

func TestSchedulerSnapshotOpenAIMixedBucketIncludesOnlyOptedInKiro(t *testing.T) {
	groupID := int64(10)
	openAIAccount := bridgeTestAccount(12, PlatformOpenAI, 10, groupID)
	kiroEnabled := bridgeTestAccount(23, PlatformKiro, 1, groupID)
	kiroDisabled := bridgeTestAccount(24, PlatformKiro, 2, groupID)
	kiroDisabled.Extra["openai_kiro_bridge_enabled"] = false
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{openAIAccount, kiroEnabled, kiroDisabled}}}
	snapshot := NewSchedulerSnapshotService(nil, nil, repo, nil, nil)

	accounts, err := snapshot.ListSchedulableAccountsWithMixedMode(context.Background(), &groupID, PlatformOpenAI, true)
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	require.ElementsMatch(t, []int64{openAIAccount.ID, kiroEnabled.ID}, []int64{accounts[0].ID, accounts[1].ID})
}

func TestOpenAIKiroBridgeSchedulerSkipsSharedCooldown(t *testing.T) {
	groupID := int64(11)
	openAIAccount := bridgeTestAccount(31, PlatformOpenAI, 20, groupID)
	kiroAccount := bridgeTestAccount(32, PlatformKiro, 1, groupID)
	kiroAccount.Credentials["refresh_token"] = "openai-bridge-cooldown"
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{openAIAccount, kiroAccount}}}
	store := &mappedKiroCooldownStore{states: map[string]*kirocooldown.State{
		buildKiroCooldownKey(&kiroAccount): {
			Active: true, Reason: kirocooldown.CooldownReason429,
			CooldownUntil: time.Now().Add(time.Minute), Remaining: time.Minute,
		},
	}}
	bridge := &GatewayService{
		accountRepo:       repo,
		kiroCooldownStore: store,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
		}},
	}
	svc := &OpenAIGatewayService{
		accountRepo: repo,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAIKiroBridgeEnabled: true,
			OpenAIWS: config.GatewayOpenAIWSConfig{
				LBTopK:                1,
				SchedulerScoreWeights: config.GatewayOpenAIWSSchedulerScoreWeights{Priority: 1},
			},
		}},
	}
	svc.SetKiroBridgeService(bridge)

	selection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, openAIAccount.ID, selection.Account.ID)
}

func TestOpenAIKiroBridgeSchedulerAllCoolingReturnsRetryAfter(t *testing.T) {
	groupID := int64(12)
	kiroAccount := bridgeTestAccount(33, PlatformKiro, 1, groupID)
	kiroAccount.Credentials["refresh_token"] = "openai-bridge-all-cooling"
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{kiroAccount}}}
	store := &mappedKiroCooldownStore{states: map[string]*kirocooldown.State{
		buildKiroCooldownKey(&kiroAccount): {
			Active: true, Reason: kirocooldown.CooldownReason429,
			CooldownUntil: time.Now().Add(45 * time.Second), Remaining: 45 * time.Second,
		},
	}}
	bridge := &GatewayService{
		accountRepo:       repo,
		kiroCooldownStore: store,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
		}},
	}
	svc := &OpenAIGatewayService{
		accountRepo: repo,
		cfg:         &config.Config{Gateway: config.GatewayConfig{OpenAIKiroBridgeEnabled: true}},
	}
	svc.SetKiroBridgeService(bridge)

	selection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, nil,
	)
	require.Nil(t, selection)
	var cooldownErr *KiroCooldownExhaustedError
	require.ErrorAs(t, err, &cooldownErr)
	require.Equal(t, http.StatusTooManyRequests, cooldownErr.StatusCode)
	require.Greater(t, cooldownErr.RetryAfter, 40*time.Second)
}

func TestOpenAIKiroBridgeInitialBindingWaitsForCompleteSuccess(t *testing.T) {
	groupID := int64(16)
	kiroAccount := bridgeTestAccount(63, PlatformKiro, 1, groupID)
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{kiroAccount}}}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{}}
	bridge := &GatewayService{accountRepo: repo, cfg: &config.Config{Gateway: config.GatewayConfig{
		KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
	}}}
	svc := &OpenAIGatewayService{
		accountRepo:        repo,
		cache:              cache,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{kiroAccount.ID: true}}),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAIKiroBridgeEnabled: true,
			OpenAIWS: config.GatewayOpenAIWSConfig{
				LBTopK:                1,
				SchedulerScoreWeights: config.GatewayOpenAIWSSchedulerScoreWeights{Priority: 1},
			},
		}},
	}
	svc.SetKiroBridgeService(bridge)

	selection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "kiro_bridge_initial", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, kiroAccount.ID, selection.Account.ID)
	require.True(t, selection.DeferStickyMigration)
	require.Zero(t, cache.sessionBindings["openai:kiro_bridge_initial"])
	selection.ReleaseFunc()
}

func TestOpenAIKiroBridgeBusyStickyScansIdleAccountAndPreservesBinding(t *testing.T) {
	groupID := int64(13)
	sticky := bridgeTestAccount(34, PlatformKiro, 1, groupID)
	idle := bridgeTestAccount(35, PlatformKiro, 2, groupID)
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{sticky, idle}}}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:kiro_bridge_busy": sticky.ID}}
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{sticky.ID: false, idle.ID: true},
		loadMap: map[int64]*AccountLoadInfo{
			sticky.ID: {AccountID: sticky.ID, LoadRate: 100},
			idle.ID:   {AccountID: idle.ID, LoadRate: 0},
		},
	}
	bridge := &GatewayService{
		accountRepo: repo,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
		}},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        repo,
		cache:              cache,
		concurrencyService: NewConcurrencyService(concurrencyCache),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAIKiroBridgeEnabled: true,
			Scheduling: config.GatewaySchedulingConfig{
				StickySessionWaitTimeout: 30 * time.Second,
				StickySessionMaxWaiting:  10,
			},
		}},
	}
	svc.SetKiroBridgeService(bridge)

	selection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "kiro_bridge_busy", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, idle.ID, selection.Account.ID)
	require.True(t, selection.PreserveStickyBinding)
	require.Equal(t, sticky.ID, cache.sessionBindings["openai:kiro_bridge_busy"])
	selection.ReleaseFunc()
}

func TestOpenAIKiroBridgeCoolingStickyScansIdleAccountAndPreservesBinding(t *testing.T) {
	groupID := int64(14)
	sticky := bridgeTestAccount(44, PlatformKiro, 1, groupID)
	sticky.Credentials["refresh_token"] = "openai-bridge-sticky-cooling"
	idle := bridgeTestAccount(45, PlatformKiro, 2, groupID)
	idle.Credentials["refresh_token"] = "openai-bridge-sticky-idle"
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{sticky, idle}}}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:kiro_bridge_cooling": sticky.ID}}
	store := &mappedKiroCooldownStore{states: map[string]*kirocooldown.State{
		buildKiroCooldownKey(&sticky): {
			Active: true, Reason: kirocooldown.CooldownReason429,
			CooldownUntil: time.Now().Add(time.Minute), Remaining: time.Minute,
		},
	}}
	bridge := &GatewayService{
		accountRepo:       repo,
		kiroCooldownStore: store,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
		}},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        repo,
		cache:              cache,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{idle.ID: true}}),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAIKiroBridgeEnabled: true,
			OpenAIWS: config.GatewayOpenAIWSConfig{
				LBTopK:                1,
				SchedulerScoreWeights: config.GatewayOpenAIWSSchedulerScoreWeights{Priority: 1},
			},
		}},
	}
	svc.SetKiroBridgeService(bridge)

	selection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "kiro_bridge_cooling", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, idle.ID, selection.Account.ID)
	require.False(t, selection.PreserveStickyBinding)
	require.True(t, selection.DeferStickyMigration)
	require.Equal(t, sticky.ID, cache.sessionBindings["openai:kiro_bridge_cooling"])
	selection.ReleaseFunc()
}

func TestOpenAIKiroBridgeFailedStickyDefersCrossPlatformMigration(t *testing.T) {
	groupID := int64(15)
	sticky := bridgeTestAccount(54, PlatformKiro, 1, groupID)
	idle := bridgeTestAccount(55, PlatformOpenAI, 2, groupID)
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{sticky, idle}}}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:kiro_bridge_failed": sticky.ID}}
	bridge := &GatewayService{accountRepo: repo, cfg: &config.Config{Gateway: config.GatewayConfig{
		KiroResilience: config.GatewayKiroResilienceConfig{Mode: config.KiroResilienceModeEnforce},
	}}}
	svc := &OpenAIGatewayService{
		accountRepo:        repo,
		cache:              cache,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{idle.ID: true}}),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAIKiroBridgeEnabled: true,
			OpenAIWS: config.GatewayOpenAIWSConfig{
				LBTopK:                1,
				SchedulerScoreWeights: config.GatewayOpenAIWSSchedulerScoreWeights{Priority: 1},
			},
		}},
	}
	svc.SetKiroBridgeService(bridge)

	selection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "kiro_bridge_failed", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, map[int64]struct{}{sticky.ID: {}},
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, idle.ID, selection.Account.ID)
	require.False(t, selection.PreserveStickyBinding)
	require.True(t, selection.DeferStickyMigration)
	require.Equal(t, sticky.ID, cache.sessionBindings["openai:kiro_bridge_failed"])
	selection.ReleaseFunc()
}

func TestOpenAIRecordUsageForKiroBridgeUsesOpenAIPriceAndKeepsCredits(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		OpenAIKiroBridgeModel: {
			InputCostPerToken:  5e-6,
			OutputCostPerToken: 30e-6,
		},
	}})
	expected, err := svc.billingService.CalculateCost(OpenAIKiroBridgeModel, UsageTokens{InputTokens: 100, OutputTokens: 10}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:    "resp_openai_kiro_bridge",
			Model:        OpenAIKiroBridgeModel,
			BillingModel: OpenAIKiroBridgeModel,
			Usage:        OpenAIUsage{InputTokens: 100, OutputTokens: 10, KiroCredits: 0.17},
			Duration:     time.Second,
		},
		APIKey: &APIKey{ID: 100},
		User:   &User{ID: 200},
		Account: &Account{
			ID: 300, Platform: PlatformKiro, Type: AccountTypeOAuth,
			Extra: map[string]any{"kiro_credit_unit_price_usd": 999.0},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, expected.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.lastLog.KiroCredits)
	require.InDelta(t, 0.17, *usageRepo.lastLog.KiroCredits, 1e-9)
}
