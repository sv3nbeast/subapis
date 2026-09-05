package dto

import "github.com/Wei-Shaw/sub2api/internal/service"

// Keep the public snake_case contract separate from the legacy storage structs.
// Changing storage JSON tags would make existing CamelCase quota/price rows unreadable.
func groupModelPricingFromService(pricing []service.ChannelModelPricing) []map[string]any {
	out := make([]map[string]any, 0, len(pricing))
	for _, p := range pricing {
		intervals := make([]map[string]any, 0, len(p.Intervals))
		for _, iv := range p.Intervals {
			intervals = append(intervals, map[string]any{
				"min_tokens": iv.MinTokens, "max_tokens": iv.MaxTokens, "tier_label": iv.TierLabel,
				"input_price": iv.InputPrice, "output_price": iv.OutputPrice,
				"cache_write_price": iv.CacheWritePrice, "cache_write_5m_price": iv.CacheWrite5mPrice,
				"cache_write_1h_price": iv.CacheWrite1hPrice, "cache_read_price": iv.CacheReadPrice,
				"input_multiplier": iv.InputMultiplier, "output_multiplier": iv.OutputMultiplier,
				"cache_write_multiplier": iv.CacheWriteMultiplier, "cache_read_multiplier": iv.CacheReadMultiplier,
				"per_request_price": iv.PerRequestPrice, "sort_order": iv.SortOrder,
			})
		}
		out = append(out, map[string]any{
			"platform": p.Platform, "models": p.Models, "enabled": !p.Disabled,
			"billing_mode": p.BillingMode, "input_price": p.InputPrice, "output_price": p.OutputPrice,
			"cache_write_price": p.CacheWritePrice, "cache_write_5m_price": p.CacheWrite5mPrice,
			"cache_write_1h_price": p.CacheWrite1hPrice, "cache_read_price": p.CacheReadPrice,
			"fast_multiplier": p.FastMultiplier, "flex_multiplier": p.FlexMultiplier,
			"image_input_price": p.ImageInputPrice, "image_output_price": p.ImageOutputPrice,
			"per_request_price": p.PerRequestPrice, "intervals": intervals, "time_pricing": p.TimePricing,
		})
	}
	return out
}
