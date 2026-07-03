package service

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

type groupRepoNoop struct{}

func (groupRepoNoop) Create(context.Context, *Group) error { panic("unexpected Create call") }
func (groupRepoNoop) GetByID(context.Context, int64) (*Group, error) {
	panic("unexpected GetByID call")
}
func (groupRepoNoop) GetByIDLite(context.Context, int64) (*Group, error) {
	panic("unexpected GetByIDLite call")
}
func (groupRepoNoop) Update(context.Context, *Group) error { panic("unexpected Update call") }
func (groupRepoNoop) Delete(context.Context, int64) error  { panic("unexpected Delete call") }
func (groupRepoNoop) DeleteCascade(context.Context, int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}
func (groupRepoNoop) List(context.Context, pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}
func (groupRepoNoop) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}
func (groupRepoNoop) ListActive(context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}
func (groupRepoNoop) ListActiveByPlatform(context.Context, string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}
func (groupRepoNoop) ExistsByName(context.Context, string) (bool, error) {
	panic("unexpected ExistsByName call")
}
func (groupRepoNoop) GetAccountCount(context.Context, int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}
func (groupRepoNoop) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}
func (groupRepoNoop) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}
func (groupRepoNoop) BindAccountsToGroup(context.Context, int64, []int64) error {
	panic("unexpected BindAccountsToGroup call")
}
func (groupRepoNoop) UpdateSortOrders(context.Context, []GroupSortOrderUpdate) error {
	panic("unexpected UpdateSortOrders call")
}

type subscriptionGroupRepoStub struct {
	groupRepoNoop
	group *Group
}

func (s *subscriptionGroupRepoStub) GetByID(context.Context, int64) (*Group, error) {
	return s.group, nil
}

type userSubRepoNoop struct{}

func (userSubRepoNoop) Create(context.Context, *UserSubscription) error {
	panic("unexpected Create call")
}
func (userSubRepoNoop) GetByID(context.Context, int64) (*UserSubscription, error) {
	panic("unexpected GetByID call")
}
func (userSubRepoNoop) GetByIDIncludeDeleted(context.Context, int64) (*UserSubscription, error) {
	panic("unexpected GetByIDIncludeDeleted call")
}
func (userSubRepoNoop) GetByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	panic("unexpected GetByUserIDAndGroupID call")
}
func (userSubRepoNoop) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	panic("unexpected GetActiveByUserIDAndGroupID call")
}
func (userSubRepoNoop) Update(context.Context, *UserSubscription) error {
	panic("unexpected Update call")
}
func (userSubRepoNoop) Delete(context.Context, int64) error { panic("unexpected Delete call") }
func (userSubRepoNoop) Restore(context.Context, int64, string) (*UserSubscription, error) {
	panic("unexpected Restore call")
}
func (userSubRepoNoop) ListByUserID(context.Context, int64) ([]UserSubscription, error) {
	panic("unexpected ListByUserID call")
}
func (userSubRepoNoop) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	panic("unexpected ListActiveByUserID call")
}
func (userSubRepoNoop) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}
func (userSubRepoNoop) List(context.Context, pagination.PaginationParams, *int64, *int64, string, string, string, string) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}
func (userSubRepoNoop) ExistsByUserIDAndGroupID(context.Context, int64, int64) (bool, error) {
	panic("unexpected ExistsByUserIDAndGroupID call")
}
func (userSubRepoNoop) ExistsActiveByUserIDAndGroupID(context.Context, int64, int64) (bool, error) {
	panic("unexpected ExistsActiveByUserIDAndGroupID call")
}
func (userSubRepoNoop) ExtendExpiry(context.Context, int64, time.Time) error {
	panic("unexpected ExtendExpiry call")
}
func (userSubRepoNoop) UpdateStatus(context.Context, int64, string) error {
	panic("unexpected UpdateStatus call")
}
func (userSubRepoNoop) UpdateNotes(context.Context, int64, string) error {
	panic("unexpected UpdateNotes call")
}
func (userSubRepoNoop) SetQuotaCycle(context.Context, int64, time.Time, time.Time, int) error {
	panic("unexpected SetQuotaCycle call")
}
func (userSubRepoNoop) ResetUsageForQuotaCycle(context.Context, int64, time.Time, time.Time, time.Time, int) error {
	panic("unexpected ResetUsageForQuotaCycle call")
}
func (userSubRepoNoop) ActivateWindows(context.Context, int64, time.Time) error {
	panic("unexpected ActivateWindows call")
}
func (userSubRepoNoop) ResetDailyUsage(context.Context, int64, time.Time) error {
	panic("unexpected ResetDailyUsage call")
}
func (userSubRepoNoop) ResetWeeklyUsage(context.Context, int64, time.Time) error {
	panic("unexpected ResetWeeklyUsage call")
}
func (userSubRepoNoop) ResetMonthlyUsage(context.Context, int64, time.Time) error {
	panic("unexpected ResetMonthlyUsage call")
}
func (userSubRepoNoop) IncrementUsage(context.Context, int64, float64) error {
	panic("unexpected IncrementUsage call")
}
func (userSubRepoNoop) BatchUpdateExpiredStatus(context.Context) (int64, error) {
	panic("unexpected BatchUpdateExpiredStatus call")
}

type subscriptionUserSubRepoStub struct {
	userSubRepoNoop

	nextID            int64
	byID              map[int64]*UserSubscription
	byUserGroup       map[string]*UserSubscription
	createCalls       int
	resetDailyCalls   int
	resetWeeklyCalls  int
	resetMonthlyCalls int
}

func newSubscriptionUserSubRepoStub() *subscriptionUserSubRepoStub {
	return &subscriptionUserSubRepoStub{
		nextID:      1,
		byID:        make(map[int64]*UserSubscription),
		byUserGroup: make(map[string]*UserSubscription),
	}
}

func (s *subscriptionUserSubRepoStub) key(userID, groupID int64) string {
	return strconvFormatInt(userID) + ":" + strconvFormatInt(groupID)
}

func (s *subscriptionUserSubRepoStub) seed(sub *UserSubscription) {
	if sub == nil {
		return
	}
	cp := *sub
	if cp.ID == 0 {
		cp.ID = s.nextID
		s.nextID++
	}
	s.byID[cp.ID] = &cp
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
}

func (s *subscriptionUserSubRepoStub) ExistsByUserIDAndGroupID(_ context.Context, userID, groupID int64) (bool, error) {
	_, ok := s.byUserGroup[s.key(userID, groupID)]
	return ok, nil
}

func (s *subscriptionUserSubRepoStub) GetByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*UserSubscription, error) {
	sub := s.byUserGroup[s.key(userID, groupID)]
	if sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	cp := *sub
	return &cp, nil
}

func (s *subscriptionUserSubRepoStub) Create(_ context.Context, sub *UserSubscription) error {
	if sub == nil {
		return nil
	}
	s.createCalls++
	cp := *sub
	if cp.ID == 0 {
		cp.ID = s.nextID
		s.nextID++
	}
	sub.ID = cp.ID
	s.byID[cp.ID] = &cp
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
	return nil
}

func (s *subscriptionUserSubRepoStub) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	sub := s.byID[id]
	if sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	cp := *sub
	return &cp, nil
}

func (s *subscriptionUserSubRepoStub) ExtendExpiry(_ context.Context, id int64, expiresAt time.Time) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.ExpiresAt = expiresAt
	sub.UpdatedAt = time.Now()
	return nil
}

func (s *subscriptionUserSubRepoStub) Update(_ context.Context, sub *UserSubscription) error {
	if sub == nil {
		return ErrSubscriptionNilInput
	}
	existing := s.byID[sub.ID]
	if existing == nil {
		return ErrSubscriptionNotFound
	}
	cp := *sub
	cp.UpdatedAt = time.Now()
	s.byID[cp.ID] = &cp
	s.byUserGroup[s.key(cp.UserID, cp.GroupID)] = &cp
	return nil
}

func (s *subscriptionUserSubRepoStub) UpdateStatus(_ context.Context, id int64, status string) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.Status = status
	sub.UpdatedAt = time.Now()
	return nil
}

func (s *subscriptionUserSubRepoStub) UpdateNotes(_ context.Context, id int64, notes string) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.Notes = notes
	sub.UpdatedAt = time.Now()
	return nil
}

func (s *subscriptionUserSubRepoStub) SetQuotaCycle(_ context.Context, id int64, startAt, endAt time.Time, cycleDays int) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.QuotaCycleStartAt = &startAt
	sub.QuotaCycleEndAt = &endAt
	sub.QuotaCycleDays = cycleDays
	sub.UpdatedAt = time.Now()
	return nil
}

func (s *subscriptionUserSubRepoStub) ResetUsageForQuotaCycle(_ context.Context, id int64, windowStart, cycleStartAt, cycleEndAt time.Time, cycleDays int) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.DailyUsageUSD = 0
	sub.WeeklyUsageUSD = 0
	sub.MonthlyUsageUSD = 0
	sub.DailyWindowStart = &windowStart
	sub.WeeklyWindowStart = &windowStart
	sub.MonthlyWindowStart = &windowStart
	sub.QuotaCycleStartAt = &cycleStartAt
	sub.QuotaCycleEndAt = &cycleEndAt
	sub.QuotaCycleDays = cycleDays
	sub.UpdatedAt = time.Now()
	return nil
}

func (s *subscriptionUserSubRepoStub) ResetDailyUsage(_ context.Context, id int64, windowStart time.Time) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	s.resetDailyCalls++
	sub.DailyUsageUSD = 0
	sub.DailyWindowStart = &windowStart
	sub.UpdatedAt = time.Now()
	return nil
}

func (s *subscriptionUserSubRepoStub) ResetWeeklyUsage(_ context.Context, id int64, windowStart time.Time) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	s.resetWeeklyCalls++
	sub.WeeklyUsageUSD = 0
	sub.WeeklyWindowStart = &windowStart
	sub.UpdatedAt = time.Now()
	return nil
}

func (s *subscriptionUserSubRepoStub) ResetMonthlyUsage(_ context.Context, id int64, windowStart time.Time) error {
	sub := s.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	s.resetMonthlyCalls++
	sub.MonthlyUsageUSD = 0
	sub.MonthlyWindowStart = &windowStart
	sub.UpdatedAt = time.Now()
	return nil
}

func newSubscriptionAssignTestEntClient(t *testing.T) *dbent.Client {
	t.Helper()

	db, err := sql.Open("sqlite", "file:subscription_assign?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestAssignSubscriptionReuseWhenSemanticsMatch(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	subRepo.seed(&UserSubscription{
		ID:        10,
		UserID:    1001,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 30),
		Notes:     "init",
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	sub, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       1001,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "init",
	})
	require.NoError(t, err)
	require.Equal(t, int64(10), sub.ID)
	require.Equal(t, 0, subRepo.createCalls, "reuse should not create new subscription")
}

func TestAssignSubscriptionConflictWhenSemanticsMismatch(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	subRepo.seed(&UserSubscription{
		ID:        11,
		UserID:    2001,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 30),
		Notes:     "old-note",
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	_, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       2001,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "new-note",
	})
	require.Error(t, err)
	require.Equal(t, "SUBSCRIPTION_ASSIGN_CONFLICT", infraerrorsReason(err))
	require.Equal(t, 0, subRepo.createCalls, "conflict should not create or mutate existing subscription")
}

func TestAssignOrExtendSubscriptionExpiredRenewalResetsUsage(t *testing.T) {
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	expiredAt := time.Now().Add(-time.Hour)
	oldDailyWindow := time.Now().Add(-48 * time.Hour)
	oldWeeklyWindow := time.Now().Add(-8 * 24 * time.Hour)
	oldMonthlyWindow := time.Now().Add(-31 * 24 * time.Hour)
	subRepo.seed(&UserSubscription{
		ID:                 31,
		UserID:             3001,
		GroupID:            1,
		StartsAt:           expiredAt.AddDate(0, 0, -30),
		ExpiresAt:          expiredAt,
		Status:             SubscriptionStatusExpired,
		DailyWindowStart:   &oldDailyWindow,
		WeeklyWindowStart:  &oldWeeklyWindow,
		MonthlyWindowStart: &oldMonthlyWindow,
		DailyUsageUSD:      12.3,
		WeeklyUsageUSD:     123.4,
		MonthlyUsageUSD:    456.7,
		Notes:              "old",
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, newSubscriptionAssignTestEntClient(t), nil)
	sub, renewed, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       3001,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "payment order 1",
	})

	require.NoError(t, err)
	require.True(t, renewed)
	require.Equal(t, SubscriptionStatusActive, sub.Status)
	require.Zero(t, sub.DailyUsageUSD)
	require.Zero(t, sub.WeeklyUsageUSD)
	require.Zero(t, sub.MonthlyUsageUSD)
	require.Equal(t, 1, subRepo.resetDailyCalls)
	require.Equal(t, 1, subRepo.resetWeeklyCalls)
	require.Equal(t, 1, subRepo.resetMonthlyCalls)
	require.NotNil(t, sub.DailyWindowStart)
	require.NotNil(t, sub.WeeklyWindowStart)
	require.NotNil(t, sub.MonthlyWindowStart)
	require.Equal(t, sub.DailyWindowStart, sub.WeeklyWindowStart)
	require.Equal(t, sub.DailyWindowStart, sub.MonthlyWindowStart)
	require.Contains(t, sub.Notes, "payment order 1")
	require.True(t, sub.ExpiresAt.After(time.Now()))
}

func TestAssignOrExtendSubscriptionActiveRenewalKeepsUsage(t *testing.T) {
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	expiresAt := time.Now().Add(24 * time.Hour)
	cycleStart := expiresAt.AddDate(0, 0, -30)
	cycleEnd := expiresAt
	subRepo.seed(&UserSubscription{
		ID:                32,
		UserID:            3002,
		GroupID:           1,
		StartsAt:          cycleStart,
		ExpiresAt:         expiresAt,
		Status:            SubscriptionStatusActive,
		QuotaCycleStartAt: &cycleStart,
		QuotaCycleEndAt:   &cycleEnd,
		QuotaCycleDays:    30,
		DailyUsageUSD:     12.3,
		WeeklyUsageUSD:    123.4,
		MonthlyUsageUSD:   456.7,
		Notes:             "old",
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, newSubscriptionAssignTestEntClient(t), nil)
	sub, renewed, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       3002,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "payment order 2",
	})

	require.NoError(t, err)
	require.True(t, renewed)
	require.Equal(t, 12.3, sub.DailyUsageUSD)
	require.Equal(t, 123.4, sub.WeeklyUsageUSD)
	require.Equal(t, 456.7, sub.MonthlyUsageUSD)
	require.NotNil(t, sub.QuotaCycleEndAt)
	require.WithinDuration(t, cycleEnd, *sub.QuotaCycleEndAt, time.Microsecond)
	require.Zero(t, subRepo.resetDailyCalls)
	require.Zero(t, subRepo.resetWeeklyCalls)
	require.Zero(t, subRepo.resetMonthlyCalls)
	require.True(t, sub.ExpiresAt.After(expiresAt))
}

func TestAssignOrExtendSubscriptionActiveRenewalKeepsExpiredMonthlyWindowForAutomaticReset(t *testing.T) {
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	expiresAt := time.Now().Add(24 * time.Hour)
	oldMonthlyWindow := time.Now().Add(-31 * 24 * time.Hour)
	cycleStart := expiresAt.AddDate(0, 0, -30)
	cycleEnd := expiresAt
	subRepo.seed(&UserSubscription{
		ID:                 33,
		UserID:             3003,
		GroupID:            1,
		StartsAt:           cycleStart,
		ExpiresAt:          expiresAt,
		Status:             SubscriptionStatusActive,
		MonthlyWindowStart: &oldMonthlyWindow,
		QuotaCycleStartAt:  &cycleStart,
		QuotaCycleEndAt:    &cycleEnd,
		QuotaCycleDays:     30,
		MonthlyUsageUSD:    456.7,
		Notes:              "old",
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, newSubscriptionAssignTestEntClient(t), nil)
	sub, renewed, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       3003,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "payment order 3",
	})

	require.NoError(t, err)
	require.True(t, renewed)
	require.Equal(t, 456.7, sub.MonthlyUsageUSD)
	require.NotNil(t, sub.MonthlyWindowStart)
	require.WithinDuration(t, oldMonthlyWindow, *sub.MonthlyWindowStart, time.Microsecond)
	require.Zero(t, subRepo.resetMonthlyCalls)
	require.True(t, sub.NeedsMonthlyReset(), "active renewal should not consume the pending automatic 30-day reset")
	require.NotNil(t, sub.QuotaCycleEndAt)
	require.WithinDuration(t, cycleEnd, *sub.QuotaCycleEndAt, time.Microsecond)
	require.True(t, sub.ExpiresAt.After(expiresAt))
}

func TestExtendSubscriptionShortenShrinksQuotaCycleBoundary(t *testing.T) {
	subRepo := newSubscriptionUserSubRepoStub()
	expiresAt := time.Now().Add(10 * 24 * time.Hour)
	cycleStart := time.Now().Add(-20 * 24 * time.Hour)
	cycleEnd := expiresAt
	subRepo.seed(&UserSubscription{
		ID:                34,
		UserID:            3004,
		GroupID:           1,
		StartsAt:          cycleStart,
		ExpiresAt:         expiresAt,
		Status:            SubscriptionStatusActive,
		QuotaCycleStartAt: &cycleStart,
		QuotaCycleEndAt:   &cycleEnd,
		QuotaCycleDays:    30,
	})

	svc := NewSubscriptionService(groupRepoNoop{}, subRepo, nil, newSubscriptionAssignTestEntClient(t), nil)
	sub, err := svc.ExtendSubscription(context.Background(), 34, -5)

	require.NoError(t, err)
	require.NotNil(t, sub.QuotaCycleEndAt)
	require.WithinDuration(t, sub.ExpiresAt, *sub.QuotaCycleEndAt, time.Microsecond)
	require.True(t, sub.ExpiresAt.Before(expiresAt))
}

func TestEnsureWindowMaintenanceResetsExpiredQuotaCycleAfterActiveRenewal(t *testing.T) {
	subRepo := newSubscriptionUserSubRepoStub()
	oldCycleEnd := time.Now().Add(-time.Hour)
	cycleStart := oldCycleEnd.AddDate(0, 0, -30)
	newExpiresAt := oldCycleEnd.AddDate(0, 0, 30)
	windowStart := cycleStart
	subRepo.seed(&UserSubscription{
		ID:                 35,
		UserID:             3005,
		GroupID:            1,
		StartsAt:           cycleStart,
		ExpiresAt:          newExpiresAt,
		Status:             SubscriptionStatusActive,
		DailyWindowStart:   &windowStart,
		WeeklyWindowStart:  &windowStart,
		MonthlyWindowStart: &windowStart,
		QuotaCycleStartAt:  &cycleStart,
		QuotaCycleEndAt:    &oldCycleEnd,
		QuotaCycleDays:     30,
		DailyUsageUSD:      12.3,
		WeeklyUsageUSD:     123.4,
		MonthlyUsageUSD:    456.7,
	})

	svc := NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil)
	refreshed, err := svc.EnsureWindowMaintenance(context.Background(), &UserSubscription{ID: 35})

	require.NoError(t, err)
	require.Zero(t, refreshed.DailyUsageUSD)
	require.Zero(t, refreshed.WeeklyUsageUSD)
	require.Zero(t, refreshed.MonthlyUsageUSD)
	require.NotNil(t, refreshed.QuotaCycleStartAt)
	require.NotNil(t, refreshed.QuotaCycleEndAt)
	require.WithinDuration(t, oldCycleEnd, *refreshed.QuotaCycleStartAt, time.Microsecond)
	require.WithinDuration(t, newExpiresAt, *refreshed.QuotaCycleEndAt, time.Microsecond)
}

func TestBulkAssignSubscriptionCreatedReusedAndConflict(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	// user 1: 语义一致，可 reused
	subRepo.seed(&UserSubscription{
		ID:        21,
		UserID:    1,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 30),
		Notes:     "same-note",
	})
	// user 3: 语义冲突（有效期不一致），应 failed
	subRepo.seed(&UserSubscription{
		ID:        23,
		UserID:    3,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 60),
		Notes:     "same-note",
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	result, err := svc.BulkAssignSubscription(context.Background(), &BulkAssignSubscriptionInput{
		UserIDs:      []int64{1, 2, 3},
		GroupID:      1,
		ValidityDays: 30,
		AssignedBy:   9,
		Notes:        "same-note",
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.SuccessCount)
	require.Equal(t, 1, result.CreatedCount)
	require.Equal(t, 1, result.ReusedCount)
	require.Equal(t, 1, result.FailedCount)
	require.Equal(t, "reused", result.Statuses[1])
	require.Equal(t, "created", result.Statuses[2])
	require.Equal(t, "failed", result.Statuses[3])
	require.Equal(t, 1, subRepo.createCalls)
}

func TestAssignSubscriptionKeepsWorkingWhenIdempotencyStoreUnavailable(t *testing.T) {
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	SetDefaultIdempotencyCoordinator(NewIdempotencyCoordinator(failingIdempotencyRepo{}, DefaultIdempotencyConfig()))
	t.Cleanup(func() {
		SetDefaultIdempotencyCoordinator(nil)
	})

	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)
	sub, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       9001,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "new",
	})
	require.NoError(t, err)
	require.NotNil(t, sub)
	require.Equal(t, 1, subRepo.createCalls, "semantic idempotent endpoint should not depend on idempotency store availability")
}

func TestNormalizeAssignValidityDays(t *testing.T) {
	require.Equal(t, 30, normalizeAssignValidityDays(0))
	require.Equal(t, 30, normalizeAssignValidityDays(-5))
	require.Equal(t, MaxValidityDays, normalizeAssignValidityDays(MaxValidityDays+100))
	require.Equal(t, 7, normalizeAssignValidityDays(7))
}

func TestDetectAssignSemanticConflictCases(t *testing.T) {
	start := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	base := &UserSubscription{
		UserID:    1,
		GroupID:   1,
		StartsAt:  start,
		ExpiresAt: start.AddDate(0, 0, 30),
		Notes:     "same",
	}

	reason, conflict := detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "same",
	})
	require.False(t, conflict)
	require.Equal(t, "", reason)

	reason, conflict = detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 60,
		Notes:        "same",
	})
	require.True(t, conflict)
	require.Equal(t, "validity_days_mismatch", reason)

	reason, conflict = detectAssignSemanticConflict(base, &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 30,
		Notes:        "other",
	})
	require.True(t, conflict)
	require.Equal(t, "notes_mismatch", reason)
}

func TestAssignSubscriptionGroupTypeValidation(t *testing.T) {
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 1, SubscriptionType: SubscriptionTypeStandard},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	svc := NewSubscriptionService(groupRepo, subRepo, nil, nil, nil)

	_, err := svc.AssignSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      1,
		ValidityDays: 30,
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrGroupNotSubscriptionType), infraerrors.Code(err))
}

func strconvFormatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}

func infraerrorsReason(err error) string {
	return infraerrors.Reason(err)
}
