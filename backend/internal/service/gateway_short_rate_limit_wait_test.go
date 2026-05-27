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
	resetAt := time.Now().Add(10 * time.Second)
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
