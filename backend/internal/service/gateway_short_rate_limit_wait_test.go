//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type shortRateLimitWaitRepo struct {
	mockAccountRepoForPlatform
}

func (r *shortRateLimitWaitRepo) ListByGroup(context.Context, int64) ([]Account, error) {
	return r.accounts, nil
}

func TestShortRetryWaitForRateLimitedAccounts_WaitsForSoonRecoveringAnthropicAccount(t *testing.T) {
	groupID := int64(11)
	resetAt := time.Now().Add(120 * time.Millisecond)
	repo := &shortRateLimitWaitRepo{
		mockAccountRepoForPlatform: mockAccountRepoForPlatform{accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, RateLimitResetAt: &resetAt},
			{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, RateLimitResetAt: nil},
		}},
	}
	svc := &GatewayService{
		accountRepo: repo,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	wait := svc.shortRetryWaitForRateLimitedAccounts(context.Background(), &groupID, PlatformAnthropic, false, "claude-opus-4-7", map[int64]struct{}{2: {}})

	require.Greater(t, wait, 0*time.Millisecond)
	require.LessOrEqual(t, wait, 500*time.Millisecond)
}

func TestShortRetryWaitForRateLimitedAccounts_SkipsLongCooldown(t *testing.T) {
	groupID := int64(11)
	resetAt := time.Now().Add(35 * time.Second)
	repo := &shortRateLimitWaitRepo{
		mockAccountRepoForPlatform: mockAccountRepoForPlatform{accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, RateLimitResetAt: &resetAt},
		}},
	}
	svc := &GatewayService{
		accountRepo: repo,
		cfg:         &config.Config{RunMode: config.RunModeStandard},
	}

	wait := svc.shortRetryWaitForRateLimitedAccounts(context.Background(), &groupID, PlatformAnthropic, false, "claude-opus-4-7", nil)

	require.Zero(t, wait)
}

func TestSelectAccountWithLoadAwareness_WaitsForEmptyPoolShortRateLimit(t *testing.T) {
	groupID := int64(11)
	resetAt := time.Now().Add(40 * time.Millisecond)
	repo := &shortRateLimitWaitRepo{
		mockAccountRepoForPlatform: mockAccountRepoForPlatform{accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, RateLimitResetAt: &resetAt},
		}},
	}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*Group{
			groupID: {ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true},
		},
	}
	svc := &GatewayService{
		accountRepo:        repo,
		groupRepo:          groupRepo,
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(&mockConcurrencyCache{}),
	}

	result, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "claude-opus-4-7", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Account)
	require.Equal(t, int64(1), result.Account.ID)
}
