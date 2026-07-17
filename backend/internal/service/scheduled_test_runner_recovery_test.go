package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type scheduledRecoveryAccountRepo struct {
	AccountRepository
	account         *Account
	getByIDCalls    int
	clearErrorCalls int
}

func (r *scheduledRecoveryAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	r.getByIDCalls++
	cloned := *r.account
	return &cloned, nil
}

func (r *scheduledRecoveryAccountRepo) ClearError(context.Context, int64) error {
	r.clearErrorCalls++
	r.account.Status = StatusActive
	r.account.Schedulable = true
	r.account.ErrorMessage = ""
	return nil
}

func TestScheduledTestRunnerService_SuccessfulKiroProbeClearsErrorState(t *testing.T) {
	repo := &scheduledRecoveryAccountRepo{
		account: &Account{
			ID:           1659,
			Platform:     PlatformKiro,
			Type:         AccountTypeOAuth,
			Status:       StatusError,
			Schedulable:  false,
			ErrorMessage: "Access forbidden (403): The bearer token included in the request is invalid.",
		},
	}
	runner := &ScheduledTestRunnerService{
		rateLimitSvc: NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
	}

	// runOnePlan calls tryRecoverAccount only after a successful scheduled probe
	// when auto_recover is enabled. The recovery must clear Kiro's error state.
	runner.tryRecoverAccount(context.Background(), 1659, 780)

	require.Equal(t, 1, repo.getByIDCalls)
	require.Equal(t, 1, repo.clearErrorCalls)
	require.Equal(t, StatusActive, repo.account.Status)
	require.True(t, repo.account.Schedulable)
	require.Empty(t, repo.account.ErrorMessage)
}
