package domain

// SubscriptionModelQuotaGroup shares one quota ratio across an explicit set
// of normalized model IDs. ID is stable and is used as the usage bucket key.
type SubscriptionModelQuotaGroup struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Models []string `json:"models"`
	Ratio  float64  `json:"ratio"`
}

// SubscriptionModelUsage stores the billed USD usage for one configured model
// inside the subscription's existing daily, weekly, and monthly windows.
type SubscriptionModelUsage struct {
	DailyUsageUSD   float64 `json:"daily_usage_usd"`
	WeeklyUsageUSD  float64 `json:"weekly_usage_usd"`
	MonthlyUsageUSD float64 `json:"monthly_usage_usd"`
}
