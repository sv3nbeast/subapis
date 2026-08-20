package service

import dbent "github.com/Wei-Shaw/sub2api/ent"

// subscriptionPlanGroupIDs returns the immutable entitlement ordering used for
// new orders: primary group first, followed by configured bonus groups.
func subscriptionPlanGroupIDs(plan *dbent.SubscriptionPlan) []int64 {
	if plan == nil || plan.GroupID <= 0 {
		return []int64{}
	}
	groupIDs := make([]int64, 0, 1+len(plan.BonusGroupIds))
	groupIDs = append(groupIDs, plan.GroupID)
	seen := map[int64]struct{}{plan.GroupID: {}}
	for _, groupID := range plan.BonusGroupIds {
		if groupID <= 0 {
			continue
		}
		if _, exists := seen[groupID]; exists {
			continue
		}
		seen[groupID] = struct{}{}
		groupIDs = append(groupIDs, groupID)
	}
	return groupIDs
}

// paymentOrderSubscriptionGroupIDs reads the order-time entitlement snapshot.
// Legacy orders created before the snapshot column fall back to the historical
// subscription_group_id field.
func paymentOrderSubscriptionGroupIDs(order *dbent.PaymentOrder) []int64 {
	if order == nil {
		return []int64{}
	}
	groupIDs := make([]int64, 0, len(order.SubscriptionGroupIds)+1)
	seen := make(map[int64]struct{}, len(order.SubscriptionGroupIds)+1)
	for _, groupID := range order.SubscriptionGroupIds {
		if groupID <= 0 {
			continue
		}
		if _, exists := seen[groupID]; exists {
			continue
		}
		seen[groupID] = struct{}{}
		groupIDs = append(groupIDs, groupID)
	}
	if len(groupIDs) == 0 && order.SubscriptionGroupID != nil && *order.SubscriptionGroupID > 0 {
		groupIDs = append(groupIDs, *order.SubscriptionGroupID)
	}
	return groupIDs
}

// PaymentOrderSubscriptionGroupIDs returns the immutable entitlement snapshot
// for a subscription order. Legacy orders fall back to subscription_group_id.
func PaymentOrderSubscriptionGroupIDs(order *dbent.PaymentOrder) []int64 {
	return paymentOrderSubscriptionGroupIDs(order)
}
