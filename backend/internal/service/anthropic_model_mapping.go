package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

type anthropicModelMappingResult struct {
	Model  string
	Source string
}

var defaultAnthropicModelAliases = map[string]string{
	"claude-opus-4.6":          "claude-opus-4-6",
	"claude-opus-4-6-thinking": "claude-opus-4-6",
	"claude-opus-4.6-thinking": "claude-opus-4-6",
	"claude-opus-4.7":          "claude-opus-4-7",
	"claude-opus-4-7-thinking": "claude-opus-4-7",
	"claude-opus-4.7-thinking": "claude-opus-4-7",
	"claude-opus-4.8":          "claude-opus-4-8",
	"claude-opus-4-8-thinking": "claude-opus-4-8",
	"claude-opus-4.8-thinking": "claude-opus-4-8",
	// Fable 5：暂不支持 thinking 变体，将 -thinking 后缀容错归一到基础模型，
	// 避免客户端误带后缀时把未知模型名透传上游导致报错（不注入 thinking 参数）。
	"claude-fable-5-thinking": "claude-fable-5",
}

func isAnthropicThinkingModelAlias(model string) bool {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return false
	}
	switch trimmed {
	case "claude-opus-4-6-thinking", "claude-opus-4.6-thinking",
		"claude-opus-4-7-thinking", "claude-opus-4.7-thinking",
		"claude-opus-4-8-thinking", "claude-opus-4.8-thinking":
		return true
	default:
		return false
	}
}

func normalizeAnthropicModelIDForUpstream(requestedModel string) string {
	return resolveDefaultAnthropicUpstreamModel(requestedModel).Model
}

func resolveDefaultAnthropicUpstreamModel(requestedModel string) anthropicModelMappingResult {
	result := anthropicModelMappingResult{Model: requestedModel}
	trimmed := strings.TrimSpace(requestedModel)
	if trimmed == "" {
		return result
	}
	if mappedModel, ok := defaultAnthropicModelAliases[trimmed]; ok {
		result.Model = mappedModel
		result.Source = "alias"
		return result
	}
	normalized := claude.NormalizeModelID(trimmed)
	if normalized != trimmed {
		result.Model = normalized
		result.Source = "prefix"
		return result
	}
	if trimmed != requestedModel {
		result.Model = trimmed
		result.Source = "trim"
	}
	return result
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

	defaultResult := resolveDefaultAnthropicUpstreamModel(requestedModel)
	normalized := defaultResult.Model
	source := defaultResult.Source
	if account.Type == AccountTypeServiceAccount {
		vertexModel := normalizeVertexAnthropicModelID(normalized)
		if vertexModel != normalized {
			source = "vertex"
		}
		normalized = vertexModel
	}
	if normalized != requestedModel {
		result.Model = normalized
		result.Source = source
	}
	return result
}
