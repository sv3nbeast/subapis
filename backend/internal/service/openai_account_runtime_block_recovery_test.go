package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type grokRuntimeRecoveryAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (r *grokRuntimeRecoveryAccountRepo) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Account, error) {
	accounts := make([]Account, len(r.accounts))
	copy(accounts, r.accounts)
	return accounts, nil
}

func (r *grokRuntimeRecoveryAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	for _, account := range r.accounts {
		if account.ID != id {
			continue
		}
		copy := account
		return &copy, nil
	}
	return nil, nil
}

func TestGrokRuntimeBlock_ReconcilesAfterBridgeWindowWhenPersistedStateRecovered(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{
		ID:          48,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
	}

	svc.openaiAccountRuntimeBlockUntil.Store(account.ID, openAIAccountRuntimeBlock{
		Until:     time.Now().Add(time.Hour),
		StartedAt: time.Now().Add(-openAIStopSchedulingBridgeCooldown - time.Second),
		Reason:    "429",
	})

	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	_, blocked := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.False(t, blocked)
}

func TestGrokRuntimeBlock_KeepsPersistedFutureCooldown(t *testing.T) {
	svc := &OpenAIGatewayService{}
	resetAt := time.Now().Add(time.Hour)
	account := &Account{
		ID:               49,
		Platform:         PlatformGrok,
		Type:             AccountTypeOAuth,
		Status:           StatusActive,
		Schedulable:      true,
		RateLimitResetAt: &resetAt,
	}

	svc.openaiAccountRuntimeBlockUntil.Store(account.ID, openAIAccountRuntimeBlock{
		Until:     resetAt,
		StartedAt: time.Now().Add(-openAIStopSchedulingBridgeCooldown - time.Second),
		Reason:    "429",
	})

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestGrokRuntimeBlock_KeepsBridgeForRecentUnpersistedBlock(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{
		ID:          50,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
	}

	svc.BlockAccountScheduling(account, time.Now().Add(time.Hour), "429")

	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestGrokRuntimeBlock_RecoveredAccountReturnsToLegacyScheduler(t *testing.T) {
	groupID := int64(32)
	account := Account{
		ID:          51,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"base_url": "https://cli-chat-proxy.grok.com/v1"},
	}
	svc := &OpenAIGatewayService{
		accountRepo: &grokRuntimeRecoveryAccountRepo{accounts: []Account{account}},
		cfg:         &config.Config{},
	}
	svc.openaiAccountRuntimeBlockUntil.Store(account.ID, openAIAccountRuntimeBlock{
		Until:     time.Now().Add(time.Hour),
		StartedAt: time.Now().Add(-openAIStopSchedulingBridgeCooldown - time.Second),
		Reason:    "429",
	})

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"grok-4.5",
		nil,
		OpenAIUpstreamTransportAny,
		OpenAIEndpointCapabilityChatCompletions,
		false,
		false,
		PlatformGrok,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, account.ID, selection.Account.ID)
}

func TestGrokRuntimeBlock_MixedCLIAndAPIKeyPoolSelectsRecoveredCLI(t *testing.T) {
	groupID := int64(32)
	cliAccount := Account{
		ID:          52,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    0,
		Credentials: map[string]any{"base_url": "https://cli-chat-proxy.grok.com/v1"},
	}
	apiKeyAccount := Account{
		ID:          53,
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    1,
		Credentials: map[string]any{"base_url": "https://api.x.ai/v1", "api_key": "test-key"},
	}
	repo := &grokRuntimeRecoveryAccountRepo{accounts: []Account{cliAccount, apiKeyAccount}}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}
	svc.openaiAccountRuntimeBlockUntil.Store(cliAccount.ID, openAIAccountRuntimeBlock{
		Until:     time.Now().Add(time.Hour),
		StartedAt: time.Now().Add(-openAIStopSchedulingBridgeCooldown - time.Second),
		Reason:    "429",
	})

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"grok-4.5",
		nil,
		OpenAIUpstreamTransportAny,
		OpenAIEndpointCapabilityChatCompletions,
		false,
		false,
		PlatformGrok,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, cliAccount.ID, selection.Account.ID)
}

func TestGrokScheduler_MixedPoolSkipsKnownExhaustedCLI(t *testing.T) {
	groupID := int64(32)
	limit, remaining := int64(2_000_000), int64(0)
	resetAt := time.Now().Add(24 * time.Hour).Unix()
	cliAccount := Account{
		ID:          54,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    0,
		Credentials: map[string]any{"base_url": "https://cli-chat-proxy.grok.com/v1"},
		Extra: map[string]any{
			grokQuotaSnapshotExtraKey: xai.QuotaSnapshot{
				Tokens:    &xai.QuotaWindow{Limit: &limit, Remaining: &remaining, ResetUnix: &resetAt},
				UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	apiKeyAccount := Account{
		ID:          55,
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    1,
		Credentials: map[string]any{"base_url": "https://api.x.ai/v1", "api_key": "test-key"},
	}
	repo := &grokRuntimeRecoveryAccountRepo{accounts: []Account{cliAccount, apiKeyAccount}}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"grok-4.5",
		nil,
		OpenAIUpstreamTransportAny,
		OpenAIEndpointCapabilityChatCompletions,
		false,
		false,
		PlatformGrok,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, apiKeyAccount.ID, selection.Account.ID)
}
