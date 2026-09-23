package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

func lastOpenAIModelSegment(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if strings.Contains(model, "/") {
		parts := strings.Split(model, "/")
		model = parts[len(parts)-1]
	}
	return strings.TrimSpace(model)
}

func canonicalizeOpenAIModelAliasSpelling(model string) string {
	return openai.CanonicalizeOpenAIModelAliasSpelling(model)
}

func normalizeKnownOpenAICodexModel(model string) string {
	normalized := canonicalizeOpenAIModelAliasSpelling(model)
	if normalized == "" {
		return ""
	}

	if mapped := getNormalizedCodexModel(normalized); mapped != "" {
		return mapped
	}
	if strings.HasSuffix(normalized, "-openai-compact") {
		if mapped := getNormalizedCodexModel(strings.TrimSuffix(normalized, "-openai-compact")); mapped != "" {
			return mapped
		}
	}

	switch {
	case normalized == "gpt-6" || normalized == "gpt-6-astra":
		return "gpt-6-astra"
	case normalized == "gpt-6-sol":
		return "gpt-6-sol"
	case normalized == "gpt-6-luna":
		return "gpt-6-luna"
	case strings.Contains(normalized, "gpt-5.6-sol"):
		return "gpt-5.6-sol"
	case strings.Contains(normalized, "gpt-5.6-terra"):
		return "gpt-5.6-terra"
	case strings.Contains(normalized, "gpt-5.6-luna"):
		return "gpt-5.6-luna"
	case normalized == "gpt-5.6":
		return "gpt-5.6-sol"
	case strings.HasPrefix(normalized, "gpt-5.6-"):
		suffix := strings.TrimPrefix(normalized, "gpt-5.6-")
		if suffix == "max" || isKnownCodexModelSuffix(suffix) {
			return "gpt-5.6-sol"
		}
		return ""
	case strings.Contains(normalized, "gpt-5.5-pro"):
		return "gpt-5.5-pro"
	case strings.Contains(normalized, "gpt-5.5"):
		return "gpt-5.5"
	case strings.Contains(normalized, "gpt-5.4-mini"):
		return "gpt-5.4-mini"
	case strings.Contains(normalized, "gpt-5.4-nano"):
		return "gpt-5.4-nano"
	case strings.Contains(normalized, "gpt-5.4"):
		return "gpt-5.4"
	case strings.Contains(normalized, "gpt-5.2"):
		return "gpt-5.2"
	case strings.Contains(normalized, "gpt-5.3-codex-spark"):
		return "gpt-5.3-codex-spark"
	case strings.Contains(normalized, "gpt-5.3-codex"):
		return "gpt-5.3-codex"
	case strings.Contains(normalized, "gpt-5.3"):
		return "gpt-5.3-codex"
	case strings.Contains(normalized, "codex"):
		return "gpt-5.3-codex"
	case strings.Contains(normalized, "gpt-5"):
		return "gpt-5.4"
	default:
		return ""
	}
}

// isOpenAIGPT56Model 判断是否 GPT-5.6 系列模型；入参可为原始模型名
// （含大小写/路径/后缀变体）或已归一化的基名，两者均能正确识别。
func isOpenAIGPT56Model(model string) bool {
	normalized := canonicalizeOpenAIModelAliasSpelling(model)
	if normalized == "gpt-5.6" {
		return true
	}
	if suffix, ok := strings.CutPrefix(normalized, "gpt-5.6-"); ok && (suffix == "max" || isKnownCodexModelSuffix(suffix)) {
		return true
	}
	for _, prefix := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		if normalized == prefix || strings.HasPrefix(normalized, prefix+"-") {
			return true
		}
	}
	return false
}

// isOpenAIGPT6AstraModel reports the canonical GPT-6 Astra model (provider
// prefixes are stripped by the shared spelling normalization) and the official
// public "gpt-6" alias. Astra has one model ID: reasoning effort belongs in the
// request parameters, never in a synthetic model suffix, and future
// "gpt-6-astra-*" variants (effort/pro/ultra/dated) must not be silently mapped
// to Astra's upstream model or price (local 7fd084688 + migration 236).
func isOpenAIGPT6AstraModel(model string) bool {
	normalized := canonicalizeOpenAIModelAliasSpelling(model)
	return normalized == "gpt-6" || normalized == "gpt-6-astra"
}

// isOpenAIGPT6SolModel / isOpenAIGPT6LunaModel follow Astra's rule: each GPT-6
// model has exactly one official ID (live probe 2026-09-23 rejects every other
// gpt-6-* spelling, including gpt-6-terra, with HTTP 400). Effort belongs in the
// request parameters, so no synthetic effort/dated/pro suffix is accepted here.
func isOpenAIGPT6SolModel(model string) bool {
	return canonicalizeOpenAIModelAliasSpelling(model) == "gpt-6-sol"
}

func isOpenAIGPT6LunaModel(model string) bool {
	return canonicalizeOpenAIModelAliasSpelling(model) == "gpt-6-luna"
}

// isOpenAIGPT6Model reports any verified GPT-6 family model. Used for the
// shared GPT-6 wire contract (unsupported sampling parameters, prompt-cache
// option shape) that the whole generation enforces. Effort handling still
// differs per model and must not be derived from this helper: Astra rejects
// "none", while Sol and Luna accept it.
func isOpenAIGPT6Model(model string) bool {
	return isOpenAIGPT6AstraModel(model) || isOpenAIGPT6SolModel(model) || isOpenAIGPT6LunaModel(model)
}

func appendUsageBillingModelCandidate(candidates []string, seen map[string]struct{}, model string) []string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return candidates
	}
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return
		}
		key := strings.ToLower(candidate)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, candidate)
	}

	add(trimmed)
	if canonical := canonicalizeOpenAIModelAliasSpelling(trimmed); canonical != "" {
		add(canonical)
	}
	if normalized := normalizeKnownOpenAICodexModel(trimmed); normalized != "" {
		add(normalized)
	}
	return candidates
}

func usageBillingModelCandidates(primary string, alternates ...string) []string {
	seen := make(map[string]struct{}, 1+len(alternates))
	candidates := appendUsageBillingModelCandidate(nil, seen, primary)
	for _, alternate := range alternates {
		candidates = appendUsageBillingModelCandidate(candidates, seen, alternate)
	}
	return candidates
}

func firstUsageBillingModel(candidates []string) string {
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
