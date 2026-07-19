package domain

// SubscriptionModelUsage stores the billed USD usage for one configured model
// inside the subscription's existing daily, weekly, and monthly windows.
type SubscriptionModelUsage struct {
	DailyUsageUSD   float64 `json:"daily_usage_usd"`
	WeeklyUsageUSD  float64 `json:"weekly_usage_usd"`
	MonthlyUsageUSD float64 `json:"monthly_usage_usd"`
}
