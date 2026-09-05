package service

// Search/tool calls add to token charges without changing their token split.
func addTokenSearchSurcharge(cost *CostBreakdown, billing *BillingService, apiKey *APIKey, calls int, multiplier float64) *CostBreakdown {
	if calls <= 0 || billing == nil {
		return cost
	}
	var price *float64
	if apiKey != nil && apiKey.Group != nil {
		price = apiKey.Group.SearchPricePer1k
	}
	search := billing.CalculateSearchCost(calls, price, multiplier)
	if cost == nil {
		return search
	}
	if search != nil {
		cost.TotalCost += search.TotalCost
		cost.ActualCost += search.ActualCost
	}
	return cost
}
