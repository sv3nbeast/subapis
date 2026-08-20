package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

type bonusRefundUserSubRepo struct {
	*subscriptionUserSubRepoStub
}

func (r *bonusRefundUserSubRepo) GetActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*UserSubscription, error) {
	sub := r.byUserGroup[r.key(userID, groupID)]
	if sub == nil || sub.Status != SubscriptionStatusActive || !sub.ExpiresAt.After(time.Now()) {
		return nil, ErrSubscriptionNotFound
	}
	cp := *sub
	return &cp, nil
}

func TestBonusGroupsOrderSnapshotAndFulfillmentAreIdempotent(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	_, err := client.ExecContext(ctx, "CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_audit_logs_order_action_uniq ON payment_audit_logs(order_id, action)")
	require.NoError(t, err)

	user, err := client.User.Create().
		SetEmail("bonus-fulfillment-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com").
		SetPasswordHash("hash").
		SetUsername("bonus-fulfillment-user").
		Save(ctx)
	require.NoError(t, err)

	plan := &dbent.SubscriptionPlan{
		ID:            88,
		GroupID:       9,
		BonusGroupIds: []int64{36},
		ValidityDays:  1,
		ValidityUnit:  "months",
	}
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	svc := &PaymentService{
		entClient:       client,
		groupRepo:       groupRepo,
		subscriptionSvc: NewSubscriptionService(groupRepo, subRepo, nil, nil, nil),
	}

	order, err := svc.createOrderInTx(
		ctx,
		CreateOrderRequest{UserID: user.ID, PaymentType: payment.TypeStripe, OrderType: payment.OrderTypeSubscription},
		&User{ID: user.ID, Email: user.Email, Username: user.Username},
		plan,
		&PaymentConfig{MaxPendingOrders: 3, OrderTimeoutMin: 30},
		500,
		500,
		0,
		500,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, []int64{9, 36}, order.SubscriptionGroupIds)
	require.NotNil(t, order.SubscriptionGroupID)
	require.Equal(t, int64(9), *order.SubscriptionGroupID)
	require.NotNil(t, order.SubscriptionDays)
	require.Equal(t, 30, *order.SubscriptionDays)

	order, err = client.PaymentOrder.UpdateOneID(order.ID).
		SetStatus(OrderStatusPaid).
		SetPaidAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))

	for _, groupID := range []int64{9, 36} {
		sub, lookupErr := subRepo.GetByUserIDAndGroupID(ctx, user.ID, groupID)
		require.NoError(t, lookupErr)
		require.Contains(t, sub.Notes, paymentSubscriptionOrderNote(order.ID))
	}
	require.Equal(t, 2, subRepo.createCalls)
	audit, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("SUBSCRIPTION_ASSIGNED"),
		).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, audit.Detail, `"groupIDs":[9,36]`)

	staleAt := time.Now().Add(-paymentFulfillmentLeaseDuration - time.Minute)
	_, err = client.PaymentOrder.UpdateOneID(order.ID).
		SetStatus(OrderStatusRecharging).
		SetUpdatedAt(staleAt).
		ClearCompletedAt().
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	require.Equal(t, 2, subRepo.createCalls, "a replay must not extend or recreate either entitlement")
}

func TestBonusGroupsPlanConfigurationValidatesAndPersistsSubscriptionGroups(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	primary, err := client.Group.Create().
		SetName("Max primary").
		SetPlatform(PlatformComposite).
		SetSubscriptionType(SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)
	bonus, err := client.Group.Create().
		SetName("Claude Opus bonus").
		SetPlatform(PlatformAnthropic).
		SetSubscriptionType(SubscriptionTypeSubscription).
		SetMonthlyLimitUsd(500).
		Save(ctx)
	require.NoError(t, err)
	standard, err := client.Group.Create().
		SetName("Standard group").
		SetPlatform(PlatformOpenAI).
		SetSubscriptionType(SubscriptionTypeStandard).
		Save(ctx)
	require.NoError(t, err)

	configSvc := NewPaymentConfigService(client, nil, nil)
	plan, err := configSvc.CreatePlan(ctx, CreatePlanRequest{
		GroupID:       primary.ID,
		BonusGroupIDs: []int64{bonus.ID},
		Name:          "Max with Opus",
		Description:   "Primary subscription plus Claude Opus bonus",
		Price:         700,
		ValidityDays:  1,
		ValidityUnit:  "months",
		ForSale:       true,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{bonus.ID}, plan.BonusGroupIds)

	invalidBonuses := []int64{standard.ID}
	_, err = configSvc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{BonusGroupIDs: &invalidBonuses})
	require.Error(t, err)

	emptyBonuses := []int64{}
	plan, err = configSvc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{BonusGroupIDs: &emptyBonuses})
	require.NoError(t, err)
	require.Empty(t, plan.BonusGroupIds)

	// Operational toggles must remain possible even if an old plan references a
	// group that has since disappeared; only entitlement edits require revalidation.
	legacyPlan, err := client.SubscriptionPlan.Create().
		SetGroupID(999999).
		SetName("Legacy invalid group").
		SetPrice(1).
		Save(ctx)
	require.NoError(t, err)
	forSale := false
	legacyPlan, err = configSvc.UpdatePlan(ctx, legacyPlan.ID, UpdatePlanRequest{ForSale: &forSale})
	require.NoError(t, err)
	require.False(t, legacyPlan.ForSale)
}

func TestBonusGroupsRefundDeductionAndRollbackCoverEverySnapshotGroup(t *testing.T) {
	ctx := context.Background()
	userID := int64(123)
	primaryGroupID := int64(9)
	bonusGroupID := int64(36)
	days := 30
	originalExpiry := time.Now().Add(90 * 24 * time.Hour).Truncate(time.Second)

	subRepo := &bonusRefundUserSubRepo{subscriptionUserSubRepoStub: newSubscriptionUserSubRepoStub()}
	subRepo.seed(&UserSubscription{ID: 91, UserID: userID, GroupID: primaryGroupID, StartsAt: time.Now(), ExpiresAt: originalExpiry, Status: SubscriptionStatusActive})
	subRepo.seed(&UserSubscription{ID: 92, UserID: userID, GroupID: bonusGroupID, StartsAt: time.Now(), ExpiresAt: originalExpiry, Status: SubscriptionStatusActive})
	subscriptionSvc := NewSubscriptionService(&subscriptionGroupRepoStub{}, subRepo, nil, nil, nil)
	svc := &PaymentService{subscriptionSvc: subscriptionSvc}
	order := &dbent.PaymentOrder{
		ID:                   500,
		UserID:               userID,
		OrderType:            payment.OrderTypeSubscription,
		SubscriptionGroupID:  &primaryGroupID,
		SubscriptionGroupIds: []int64{primaryGroupID, bonusGroupID},
		SubscriptionDays:     &days,
	}
	plan := &RefundPlan{OrderID: order.ID, Order: order}

	require.Nil(t, svc.prepDeduct(ctx, order, plan, false))
	require.Equal(t, []RefundSubscriptionTarget{
		{SubscriptionID: 91, GroupID: primaryGroupID},
		{SubscriptionID: 92, GroupID: bonusGroupID},
	}, plan.SubscriptionTargets)

	require.NoError(t, svc.deductRefundSubscriptions(ctx, plan))
	for _, subID := range []int64{91, 92} {
		sub, err := subRepo.GetByID(ctx, subID)
		require.NoError(t, err)
		require.True(t, sub.ExpiresAt.Equal(originalExpiry.AddDate(0, 0, -days)))
	}

	require.NoError(t, svc.rollbackRefundSubscriptions(ctx, plan))
	for _, subID := range []int64{91, 92} {
		sub, err := subRepo.GetByID(ctx, subID)
		require.NoError(t, err)
		require.True(t, sub.ExpiresAt.Equal(originalExpiry))
	}
}
