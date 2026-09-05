package admin

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAstraDedicatedPricingVisibleInAdminResponse(t *testing.T) {
	models := []string{"gpt-6-astra"}
	input, output, write, read := 10e-6, 50e-6, 12.5e-6, 1e-6
	astra := service.ChannelModelPricing{ID: 2, Platform: service.PlatformOpenAI, Models: models, BillingMode: service.BillingModeToken, InputPrice: &input, OutputPrice: &output, CacheWritePrice: &write, CacheReadPrice: &read}
	channel := &service.Channel{ModelPricing: []service.ChannelModelPricing{{ID: 1, Platform: service.PlatformOpenAI, Models: []string{"gpt-5.6-sol"}}}}
	before := channelToResponse(channel)
	require.Len(t, before.ModelPricing, 1)
	channel.ModelPricing = append(channel.ModelPricing, astra)
	channel.AccountStatsPricingRules = []service.AccountStatsPricingRule{{ID: 1, Pricing: []service.ChannelModelPricing{astra}}}
	after := channelToResponse(channel)
	require.Len(t, after.ModelPricing, 2)
	require.Equal(t, models, after.ModelPricing[1].Models)
	require.True(t, after.ModelPricing[1].Enabled)
	require.Equal(t, &write, after.ModelPricing[1].CacheWritePrice)
	require.Equal(t, models, after.AccountStatsPricingRules[0].Pricing[0].Models)
}
