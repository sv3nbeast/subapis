//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type scheduledTestPlanRepoStub struct {
	deletedIDs []int64
	updatedIDs []int64
}

func (r *scheduledTestPlanRepoStub) Create(context.Context, *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}

func (r *scheduledTestPlanRepoStub) GetByID(context.Context, int64) (*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}

func (r *scheduledTestPlanRepoStub) ListByAccountID(context.Context, int64) ([]*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}

func (r *scheduledTestPlanRepoStub) ListDue(context.Context, time.Time) ([]*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}

func (r *scheduledTestPlanRepoStub) Update(context.Context, *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}

func (r *scheduledTestPlanRepoStub) Delete(_ context.Context, id int64) error {
	r.deletedIDs = append(r.deletedIDs, id)
	return nil
}

func (r *scheduledTestPlanRepoStub) UpdateAfterRun(_ context.Context, id int64, _ time.Time, _ time.Time) error {
	r.updatedIDs = append(r.updatedIDs, id)
	return nil
}

type scheduledTestResultRepoStub struct {
	createdPlanIDs []int64
	prunedPlanIDs  []int64
}

func (r *scheduledTestResultRepoStub) Create(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	r.createdPlanIDs = append(r.createdPlanIDs, result.PlanID)
	return result, nil
}

func (r *scheduledTestResultRepoStub) ListByPlanID(context.Context, int64, int) ([]*ScheduledTestResult, error) {
	return nil, errors.New("not implemented")
}

func (r *scheduledTestResultRepoStub) PruneOldResults(_ context.Context, planID int64, _ int) error {
	r.prunedPlanIDs = append(r.prunedPlanIDs, planID)
	return nil
}

type missingAccountRepoStub struct {
	AccountRepository
}

func (r *missingAccountRepoStub) GetByID(context.Context, int64) (*Account, error) {
	return nil, errors.New("missing")
}

type scheduledTestAccountRepoStub struct {
	AccountRepository
	accounts map[int64]*Account
}

func (r *scheduledTestAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	account, ok := r.accounts[id]
	if !ok {
		return nil, errors.New("missing")
	}
	cloned := *account
	return &cloned, nil
}

func newScheduledTestAntigravityAccount(id int64) *Account {
	return &Account{
		ID:          id,
		Name:        "scheduled-test-account",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra:       map[string]any{},
	}
}

func TestScheduledTestRunnerService_RunOnePlan_DeletesStalePlanOnMissingAccount(t *testing.T) {
	planRepo := &scheduledTestPlanRepoStub{}
	resultRepo := &scheduledTestResultRepoStub{}
	runner := &ScheduledTestRunnerService{
		planRepo:     planRepo,
		scheduledSvc: NewScheduledTestService(planRepo, resultRepo),
		accountTestSvc: &AccountTestService{
			accountRepo: &missingAccountRepoStub{},
		},
	}

	plan := &ScheduledTestPlan{
		ID:             42,
		AccountID:      999,
		ModelID:        "claude-opus-4-6-thinking",
		CronExpression: "*/10 * * * *",
		MaxResults:     50,
	}

	runner.runOnePlan(context.Background(), plan)

	require.Equal(t, []int64{42}, planRepo.deletedIDs)
	require.Empty(t, planRepo.updatedIDs)
	require.Empty(t, resultRepo.createdPlanIDs)
	require.Empty(t, resultRepo.prunedPlanIDs)
}

func TestScheduledTestRunnerService_RunOnePlan_SkipsAutoRecoverProbeWithoutRecoverableState(t *testing.T) {
	planRepo := &scheduledTestPlanRepoStub{}
	resultRepo := &scheduledTestResultRepoStub{}
	accountRepo := &scheduledTestAccountRepoStub{
		accounts: map[int64]*Account{
			1001: newScheduledTestAntigravityAccount(1001),
		},
	}
	runner := &ScheduledTestRunnerService{
		planRepo:     planRepo,
		scheduledSvc: NewScheduledTestService(planRepo, resultRepo),
		accountTestSvc: &AccountTestService{
			accountRepo: accountRepo,
		},
	}

	plan := &ScheduledTestPlan{
		ID:             51,
		AccountID:      1001,
		ModelID:        "claude-opus-4-6-thinking",
		CronExpression: "*/10 * * * *",
		MaxResults:     50,
		AutoRecover:    true,
	}

	runner.runOnePlan(context.Background(), plan)

	require.Equal(t, []int64{51}, planRepo.updatedIDs)
	require.Empty(t, planRepo.deletedIDs)
	require.Empty(t, resultRepo.createdPlanIDs)
	require.Empty(t, resultRepo.prunedPlanIDs)
}

func TestScheduledTestRunnerService_RunOnePlan_AutoRecoverProbeRunsForRecoverableState(t *testing.T) {
	resetAt := time.Now().Add(10 * time.Minute)
	account := newScheduledTestAntigravityAccount(1002)
	account.RateLimitResetAt = &resetAt

	planRepo := &scheduledTestPlanRepoStub{}
	resultRepo := &scheduledTestResultRepoStub{}
	accountRepo := &scheduledTestAccountRepoStub{
		accounts: map[int64]*Account{
			1002: account,
		},
	}
	runner := &ScheduledTestRunnerService{
		planRepo:     planRepo,
		scheduledSvc: NewScheduledTestService(planRepo, resultRepo),
		accountTestSvc: &AccountTestService{
			accountRepo: accountRepo,
		},
	}

	plan := &ScheduledTestPlan{
		ID:             52,
		AccountID:      1002,
		ModelID:        "claude-opus-4-6-thinking",
		CronExpression: "*/10 * * * *",
		MaxResults:     50,
		AutoRecover:    true,
	}

	runner.runOnePlan(context.Background(), plan)

	require.Equal(t, []int64{52}, planRepo.updatedIDs)
	require.Empty(t, planRepo.deletedIDs)
	require.Equal(t, []int64{52}, resultRepo.createdPlanIDs)
	require.Equal(t, []int64{52}, resultRepo.prunedPlanIDs)
}

func TestScheduledTestRunnerService_RunOnePlan_AutoRecoverProbeRunsForErrorState(t *testing.T) {
	account := newScheduledTestAntigravityAccount(1003)
	account.Status = StatusError
	account.ErrorMessage = "temporary upstream auth error"

	planRepo := &scheduledTestPlanRepoStub{}
	resultRepo := &scheduledTestResultRepoStub{}
	accountRepo := &scheduledTestAccountRepoStub{
		accounts: map[int64]*Account{
			1003: account,
		},
	}
	runner := &ScheduledTestRunnerService{
		planRepo:     planRepo,
		scheduledSvc: NewScheduledTestService(planRepo, resultRepo),
		accountTestSvc: &AccountTestService{
			accountRepo: accountRepo,
		},
	}

	plan := &ScheduledTestPlan{
		ID:             53,
		AccountID:      1003,
		ModelID:        "claude-opus-4-6-thinking",
		CronExpression: "*/10 * * * *",
		MaxResults:     50,
		AutoRecover:    true,
	}

	runner.runOnePlan(context.Background(), plan)

	require.Equal(t, []int64{53}, planRepo.updatedIDs)
	require.Empty(t, planRepo.deletedIDs)
	require.Equal(t, []int64{53}, resultRepo.createdPlanIDs)
	require.Equal(t, []int64{53}, resultRepo.prunedPlanIDs)
}

func TestScheduledTestRunnerService_RunOnePlan_NonAutoRecoverPlanStillProbes(t *testing.T) {
	planRepo := &scheduledTestPlanRepoStub{}
	resultRepo := &scheduledTestResultRepoStub{}
	accountRepo := &scheduledTestAccountRepoStub{
		accounts: map[int64]*Account{
			1004: newScheduledTestAntigravityAccount(1004),
		},
	}
	runner := &ScheduledTestRunnerService{
		planRepo:     planRepo,
		scheduledSvc: NewScheduledTestService(planRepo, resultRepo),
		accountTestSvc: &AccountTestService{
			accountRepo: accountRepo,
		},
	}

	plan := &ScheduledTestPlan{
		ID:             54,
		AccountID:      1004,
		ModelID:        "claude-opus-4-6-thinking",
		CronExpression: "*/10 * * * *",
		MaxResults:     50,
		AutoRecover:    false,
	}

	runner.runOnePlan(context.Background(), plan)

	require.Equal(t, []int64{54}, planRepo.updatedIDs)
	require.Empty(t, planRepo.deletedIDs)
	require.Equal(t, []int64{54}, resultRepo.createdPlanIDs)
	require.Equal(t, []int64{54}, resultRepo.prunedPlanIDs)
}
