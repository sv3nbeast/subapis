//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountStatsPricing_CustomRulePriority(t *testing.T) {
	channel := &Channel{
		ID:                         1,
		Status:                     StatusActive,
		ApplyPricingToAccountStats: true,
		AccountStatsPricingRules: []AccountStatsPricingRule{
			{
				GroupIDs: []int64{10},
				Pricing: []ChannelModelPricing{
					{
						Platform:    "anthropic",
						Models:      []string{"claude-sonnet-*"},
						BillingMode: BillingModeToken,
						InputPrice:  testPtrFloat64(0.01),
						OutputPrice: testPtrFloat64(0.02),
					},
				},
			},
		},
	}
	channelService := newTestChannelServiceForAccountStats(t, channel, 10, "anthropic")

	got := resolveAccountStatsCost(
		context.Background(),
		channelService,
		nil,
		101,
		10,
		"claude-sonnet-4-6",
		UsageTokens{InputTokens: 100, OutputTokens: 50},
		1,
		99,
		"",
		time.Time{},
	)

	require.NotNil(t, got)
	require.InDelta(t, 2.0, *got, 1e-12)
}

func TestAccountStatsPricing_ApplyPricingUsesTotalCost(t *testing.T) {
	channel := &Channel{
		ID:                         1,
		Status:                     StatusActive,
		ApplyPricingToAccountStats: true,
	}
	channelService := newTestChannelServiceForAccountStats(t, channel, 10, "anthropic")

	got := resolveAccountStatsCost(
		context.Background(),
		channelService,
		nil,
		101,
		10,
		"claude-sonnet-4-6",
		UsageTokens{InputTokens: 100},
		1,
		0.75,
		"",
		time.Time{},
	)

	require.NotNil(t, got)
	require.InDelta(t, 0.75, *got, 1e-12)
}

func TestAccountStatsPricing_ModelFileFallback(t *testing.T) {
	channel := &Channel{ID: 1, Status: StatusActive}
	channelService := newTestChannelServiceForAccountStats(t, channel, 10, "anthropic")
	billingService := &BillingService{
		fallbackPrices: map[string]*ModelPricing{
			"claude-sonnet-4": {
				InputPricePerToken:  0.001,
				OutputPricePerToken: 0.002,
			},
		},
	}

	got := resolveAccountStatsCost(
		context.Background(),
		channelService,
		billingService,
		101,
		10,
		"claude-sonnet-4-6",
		UsageTokens{InputTokens: 100, OutputTokens: 50},
		1,
		99,
		"",
		time.Time{},
	)

	require.NotNil(t, got)
	require.InDelta(t, 0.2, *got, 1e-12)
}

func TestAccountStatsPricing_TokenIntervals(t *testing.T) {
	pricing := &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(0.01),
		Intervals: []PricingInterval{
			{
				MinTokens:  100,
				MaxTokens:  testPtrInt(1000),
				InputPrice: testPtrFloat64(0.02),
			},
		},
	}

	got := calculateAccountStatsCost(pricing, UsageTokens{InputTokens: 500}, 1)

	require.NotNil(t, got)
	require.InDelta(t, 10.0, *got, 1e-12)
}

func TestAccountStatsPricing_CacheWriteBreakdown(t *testing.T) {
	pricing := &ChannelModelPricing{
		BillingMode:       BillingModeToken,
		CacheWritePrice:   testPtrFloat64(0.01),
		CacheWrite5mPrice: testPtrFloat64(0.02),
		CacheWrite1hPrice: testPtrFloat64(0.03),
	}

	got := calculateAccountStatsCost(pricing, UsageTokens{
		CacheCreationTokens:   300,
		CacheCreation5mTokens: 100,
		CacheCreation1hTokens: 200,
	}, 1)

	require.NotNil(t, got)
	require.InDelta(t, 8.0, *got, 1e-12)
}

func TestAccountStatsPricing_WildcardUsesLongestPrefix(t *testing.T) {
	pricing := []ChannelModelPricing{
		{ID: 1, Models: []string{"claude-*"}},
		{ID: 2, Models: []string{"claude-opus-*"}},
	}

	got := findAccountStatsPricingForModel(pricing, "", "claude-opus-4-6")

	require.NotNil(t, got)
	require.Equal(t, int64(2), got.ID)
}

func newTestChannelServiceForAccountStats(t *testing.T, channel *Channel, groupID int64, platform string) *ChannelService {
	t.Helper()

	cache := newEmptyChannelCache()
	cache.channelByGroupID[groupID] = channel
	cache.groupPlatform[groupID] = platform
	cache.loadedAt = time.Now()

	channelService := &ChannelService{}
	channelService.cache.Store(cache)
	return channelService
}

// 官方 8249ab37d 引入 max 推理强度倍率；本地默认不自动注入（见
// applyDefaultMaxReasoningEffortMultiplier），账号统计成本因此与生产口径一致，
// 仅在运营显式配置渠道倍率时才放大。
func TestAccountStatsPricing_Fable51MaxEffortKeepsBaseQuota(t *testing.T) {
	bs := &BillingService{fallbackPrices: map[string]*ModelPricing{
		"claude-fable-5-1": {InputPricePerToken: 0.001},
	}}
	tokens := UsageTokens{InputTokens: 100}
	standard := tryAccountStatsModelFilePricing(bs, "claude-fable-5-1", tokens, "", time.Time{}, "xhigh")
	maxEffort := tryAccountStatsModelFilePricing(bs, "claude-fable-5-1", tokens, "", time.Time{}, "max")
	require.NotNil(t, standard)
	require.NotNil(t, maxEffort)
	require.InDelta(t, 0.1, *standard, 1e-12)
	require.InDelta(t, *standard, *maxEffort, 1e-12)
}

// 运营显式配置的 max 倍率仍然生效。
func TestAccountStatsPricing_Fable51MaxEffortHonorsConfiguredMultiplier(t *testing.T) {
	configured := 3.0
	bs := &BillingService{fallbackPrices: map[string]*ModelPricing{
		"claude-fable-5-1": {InputPricePerToken: 0.001, MaxReasoningEffortMultiplier: &configured},
	}}
	tokens := UsageTokens{InputTokens: 100}
	standard := tryAccountStatsModelFilePricing(bs, "claude-fable-5-1", tokens, "", time.Time{}, "xhigh")
	maxEffort := tryAccountStatsModelFilePricing(bs, "claude-fable-5-1", tokens, "", time.Time{}, "max")
	require.NotNil(t, standard)
	require.NotNil(t, maxEffort)
	require.InDelta(t, *standard*3, *maxEffort, 1e-12)
}

// 官方 958a21ad6/ab9bd9e87：账号统计成本按 pricingAt 叠加 DeepSeek 峰谷倍率（默认价卡）。
func TestAccountStatsPricing_DeepSeekPeakPricing(t *testing.T) {
	weekday := func(hour, minute int) time.Time {
		return time.Date(2026, time.August, 24, hour, minute, 0, 0, time.UTC)
	}
	for _, model := range []struct {
		name                          string
		input, output, cacheReadPrice float64
	}{
		{"deepseek-v4-flash", 1.5e-7, 6e-7, 3e-9},
		{"deepseek-v4-pro", 6.6e-7, 1.98e-6, 2.2e-8},
	} {
		for _, usage := range []struct {
			name   string
			tokens UsageTokens
		}{
			{"input", UsageTokens{InputTokens: 1000}},
			{"output", UsageTokens{OutputTokens: 500}},
			{"cache_read", UsageTokens{CacheReadTokens: 1000}},
			{"mixed", UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}},
		} {
			t.Run(model.name+"/"+usage.name, func(t *testing.T) {
				bs := newTestBillingService()
				tokens := usage.tokens
				baseCost := float64(tokens.InputTokens)*model.input +
					float64(tokens.OutputTokens)*model.output + float64(tokens.CacheReadTokens)*model.cacheReadPrice
				for _, slot := range []struct {
					name       string
					at         time.Time
					multiplier float64
				}{
					{"before_morning_peak", weekday(0, 59), 1},
					{"morning_peak_start", weekday(1, 0), 2},
					{"morning_peak_last_minute", weekday(3, 59), 2},
					{"morning_peak_end", weekday(4, 0), 1},
					{"afternoon_peak_start", weekday(6, 0), 2},
					{"afternoon_peak_last_minute", weekday(9, 59), 2},
					{"afternoon_peak_end", weekday(10, 0), 1},
					{"saturday", time.Date(2026, time.August, 22, 2, 0, 0, 0, time.UTC), 1},
					{"sunday", time.Date(2026, time.August, 23, 7, 0, 0, 0, time.UTC), 1},
				} {
					t.Run(slot.name, func(t *testing.T) {
						cost := tryAccountStatsModelFilePricing(bs, model.name, tokens, "", slot.at)
						require.NotNil(t, cost)
						require.InDelta(t, baseCost*slot.multiplier, *cost, 1e-12)
					})
				}
			})
		}
	}
}

// 官方 958a21ad6：DeepSeek 账号统计成本的优先级链（自定义规则 > 客户计费 > 目录价×峰谷）。
func TestAccountStatsPricing_DeepSeekPricingPriority(t *testing.T) {
	peak := time.Date(2026, time.August, 24, 2, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name         string
		customRule   bool
		applyPricing bool
		noChannel    bool
		want         float64
	}{
		{name: "catalog", want: 1000 * 1.5e-7 * 2},
		{name: "custom_rule", customRule: true, want: 1},
		{name: "custom_rule_before_customer_price", customRule: true, applyPricing: true, want: 1},
		{name: "customer_price", applyPricing: true, want: 0.75},
		{name: "no_channel", noChannel: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{
				ID: 1, Status: StatusActive, ApplyPricingToAccountStats: tt.applyPricing,
				ModelPricing: []ChannelModelPricing{{
					Models: []string{"deepseek-v4-flash"}, InputPrice: testPtrFloat64(0.02),
				}},
			}
			if tt.customRule {
				channel.AccountStatsPricingRules = []AccountStatsPricingRule{{
					AccountIDs: []int64{1},
					Pricing: []ChannelModelPricing{{
						Models: []string{"deepseek-v4-flash"}, InputPrice: testPtrFloat64(0.001),
					}},
				}}
			}
			cs := newTestChannelServiceForAccountStats(t, channel, 10, PlatformDeepseek)
			groupID := int64(10)
			if tt.noChannel {
				groupID = 99
			}
			cost := resolveAccountStatsCost(context.Background(), cs, newTestBillingService(),
				1, groupID, "deepseek-v4-flash", UsageTokens{InputTokens: 1000}, 1, 0.75, "", peak)
			if tt.noChannel {
				require.Nil(t, cost)
				return
			}
			require.NotNil(t, cost)
			require.InDelta(t, tt.want, *cost, 1e-12)
		})
	}
}
