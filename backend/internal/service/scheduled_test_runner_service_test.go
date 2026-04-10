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
