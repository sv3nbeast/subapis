package service

import (
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionPlanGroupIDsKeepsPrimaryFirstAndDeduplicates(t *testing.T) {
	plan := &dbent.SubscriptionPlan{
		GroupID:       9,
		BonusGroupIds: []int64{36, 9, 0, 37, 36},
	}

	require.Equal(t, []int64{9, 36, 37}, subscriptionPlanGroupIDs(plan))
}

func TestPaymentOrderSubscriptionGroupIDsUsesImmutableSnapshot(t *testing.T) {
	legacyPrimary := int64(9)
	order := &dbent.PaymentOrder{
		SubscriptionGroupID:  &legacyPrimary,
		SubscriptionGroupIds: []int64{10, 36, 10, 0},
	}

	require.Equal(t, []int64{10, 36}, PaymentOrderSubscriptionGroupIDs(order))
}

func TestPaymentOrderSubscriptionGroupIDsFallsBackForLegacyOrders(t *testing.T) {
	legacyPrimary := int64(9)
	order := &dbent.PaymentOrder{SubscriptionGroupID: &legacyPrimary}

	require.Equal(t, []int64{9}, PaymentOrderSubscriptionGroupIDs(order))
}

func TestNormalizePlanBonusGroupIDsRejectsAmbiguousEntitlements(t *testing.T) {
	tests := []struct {
		name string
		raw  []int64
	}{
		{name: "non-positive", raw: []int64{0}},
		{name: "primary repeated", raw: []int64{9}},
		{name: "bonus repeated", raw: []int64{36, 36}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizePlanBonusGroupIDs(9, test.raw)
			require.Error(t, err)
		})
	}

	got, err := normalizePlanBonusGroupIDs(9, []int64{36, 37})
	require.NoError(t, err)
	require.Equal(t, []int64{36, 37}, got)
}
