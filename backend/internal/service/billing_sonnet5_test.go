package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGetModelPricing_ClaudeSonnet5Fallback(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)

	pricing, err := svc.GetModelPricing("claude-sonnet-5")
	require.NoError(t, err)
	require.NotNil(t, pricing)
	require.InDelta(t, 2e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 10e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 2.5e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 2.5e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 4e-6, pricing.CacheCreation1hPrice, 1e-12)
	require.InDelta(t, 0.2e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.True(t, pricing.SupportsCacheBreakdown)
}
