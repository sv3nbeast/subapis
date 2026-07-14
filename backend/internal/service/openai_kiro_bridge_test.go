package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
			"model_mapping": map[string]any{OpenAIKiroBridgeModel: OpenAIKiroBridgeModel},
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
	require.True(t, base.IsEligibleForOpenAIKiroBridge(OpenAIKiroBridgeModel))

	tests := []struct {
		name   string
		mutate func(*Account)
	}{
		{name: "account opt-in disabled", mutate: func(a *Account) { a.Extra["openai_kiro_bridge_enabled"] = false }},
		{name: "not OAuth", mutate: func(a *Account) { a.Type = AccountTypeAPIKey }},
		{name: "mapping missing", mutate: func(a *Account) { delete(a.Credentials, "model_mapping") }},
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

	selection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, kiroAccount.ID, selection.Account.ID)

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
