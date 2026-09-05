package service

import (
	"context"
	"strings"
)

// A CN provider serving Claude-compatible aliases must not inherit Claude list
// pricing unless the operator explicitly configured a group/channel price.
func (s *OpenAIGatewayService) filterCNProviderBillingModelCandidates(ctx context.Context, account *Account, apiKey *APIKey, candidates []string) []string {
	if account == nil || !account.IsCNProvider() {
		return candidates
	}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		if strings.Contains(strings.ToLower(trimmed), "claude") &&
			s.resolveOpenAIChannelPricing(ctx, trimmed, apiKey) == nil {
			continue
		}
		out = append(out, candidate)
	}
	return out
}
