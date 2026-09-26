package service

import (
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	kironianzs "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

// TestOpus55ModelContract locks the discovery/alias surface verified against the
// live Anthropic Messages upstream on 2026-09-24 (account 2587): the exact id is
// echoed back, thinking cannot be disabled, and Kiro does not serve the model.
func TestOpus55ModelContract(t *testing.T) {
	require.Contains(t, claude.DefaultModelIDs(), "claude-opus-5-5")

	// One official id. The dotted spelling and the -thinking suffix are tolerated
	// and normalized; no dated/pro/ultra variant is invented.
	for _, alias := range []string{"claude-opus-5.5", "claude-opus-5-5-thinking", "claude-opus-5.5-thinking"} {
		require.Equal(t, "claude-opus-5-5", normalizeAnthropicModelIDForUpstream(alias), alias)
	}
	require.Equal(t, "claude-opus-5-5", normalizeAnthropicModelIDForUpstream("claude-opus-5-5"))

	// Opus 5.5 rejects thinking.disabled and budget_tokens upstream, so its
	// thinking alias must resolve to adaptive, never to the legacy enabled mode.
	require.Equal(t, "adaptive", anthropicThinkingModeForAlias("claude-opus-5-5-thinking"))
	require.Equal(t, "adaptive", anthropicThinkingModeForAlias("claude-opus-5.5-thinking"))
	require.True(t, isAnthropicThinkingModelAlias("claude-opus-5-5-thinking"))

	// Opus 5 must keep its own identity: 5.5 is a separate model, not an alias.
	require.Equal(t, "claude-opus-5", normalizeAnthropicModelIDForUpstream("claude-opus-5"))

	// Kiro added Opus 5.5 after 2026-09-24: ListAvailableModels now lists
	// claude-opus-5.5 (verified 2026-09-26, account 2696), so the Kiro catalog
	// advertises it. Its Kiro-side contract is covered by the Kiro tests.
	var kiroHasOpus55 bool
	for _, m := range kironianzs.DefaultModels {
		kiroHasOpus55 = kiroHasOpus55 || m.ID == "claude-opus-5-5"
	}
	require.True(t, kiroHasOpus55)
}

// TestOpus55PricingDoesNotFallBackToOpus5 is the regression this change exists
// for: "claude-opus-5-5" contains the substring "claude-opus-5", so every
// family/fuzzy matcher must prefer the more specific card. Falling through to
// Opus 5 would bill $5/$25 instead of the official $4/$20.
func TestOpus55PricingDoesNotFallBackToOpus5(t *testing.T) {
	body, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := &PricingService{}
	catalog.pricingData, err = catalog.parsePricingData(body)
	require.NoError(t, err)

	for _, source := range []*PricingService{nil, catalog, {pricingData: map[string]*LiteLLMModelPricing{}}} {
		s := NewBillingService(&config.Config{}, nil)
		s.pricingService = source

		opus5, err := s.GetModelPricing("claude-opus-5")
		require.NoError(t, err)
		require.InDelta(t, 5e-6, opus5.InputPricePerToken, 1e-14)

		for _, model := range []string{"claude-opus-5-5", "claude-opus-5.5", "claude-opus-5-5-thinking"} {
			p, err := s.GetModelPricing(model)
			require.NoError(t, err, model)
			require.InDelta(t, 4e-6, p.InputPricePerToken, 1e-14, model)
			require.InDelta(t, 20e-6, p.OutputPricePerToken, 1e-14, model)
			// Cache reads are 0.05x base input on Opus 5.5, not the usual 0.1x.
			require.InDelta(t, 0.2e-6, p.CacheReadPricePerToken, 1e-14, model)
			require.InDelta(t, 5e-6, p.CacheCreationPricePerToken, 1e-14, model)
			require.NotEqual(t, opus5.InputPricePerToken, p.InputPricePerToken,
				"%s must not inherit the Opus 5 card", model)
		}
	}

	// 1M context is billed at standard rates: no long-context tier may appear.
	s := NewBillingService(&config.Config{}, nil)
	p, err := s.GetModelPricing("claude-opus-5-5")
	require.NoError(t, err)
	require.Zero(t, p.LongContextInputThreshold)
	// Fast mode is 2x standard ($8 / $40).
	require.InDelta(t, 8e-6, p.InputPricePerTokenPriority, 1e-14)
	require.InDelta(t, 40e-6, p.OutputPricePerTokenPriority, 1e-14)
	// 5m / 1h cache-write breakdown per the official table.
	require.InDelta(t, 5e-6, p.CacheCreation5mPrice, 1e-14)
	require.InDelta(t, 8e-6, p.CacheCreation1hPrice, 1e-14)
}

// TestGrok47ModelContract locks the surface verified against the live xAI
// upstream on 2026-09-24 (account 2539): grok-4.7 is echoed back verbatim and a
// synthetic id is rejected with 404, so no alias may collapse into it.
func TestGrok47ModelContract(t *testing.T) {
	ids := make(map[string]bool)
	for _, m := range xai.DefaultModels() {
		ids[m.ID] = true
	}
	require.True(t, ids["grok-4.7"], "grok-4.7 must be discoverable")
	require.True(t, ids["grok-4.6"], "grok-4.6 must remain discoverable")

	mapping := xai.DefaultModelMapping()
	require.Equal(t, "grok-4.7", mapping["grok-4.7"])
	require.Equal(t, "grok-4.7", mapping["grok-4.7-latest"])

	// The undated aliases keep pointing at the configured default (grok-4.6).
	// Promoting them is an operator decision via grok_default_text_model, not a
	// side effect of adding a model.
	require.Equal(t, xai.DefaultTextModel, mapping["grok"])
	require.Equal(t, "grok-4.6", xai.DefaultTextModel)
}

func TestGrok47Pricing(t *testing.T) {
	s := NewBillingService(&config.Config{}, nil)
	for _, model := range []string{"grok-4.7", "grok-4.7-latest"} {
		p, err := s.GetModelPricing(model)
		require.NoError(t, err, model)
		require.InDelta(t, 2e-6, p.InputPricePerToken, 1e-14, model)
		require.InDelta(t, 6e-6, p.OutputPricePerToken, 1e-14, model)
		require.InDelta(t, 0.5e-6, p.CacheReadPricePerToken, 1e-14, model)
		// "Requests whose prompt reaches 200k tokens are billed at the higher
		// rate for all tokens" -> inclusive threshold, x2 on input and output.
		require.Equal(t, 200000, p.LongContextInputThreshold, model)
		require.True(t, p.LongContextThresholdInclusive, model)
		require.InDelta(t, 2, p.LongContextInputMultiplier, 1e-9, model)
		require.InDelta(t, 2, p.LongContextOutputMultiplier, 1e-9, model)
	}

	// Exactly 200,000 prompt tokens already bills at the long-context rate.
	p, err := s.GetModelPricing("grok-4.7")
	require.NoError(t, err)
	require.False(t, s.shouldApplySessionLongContextPricing(UsageTokens{InputTokens: 199999}, p))
	require.True(t, s.shouldApplySessionLongContextPricing(UsageTokens{InputTokens: 200000}, p))

	// The unknown-Grok-text fallback still covers unreleased spellings so a new
	// model can never ship unbilled.
	baseline, err := s.GetModelPricing("grok-4.6")
	require.NoError(t, err)
	for _, unknown := range []string{"grok-4.7-beta", "grok-5", "x-ai/grok-7"} {
		up, err := s.GetModelPricing(unknown)
		require.NoError(t, err, unknown)
		require.InDelta(t, baseline.InputPricePerToken, up.InputPricePerToken, 1e-14, unknown)
	}
}
