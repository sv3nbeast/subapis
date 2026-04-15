package service

import (
	"context"
	"sort"
	"strings"
)

// resolveAccountStatsCost computes the cost snapshot used by account statistics.
// It never affects user billing. nil means "use legacy total_cost * account_rate_multiplier".
//
// Priority:
//  1. Custom account/group rules, always attempted.
//  2. If ApplyPricingToAccountStats is enabled, use this request's pre-multiplier totalCost.
//  3. Fallback to model pricing file using the final upstream model.
//  4. nil -> legacy formula.
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
) *float64 {
	if channelService == nil || strings.TrimSpace(upstreamModel) == "" {
		return nil
	}
	channel, err := channelService.GetChannelForGroup(ctx, groupID)
	if err != nil || channel == nil {
		return nil
	}

	platform := channelService.GetGroupPlatform(ctx, groupID)
	if cost := tryAccountStatsCustomRules(channel, accountID, groupID, platform, upstreamModel, tokens, requestCount); cost != nil {
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
		return tryAccountStatsModelFilePricing(billingService, upstreamModel, tokens)
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
) *float64 {
	modelLower := strings.ToLower(model)
	for _, rule := range channel.AccountStatsPricingRules {
		if !matchAccountStatsRule(&rule, accountID, groupID) {
			continue
		}
		pricing := findAccountStatsPricingForModel(rule.Pricing, platform, modelLower)
		if pricing == nil {
			continue
		}
		return calculateAccountStatsCost(pricing, tokens, requestCount)
	}
	return nil
}

func tryAccountStatsModelFilePricing(billingService *BillingService, model string, tokens UsageTokens) *float64 {
	pricing, err := billingService.GetModelPricing(model)
	if err != nil || pricing == nil {
		return nil
	}
	cost := float64(tokens.InputTokens)*pricing.InputPricePerToken +
		float64(tokens.OutputTokens)*pricing.OutputPricePerToken +
		float64(tokens.CacheCreationTokens)*pricing.CacheCreationPricePerToken +
		float64(tokens.CacheReadTokens)*pricing.CacheReadPricePerToken +
		float64(tokens.ImageOutputTokens)*pricing.ImageOutputPricePerToken
	if cost <= 0 {
		return nil
	}
	return &cost
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
				InputPrice:      interval.InputPrice,
				OutputPrice:     interval.OutputPrice,
				CacheWritePrice: interval.CacheWritePrice,
				CacheReadPrice:  interval.CacheReadPrice,
				PerRequestPrice: interval.PerRequestPrice,
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
		float64(tokens.CacheCreationTokens)*deref(priceSource.CacheWritePrice) +
		float64(tokens.CacheReadTokens)*deref(priceSource.CacheReadPrice) +
		float64(tokens.ImageOutputTokens)*deref(priceSource.ImageOutputPrice)
	if cost <= 0 {
		return nil
	}
	return &cost
}
