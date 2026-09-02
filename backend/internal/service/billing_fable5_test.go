package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGetModelPricing_ClaudeFable5Fallback(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)

	pricing, err := svc.GetModelPricing("claude-fable-5")
	require.NoError(t, err)
	require.NotNil(t, pricing)
	require.InDelta(t, 10e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 50e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 12.5e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 12.5e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 20e-6, pricing.CacheCreation1hPrice, 1e-12)
	require.InDelta(t, 1e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.True(t, pricing.SupportsCacheBreakdown)
}

func TestGetModelPricing_ClaudeFable51FallbackUsesDedicatedRates(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)

	for _, model := range []string{"claude-fable-5-1", "claude-fable-5-1-thinking"} {
		t.Run(model, func(t *testing.T) {
			pricing, err := svc.GetModelPricing(model)
			require.NoError(t, err)
			require.NotNil(t, pricing)
			require.InDelta(t, 15e-6, pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, 75e-6, pricing.OutputPricePerToken, 1e-12)
			require.InDelta(t, 18.75e-6, pricing.CacheCreationPricePerToken, 1e-12)
			require.InDelta(t, 18.75e-6, pricing.CacheCreation5mPrice, 1e-12)
			require.InDelta(t, 30e-6, pricing.CacheCreation1hPrice, 1e-12)
			require.InDelta(t, 0.25e-6, pricing.CacheReadPricePerToken, 1e-12)
			require.True(t, pricing.SupportsCacheBreakdown)
		})
	}
}

func TestGetModelPricing_ClaudeFable51DynamicFamilyDoesNotMatchFable5(t *testing.T) {
	pricingSvc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"claude-fable-5": {
			InputCostPerToken:  10e-6,
			OutputCostPerToken: 50e-6,
		},
		"claude-fable-5-1": {
			InputCostPerToken:                   15e-6,
			OutputCostPerToken:                  75e-6,
			CacheCreationInputTokenCost:         18.75e-6,
			CacheCreationInputTokenCostAbove1hr: 30e-6,
			CacheReadInputTokenCost:             0.25e-6,
		},
	}}
	svc := NewBillingService(&config.Config{}, pricingSvc)

	pricing, err := svc.GetModelPricing("claude-fable-5-1-thinking")
	require.NoError(t, err)
	require.InDelta(t, 15e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 75e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 0.25e-6, pricing.CacheReadPricePerToken, 1e-12)
}
