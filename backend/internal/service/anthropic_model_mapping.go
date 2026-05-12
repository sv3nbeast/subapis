package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/claude"

type anthropicModelMappingResult struct {
	Model  string
	Source string
}

func resolveAnthropicUpstreamModel(account *Account, requestedModel string) anthropicModelMappingResult {
	result := anthropicModelMappingResult{Model: requestedModel}
	if account == nil || requestedModel == "" {
		return result
	}

	if mappedModel, matched := account.ResolveMappedModel(requestedModel); matched {
		result.Model = mappedModel
		result.Source = "account"
		return result
	}

	if account.Platform != PlatformAnthropic {
		return result
	}

	normalized := claude.NormalizeModelID(requestedModel)
	if account.Type == AccountTypeServiceAccount {
		normalized = normalizeVertexAnthropicModelID(normalized)
	}
	if normalized != requestedModel {
		result.Model = normalized
		result.Source = "prefix"
		if account.Type == AccountTypeServiceAccount {
			result.Source = "vertex"
		}
	}
	return result
}
