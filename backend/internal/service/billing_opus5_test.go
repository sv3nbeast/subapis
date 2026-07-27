package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func assertClaudeOpus5Pricing(t *testing.T, pricing *ModelPricing) {
	t.Helper()
	require.NotNil(t, pricing)
	require.InDelta(t, 5e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 25e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 6.25e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 6.25e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 10e-6, pricing.CacheCreation1hPrice, 1e-12)
	require.InDelta(t, 0.5e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.True(t, pricing.SupportsCacheBreakdown)
}

func TestGetModelPricing_ClaudeOpus5Fallback(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)

	base, err := svc.GetModelPricing("claude-opus-5")
	require.NoError(t, err)
	assertClaudeOpus5Pricing(t, base)

	thinking, err := svc.GetModelPricing("claude-opus-5-thinking")
	require.NoError(t, err)
	assertClaudeOpus5Pricing(t, thinking)
}

func TestDefaultPricingIncludesOfficialClaudeOpus5Rates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	pricingSvc := &PricingService{}
	pricingData, err := pricingSvc.parsePricingData(data)
	require.NoError(t, err)
	pricingSvc.pricingData = pricingData

	raw := pricingSvc.GetModelPricing("claude-opus-5")
	require.NotNil(t, raw)
	require.Equal(t, int64(1_000_000), gjson.GetBytes(data, "claude-opus-5.max_input_tokens").Int())
	require.Equal(t, int64(128_000), gjson.GetBytes(data, "claude-opus-5.max_output_tokens").Int())
	require.InDelta(t, 5e-6, raw.InputCostPerToken, 1e-12)
	require.InDelta(t, 25e-6, raw.OutputCostPerToken, 1e-12)
	require.InDelta(t, 6.25e-6, raw.CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 10e-6, raw.CacheCreationInputTokenCostAbove1hr, 1e-12)
	require.InDelta(t, 0.5e-6, raw.CacheReadInputTokenCost, 1e-12)

	billingSvc := NewBillingService(&config.Config{}, pricingSvc)
	thinking, err := billingSvc.GetModelPricing("claude-opus-5-thinking")
	require.NoError(t, err)
	assertClaudeOpus5Pricing(t, thinking)
}
