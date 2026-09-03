package service

import "strings"

func anthropicSpeedModel(parsed *ParsedRequest, result *ForwardResult) string {
	if result != nil {
		if upstreamModel := strings.TrimSpace(result.UpstreamModel); upstreamModel != "" {
			return upstreamModel
		}
	}
	if parsed == nil {
		return ""
	}
	return parsed.Model
}

// anthropicSpeedServiceTier maps supported Anthropic speed=fast requests to
// the billing tier. Unsupported models and third-party hosting never get the
// premium tier.
func anthropicSpeedServiceTier(account *Account, speed, model string) *string {
	if account == nil || account.Platform != PlatformAnthropic || speed != "fast" {
		return nil
	}
	if account.IsBedrock() || !modelSupportsAnthropicFastMode(model) {
		return nil
	}
	tier := "fast"
	return &tier
}

func modelSupportsAnthropicFastMode(model string) bool {
	modelLower := strings.ToLower(strings.TrimSpace(model))
	if !strings.Contains(modelLower, "opus") {
		return false
	}
	if strings.Contains(modelLower, "opus-5") || strings.Contains(modelLower, "opus5") {
		return true
	}
	return strings.Contains(modelLower, "4.8") || strings.Contains(modelLower, "4-8")
}
