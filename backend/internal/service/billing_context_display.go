package service

import "context"

// ContextPricingIntervalsFromTiers 把阶梯表各档折成展示用 PricingInterval：逐档绝对单价 +
// 计费生成的档位标签（≤272K / >272K）。模型广场与「可用分组」页共用同一口径。
func ContextPricingIntervalsFromTiers(tiers []ContextPricingTier) []PricingInterval {
	intervals := make([]PricingInterval, 0, len(tiers))
	for i, t := range tiers {
		intervals = append(intervals, PricingInterval{
			MinTokens:       t.MinTokens,
			MaxTokens:       t.MaxTokens,
			TierLabel:       t.Label,
			InputPrice:      t.Input,
			OutputPrice:     t.Output,
			CacheWritePrice: t.CacheWrite,
			CacheReadPrice:  t.CacheRead,
			SortOrder:       i,
		})
	}
	return intervals
}

// ResolveDisplayContextIntervals 按实收口径解析（分组, 模型）的上下文阶梯，供用户侧价目展示。
// 多档时返回逐档绝对单价与标签；单档、非 token 计费、无定价来源或解析失败均返回 nil，
// 调用方沿用原有平价展示。platform 为模型所属平台（composite 分组传模型平台）。
func (s *BillingService) ResolveDisplayContextIntervals(ctx context.Context, resolver *ModelPricingResolver, model, platform string, group *Group) []PricingInterval {
	if s == nil || resolver == nil {
		return nil
	}
	sched, err := s.ResolveContextPricingSchedule(ctx, resolver, ContextPricingScheduleInput{
		Model:    model,
		Group:    group,
		Platform: platform,
	})
	if err != nil || sched == nil || len(sched.Tiers) < 2 {
		return nil
	}
	return ContextPricingIntervalsFromTiers(sched.Tiers)
}
