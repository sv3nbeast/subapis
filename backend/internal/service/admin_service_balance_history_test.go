//go:build unit

package service

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type balanceHistoryRedeemRepo struct {
	records        []RedeemCode
	totalRecharged float64
}

func (r *balanceHistoryRedeemRepo) Create(context.Context, *RedeemCode) error {
	panic("unexpected Create call")
}

func (r *balanceHistoryRedeemRepo) CreateBatch(context.Context, []RedeemCode) error {
	panic("unexpected CreateBatch call")
}

func (r *balanceHistoryRedeemRepo) GetByID(context.Context, int64) (*RedeemCode, error) {
	panic("unexpected GetByID call")
}

func (r *balanceHistoryRedeemRepo) GetByCode(context.Context, string) (*RedeemCode, error) {
	panic("unexpected GetByCode call")
}

func (r *balanceHistoryRedeemRepo) Update(context.Context, *RedeemCode) error {
	panic("unexpected Update call")
}

func (r *balanceHistoryRedeemRepo) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

func (r *balanceHistoryRedeemRepo) Use(context.Context, int64, int64) error {
	panic("unexpected Use call")
}

func (r *balanceHistoryRedeemRepo) List(context.Context, pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (r *balanceHistoryRedeemRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (r *balanceHistoryRedeemRepo) ListByUser(context.Context, int64, int) ([]RedeemCode, error) {
	panic("unexpected ListByUser call")
}

func (r *balanceHistoryRedeemRepo) ListByUserPaginated(_ context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]RedeemCode, *pagination.PaginationResult, error) {
	filtered := make([]RedeemCode, 0, len(r.records))
	for _, record := range r.records {
		if record.UsedBy == nil || *record.UsedBy != userID {
			continue
		}
		if codeType != "" && record.Type != codeType {
			continue
		}
		filtered = append(filtered, record)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return balanceHistoryRecordTime(filtered[i]).After(balanceHistoryRecordTime(filtered[j]))
	})
	total := int64(len(filtered))
	limit := params.Limit()
	if params.Page < 1 {
		params.Page = 1
	}
	params.PageSize = limit
	offset := params.Offset()
	if offset >= len(filtered) {
		filtered = []RedeemCode{}
	} else {
		end := offset + limit
		if end > len(filtered) {
			end = len(filtered)
		}
		filtered = filtered[offset:end]
	}
	return filtered, balanceHistoryPaginationResult(total, params), nil
}

func (r *balanceHistoryRedeemRepo) SumPositiveBalanceByUser(context.Context, int64) (float64, error) {
	return r.totalRecharged, nil
}

func balanceHistoryPaginationResult(total int64, params pagination.PaginationParams) *pagination.PaginationResult {
	limit := params.Limit()
	if params.Page < 1 {
		params.Page = 1
	}
	pages := int((total + int64(limit) - 1) / int64(limit))
	if pages < 1 {
		pages = 1
	}
	return &pagination.PaginationResult{
		Total:    total,
		Page:     params.Page,
		PageSize: limit,
		Pages:    pages,
	}
}

func TestAdminServiceGetUserBalanceHistoryIncludesSubscriptionPayments(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	now := time.Now().UTC()

	user, err := client.User.Create().
		SetEmail("history@example.com").
		SetPasswordHash("hash").
		SetUsername("history-user").
		Save(ctx)
	require.NoError(t, err)

	completedAt := now.Add(2 * time.Hour)
	_, err = client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(30).
		SetPayAmount(30).
		SetFeeRate(0).
		SetRechargeCode("PAY-SUB-1").
		SetOutTradeNo("sub2_subscription_paid").
		SetPaymentType("alipay").
		SetPaymentTradeNo("trade-sub-1").
		SetOrderType(RedeemTypeSubscription).
		SetStatus(OrderStatusCompleted).
		SetExpiresAt(now.Add(time.Hour)).
		SetPaidAt(now.Add(time.Hour)).
		SetCompletedAt(completedAt).
		SetSubscriptionDays(30).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(30).
		SetPayAmount(30).
		SetFeeRate(0).
		SetRechargeCode("PAY-SUB-PENDING").
		SetOutTradeNo("sub2_subscription_pending").
		SetPaymentType("alipay").
		SetPaymentTradeNo("").
		SetOrderType(RedeemTypeSubscription).
		SetStatus(OrderStatusPending).
		SetExpiresAt(now.Add(time.Hour)).
		SetSubscriptionDays(30).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	usedBy := user.ID
	redeemUsedAt := now.Add(time.Hour)
	redeemRepo := &balanceHistoryRedeemRepo{
		totalRecharged: 10,
		records: []RedeemCode{{
			ID:        10,
			Code:      "BALANCE-REDEEM",
			Type:      RedeemTypeBalance,
			Value:     10,
			Status:    StatusUsed,
			UsedBy:    &usedBy,
			UsedAt:    &redeemUsedAt,
			CreatedAt: now,
		}},
	}
	svc := &adminServiceImpl{
		redeemCodeRepo: redeemRepo,
		entClient:      client,
	}

	records, total, totalRecharged, err := svc.GetUserBalanceHistory(ctx, user.ID, 1, 10, "")
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Equal(t, 10.0, totalRecharged)
	require.Len(t, records, 2)
	require.Equal(t, RedeemTypeSubscription, records[0].Type)
	require.Equal(t, "PAY-SUB-1", records[0].Code)
	require.Equal(t, 30, records[0].ValidityDays)
	require.Equal(t, RedeemTypeBalance, records[1].Type)

	subscriptionRecords, subscriptionTotal, _, err := svc.GetUserBalanceHistory(ctx, user.ID, 1, 10, RedeemTypeSubscription)
	require.NoError(t, err)
	require.Equal(t, int64(1), subscriptionTotal)
	require.Len(t, subscriptionRecords, 1)
	require.Equal(t, "PAY-SUB-1", subscriptionRecords[0].Code)

	balanceRecords, balanceTotal, _, err := svc.GetUserBalanceHistory(ctx, user.ID, 1, 10, RedeemTypeBalance)
	require.NoError(t, err)
	require.Equal(t, int64(1), balanceTotal)
	require.Len(t, balanceRecords, 1)
	require.Equal(t, "BALANCE-REDEEM", balanceRecords[0].Code)
}
