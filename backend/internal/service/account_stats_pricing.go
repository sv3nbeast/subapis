package service

import (
	"context"
	"sort"
	"strings"
	"time"
)

// resolveAccountStatsCost computes the cost snapshot used by account statistics.
// It never affects user billing. nil means "use legacy total_cost * account_rate_multiplier".
//
// Priority:
//  1. Custom account/group rules, always attempted.
//  2. If ApplyPricingToAccountStats is enabled, use this request's pre-multiplier totalCost.
//  3. Fallback to model pricing file using the final upstream model.
//  4. nil -> legacy formula.
//
// pricingAt 与本次客户计费使用同一时刻，避免跨峰谷请求的成本与售价错位。
// reasoningEffort 是最终转发等级；Fable 5.1 max 默认按 3 倍额度消耗（官方 8249ab37d）。
func resolveAccountStatsCost(
	ctx context.Context,
	channelService *ChannelService,
	billingService *BillingService,
	accountID int64,
	groupID int64,
	upstreamModel string,
	tokens UsageTokens,
	requestCount int,
	totalCost float64,
	serviceTier string,
	pricingAt time.Time,
	reasoningEfforts ...string,
) *float64 {
	reasoningEffort := ""
	if len(reasoningEfforts) > 0 {
		reasoningEffort = reasoningEfforts[0]
	}
	if channelService == nil || strings.TrimSpace(upstreamModel) == "" {
		return nil
	}
	channel, err := channelService.GetChannelForGroup(ctx, groupID)
	if err != nil || channel == nil {
		return nil
	}

	platform := channelService.GetGroupPlatform(ctx, groupID)
	if cost := tryAccountStatsCustomRules(channel, accountID, groupID, platform, upstreamModel, tokens, requestCount, reasoningEffort); cost != nil {
		return cost
	}

	if channel.ApplyPricingToAccountStats {
		if totalCost <= 0 {
			return nil
		}
		cost := totalCost
		return &cost
	}

	if billingService != nil {
		return tryAccountStatsModelFilePricing(billingService, upstreamModel, tokens, serviceTier, pricingAt, reasoningEffort)
	}
	return nil
}

func tryAccountStatsCustomRules(
	channel *Channel,
	accountID int64,
	groupID int64,
	platform string,
	model string,
	tokens UsageTokens,
	requestCount int,
	reasoningEfforts ...string,
) *float64 {
	reasoningEffort := ""
	if len(reasoningEfforts) > 0 {
		reasoningEffort = reasoningEfforts[0]
	}
	modelLower := strings.ToLower(model)
	for _, rule := range channel.AccountStatsPricingRules {
		if !matchAccountStatsRule(&rule, accountID, groupID) {
			continue
		}
		pricing := findAccountStatsPricingForModel(rule.Pricing, platform, modelLower)
		if pricing == nil {
			continue
		}
		cost := calculateAccountStatsCost(pricing, tokens, requestCount)
		if cost != nil {
			*cost *= maxReasoningEffortBillingMultiplier(model, reasoningEffort, nil)
		}
		return cost
	}
	return nil
}

// tryAccountStatsModelFilePricing 使用模型定价文件（LiteLLM/fallback）中的价格计算费用。
// 与用户计费共用同一条定价管线；解析器不配置渠道或分组，保持优先级 3 的
// 语义：只取模型定价文件，不引入自定义售价。
func tryAccountStatsModelFilePricing(billingService *BillingService, model string, tokens UsageTokens, serviceTier string, pricingAt time.Time, reasoningEfforts ...string) *float64 {
	reasoningEffort := ""
	if len(reasoningEfforts) > 0 {
		reasoningEffort = reasoningEfforts[0]
	}
	breakdown, err := billingService.CalculateCostUnified(CostInput{
		Ctx:             context.Background(),
		Model:           model,
		Tokens:          tokens,
		RateMultiplier:  1,
		ServiceTier:     normalizeBillingServiceTier(serviceTier),
		ReasoningEffort: reasoningEffort,
		PricingAt:       pricingAt,
		Resolver:        NewModelPricingResolver(nil, billingService),
	})
	if err != nil || breakdown == nil || breakdown.TotalCost <= 0 {
		return nil
	}
	return &breakdown.TotalCost
}

func matchAccountStatsRule(rule *AccountStatsPricingRule, accountID int64, groupID int64) bool {
	if len(rule.AccountIDs) == 0 && len(rule.GroupIDs) == 0 {
		return false
	}
	for _, id := range rule.AccountIDs {
		if id == accountID {
			return true
		}
	}
	for _, id := range rule.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

type accountStatsWildcardMatch struct {
	prefixLen int
	pricing   *ChannelModelPricing
}

func findAccountStatsPricingForModel(pricingList []ChannelModelPricing, platform string, modelLower string) *ChannelModelPricing {
	for i := range pricingList {
		pricing := &pricingList[i]
		if pricing.Disabled {
			continue
		}
		if !isAccountStatsPlatformMatch(platform, pricing.Platform) {
			continue
		}
		for _, model := range pricing.Models {
			if strings.ToLower(model) == modelLower {
				return pricing
			}
		}
	}

	var matches []accountStatsWildcardMatch
	for i := range pricingList {
		pricing := &pricingList[i]
		if pricing.Disabled {
			continue
		}
		if !isAccountStatsPlatformMatch(platform, pricing.Platform) {
			continue
		}
		for _, model := range pricing.Models {
			lower := strings.ToLower(model)
			if !strings.HasSuffix(lower, "*") {
				continue
			}
			prefix := strings.TrimSuffix(lower, "*")
			if strings.HasPrefix(modelLower, prefix) {
				matches = append(matches, accountStatsWildcardMatch{
					prefixLen: len(prefix),
					pricing:   pricing,
				})
			}
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].prefixLen > matches[j].prefixLen
	})
	return matches[0].pricing
}

func isAccountStatsPlatformMatch(queryPlatform string, pricingPlatform string) bool {
	return queryPlatform == "" || pricingPlatform == "" || queryPlatform == pricingPlatform
}

func calculateAccountStatsCost(pricing *ChannelModelPricing, tokens UsageTokens, requestCount int) *float64 {
	if pricing == nil {
		return nil
	}
	switch pricing.BillingMode {
	case BillingModePerRequest, BillingModeImage:
		return calculateAccountStatsPerRequestCost(pricing, requestCount)
	default:
		return calculateAccountStatsTokenCost(pricing, tokens)
	}
}

func calculateAccountStatsPerRequestCost(pricing *ChannelModelPricing, requestCount int) *float64 {
	if pricing.PerRequestPrice == nil || *pricing.PerRequestPrice <= 0 {
		return nil
	}
	cost := *pricing.PerRequestPrice * float64(requestCount)
	return &cost
}

func calculateAccountStatsTokenCost(pricing *ChannelModelPricing, tokens UsageTokens) *float64 {
	priceSource := pricing
	if len(pricing.Intervals) > 0 {
		totalTokens := tokens.InputTokens + tokens.OutputTokens + tokens.CacheCreationTokens + tokens.CacheReadTokens
		if interval := FindMatchingInterval(pricing.Intervals, totalTokens); interval != nil {
			priceSource = &ChannelModelPricing{
				InputPrice:        interval.InputPrice,
				OutputPrice:       interval.OutputPrice,
				CacheWritePrice:   interval.CacheWritePrice,
				CacheWrite5mPrice: interval.CacheWrite5mPrice,
				CacheWrite1hPrice: interval.CacheWrite1hPrice,
				CacheReadPrice:    interval.CacheReadPrice,
				PerRequestPrice:   interval.PerRequestPrice,
			}
		}
	}
	deref := func(price *float64) float64 {
		if price == nil {
			return 0
		}
		return *price
	}
	cost := float64(tokens.InputTokens)*deref(priceSource.InputPrice) +
		float64(tokens.OutputTokens)*deref(priceSource.OutputPrice) +
		calculateAccountStatsCacheCreationCost(priceSource, tokens) +
		float64(tokens.CacheReadTokens)*deref(priceSource.CacheReadPrice) +
		float64(tokens.ImageOutputTokens)*deref(priceSource.ImageOutputPrice)
	if cost <= 0 {
		return nil
	}
	return &cost
}

func calculateAccountStatsCacheCreationCost(pricing *ChannelModelPricing, tokens UsageTokens) float64 {
	if pricing == nil {
		return 0
	}
	cacheWritePrice := derefFloat64(pricing.CacheWritePrice)
	cacheWrite5mPrice := cacheWritePrice
	if pricing.CacheWrite5mPrice != nil {
		cacheWrite5mPrice = *pricing.CacheWrite5mPrice
	}
	cacheWrite1hPrice := cacheWritePrice
	if pricing.CacheWrite1hPrice != nil {
		cacheWrite1hPrice = *pricing.CacheWrite1hPrice
	}
	if tokens.CacheCreation5mTokens == 0 && tokens.CacheCreation1hTokens == 0 {
		return float64(tokens.CacheCreationTokens) * cacheWrite5mPrice
	}
	return float64(tokens.CacheCreation5mTokens)*cacheWrite5mPrice +
		float64(tokens.CacheCreation1hTokens)*cacheWrite1hPrice
}

func derefFloat64(price *float64) float64 {
	if price == nil {
		return 0
	}
	return *price
}

// applyAccountStatsCost resolves the account stats cost for a usage log entry.
// It prefers the upstream model and falls back to the requested model.
func applyAccountStatsCost(
	ctx context.Context,
	usageLog *UsageLog,
	cs *ChannelService,
	bs *BillingService,
	accountID int64,
	groupID int64,
	upstreamModel string,
	requestedModel string,
	tokens UsageTokens,
	totalCost float64,
	pricingAt time.Time,
) {
	model := upstreamModel
	if model == "" {
		model = requestedModel
	}
	requestCount := 1
	if usageLog != nil && usageLog.ImageCount > 0 {
		requestCount = usageLog.ImageCount
	}
	serviceTier := ""
	reasoningEffort := ""
	if usageLog != nil && usageLog.ServiceTier != nil {
		serviceTier = *usageLog.ServiceTier
	}
	if usageLog != nil && usageLog.ReasoningEffort != nil {
		reasoningEffort = *usageLog.ReasoningEffort
	}
	usageLog.AccountStatsCost = resolveAccountStatsCost(
		ctx,
		cs,
		bs,
		accountID,
		groupID,
		model,
		tokens,
		requestCount,
		totalCost,
		serviceTier,
		pricingAt,
		reasoningEffort,
	)
}
