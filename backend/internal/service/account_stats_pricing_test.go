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
