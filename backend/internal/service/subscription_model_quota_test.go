package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSubscriptionModelQuotaRatios(t *testing.T) {
	got, err := NormalizeSubscriptionModelQuotaRatios(SubscriptionTypeSubscription, map[string]float64{
		" Claude_Fable-5 ": 0.5,
	})
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"claude-fable-5": 0.5}, got)

	got, err = NormalizeSubscriptionModelQuotaRatios(SubscriptionTypeStandard, map[string]float64{"claude-fable-5": 0.5})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestNormalizeSubscriptionModelQuotaRatiosRejectsInvalidValues(t *testing.T) {
	for _, ratio := range []float64{0, -0.1, 1.01, math.NaN(), math.Inf(1)} {
		_, err := NormalizeSubscriptionModelQuotaRatios(SubscriptionTypeSubscription, map[string]float64{"claude-fable-5": ratio})
		require.Error(t, err, "ratio=%v", ratio)
	}
	_, err := NormalizeSubscriptionModelQuotaRatios(SubscriptionTypeSubscription, map[string]float64{"invalid model": 0.5})
	require.Error(t, err)
}

func TestValidateSubscriptionModelQuotaBase(t *testing.T) {
	rules := map[string]float64{"claude-fable-5": 0.5}
	require.Error(t, ValidateSubscriptionModelQuotaBase(rules, nil, nil, nil))
	zero := 0.0
	require.Error(t, ValidateSubscriptionModelQuotaBase(rules, &zero, nil, nil))
	weekly := 20.0
	require.NoError(t, ValidateSubscriptionModelQuotaBase(rules, nil, &weekly, nil))
	require.NoError(t, ValidateSubscriptionModelQuotaBase(nil, nil, nil, nil))
}

func TestMatchSubscriptionModelQuotaUsesLongestFamilyRule(t *testing.T) {
	rules := map[string]float64{
		"claude-fable":   0.8,
		"claude-fable-5": 0.5,
	}
	for _, requested := range []string{
		"claude-fable-5[1m]",
		"Claude_Fable-5-thinking",
		"publishers/anthropic/models/claude-fable-5-20260601",
		"anthropic.claude-fable-5-v1:0",
	} {
		model, ratio, ok := MatchSubscriptionModelQuota(rules, requested)
		require.True(t, ok, requested)
		require.Equal(t, "claude-fable-5", model)
		require.Equal(t, 0.5, ratio)
	}
}

func TestCheckSubscriptionModelQuotaRejectsOnlyConfiguredModelAtLimit(t *testing.T) {
	now := time.Now()
	dailyStart := now.Add(-time.Hour)
	dailyLimit := 10.0
	group := &Group{
		SubscriptionType: SubscriptionTypeSubscription,
		DailyLimitUSD:    &dailyLimit,
		ModelQuotaRatios: map[string]float64{"claude-fable-5": 0.5},
	}
	sub := &UserSubscription{
		StartsAt:         now.Add(-24 * time.Hour),
		ExpiresAt:        now.Add(30 * 24 * time.Hour),
		DailyWindowStart: &dailyStart,
	}
	usage := map[string]SubscriptionModelUsage{
		"claude-fable-5": {DailyUsageUSD: 5},
	}

	ctx := context.WithValue(context.Background(), ctxkey.Model, "claude-fable-5[1m]")
	err := checkSubscriptionModelQuota(ctx, group, sub, usage)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrSubscriptionModelQuotaExhausted))

	ctx = context.WithValue(context.Background(), ctxkey.Model, "claude-sonnet-5")
	require.NoError(t, checkSubscriptionModelQuota(ctx, group, sub, usage))
}

func TestCheckSubscriptionModelQuotaIgnoresExpiredWindowUsage(t *testing.T) {
	now := time.Now()
	dailyStart := now.Add(-25 * time.Hour)
	dailyLimit := 10.0
	group := &Group{
		SubscriptionType: SubscriptionTypeSubscription,
		DailyLimitUSD:    &dailyLimit,
		ModelQuotaRatios: map[string]float64{"claude-fable-5": 0.5},
	}
	sub := &UserSubscription{
		StartsAt:         now.Add(-48 * time.Hour),
		ExpiresAt:        now.Add(30 * 24 * time.Hour),
		DailyWindowStart: &dailyStart,
	}
	ctx := context.WithValue(context.Background(), ctxkey.Model, "claude-fable-5")
	require.NoError(t, checkSubscriptionModelQuota(ctx, group, sub, map[string]SubscriptionModelUsage{
		"claude-fable-5": {DailyUsageUSD: 9},
	}))
}

func TestBuildUsageBillingCommandIncludesSubscriptionQuotaModel(t *testing.T) {
	groupID := int64(9)
	subID := int64(11)
	p := &postUsageBillingParams{
		Cost:               &CostBreakdown{ActualCost: 1},
		User:               &User{ID: 1},
		APIKey:             &APIKey{ID: 2, GroupID: &groupID, Group: &Group{ModelQuotaRatios: map[string]float64{"claude-fable-5": 0.5}}},
		Account:            &Account{ID: 3},
		Subscription:       &UserSubscription{ID: subID},
		IsSubscriptionBill: true,
		RequestedModel:     "claude-fable-5[1m]",
	}
	cmd := buildUsageBillingCommand("req-1", &UsageLog{Model: "claude-fable-5", RequestedModel: "claude-fable-5[1m]"}, p)
	require.NotNil(t, cmd)
	require.Equal(t, "claude-fable-5", cmd.SubscriptionModel)
}

func TestUsageBillingFingerprintRemainsCompatibleWithExistingDedupRows(t *testing.T) {
	cmd := &UsageBillingCommand{
		UserID:      1,
		AccountID:   2,
		APIKeyID:    3,
		Model:       "claude-fable-5",
		BillingType: BillingTypeSubscription,
	}
	withoutModelQuota := buildUsageBillingFingerprint(cmd)
	cmd.SubscriptionModel = "claude-fable-5"
	require.Equal(t, withoutModelQuota, buildUsageBillingFingerprint(cmd))
}

func TestNormalizeExpiredWindowsResetsOnlyExpiredModelDimensions(t *testing.T) {
	dailyStart := time.Now().Add(-25 * time.Hour)
	weeklyStart := time.Now().Add(-24 * time.Hour)
	monthlyStart := time.Now().Add(-24 * time.Hour)
	subs := []UserSubscription{{
		DailyWindowStart:   &dailyStart,
		WeeklyWindowStart:  &weeklyStart,
		MonthlyWindowStart: &monthlyStart,
		DailyUsageUSD:      4,
		WeeklyUsageUSD:     5,
		MonthlyUsageUSD:    6,
		ModelUsage: map[string]SubscriptionModelUsage{
			"claude-fable-5": {DailyUsageUSD: 2, WeeklyUsageUSD: 3, MonthlyUsageUSD: 4},
		},
	}}

	normalizeExpiredWindows(subs)

	require.Zero(t, subs[0].DailyUsageUSD)
	require.Equal(t, 5.0, subs[0].WeeklyUsageUSD)
	require.Equal(t, 6.0, subs[0].MonthlyUsageUSD)
	usage := subs[0].ModelUsage["claude-fable-5"]
	require.Zero(t, usage.DailyUsageUSD)
	require.Equal(t, 3.0, usage.WeeklyUsageUSD)
	require.Equal(t, 4.0, usage.MonthlyUsageUSD)
}
