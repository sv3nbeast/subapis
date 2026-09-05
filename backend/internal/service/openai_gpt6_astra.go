package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// Only these local effort aliases are supported. Do not silently map future
// Astra variants (pro/ultra/dates) to a different upstream model or price.
func isOpenAIGPT6AstraModel(model string) bool {
	m := canonicalizeOpenAIModelAliasSpelling(model)
	if m == "gpt-6-astra" {
		return true
	}
	suffix, ok := strings.CutPrefix(m, "gpt-6-astra-")
	if !ok {
		return false
	}
	switch suffix {
	case "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

func normalizeOpenAIAstraLegacyCacheOptions(req map[string]any) bool {
	model, _ := req["model"].(string)
	if !isOpenAIGPT6AstraModel(model) {
		return false
	}
	if _, exists := req["prompt_cache_retention"]; !exists {
		return false
	}
	options, _ := req["prompt_cache_options"].(map[string]any)
	if options == nil {
		options = map[string]any{}
		req["prompt_cache_options"] = options
	}
	if _, exists := options["ttl"]; !exists {
		options["ttl"] = "30m"
	}
	delete(req, "prompt_cache_retention")
	return true
}

// normalizeOpenAIAstraRequest applies Astra's documented wire contract at the
// final OpenAI boundary. It does not alter other providers or conversation items
// (including configuration_update, async tools, and encrypted reasoning).
func normalizeOpenAIAstraRequest(account *Account, body []byte) ([]byte, bool, error) {
	if account == nil || !account.IsOpenAI() || !isOpenAIGPT6AstraModel(gjson.GetBytes(body, "model").String()) {
		return body, false, nil
	}
	// Valid native requests are the common path: preserve their original bytes
	// and avoid copying the full (potentially 1M-context) conversation.
	needsNormalization := false
	if account.IsOpenAIOAuthLike() {
		needsNormalization = gjson.GetBytes(body, "prompt_cache_options").Exists()
	}
	for _, key := range []string{"temperature", "top_p", "top_logprobs", "logprobs", "prompt_cache_retention"} {
		needsNormalization = needsNormalization || gjson.GetBytes(body, key).Exists()
	}
	for _, key := range []string{"reasoning.effort", "reasoning_effort"} {
		effort := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, key).String()))
		needsNormalization = needsNormalization || effort == "none" || effort == "minimal"
	}
	for _, value := range gjson.GetBytes(body, "include").Array() {
		needsNormalization = needsNormalization || value.String() == "message.output_text.logprobs"
	}
	if !needsNormalization {
		return body, false, nil
	}
	var req map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &req); err != nil {
		return body, false, err
	}
	changed := false
	if account.IsOpenAIOAuthLike() {
		// Verified Codex OAuth backend rejects the public API cache options.
		for _, key := range []string{"prompt_cache_options", "prompt_cache_retention"} {
			if _, exists := req[key]; exists {
				delete(req, key)
				changed = true
			}
		}
	} else {
		changed = normalizeOpenAIAstraLegacyCacheOptions(req)
	}
	for _, key := range []string{"temperature", "top_p", "top_logprobs", "logprobs", "prompt_cache_retention"} {
		if _, ok := req[key]; ok {
			delete(req, key)
			changed = true
		}
	}
	for _, key := range []string{"reasoning_effort", "effort"} {
		target := req
		if key == "effort" {
			target, _ = req["reasoning"].(map[string]any)
		}
		if value, ok := target[key].(string); ok {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "none", "minimal":
				target[key] = "low"
				changed = true
			}
		}
	}
	if include, ok := req["include"].([]any); ok {
		filtered := make([]any, 0, len(include))
		for _, value := range include {
			if value == "message.output_text.logprobs" {
				changed = true
				continue
			}
			filtered = append(filtered, value)
		}
		if len(filtered) != len(include) {
			req["include"] = filtered
		}
	}
	if !changed {
		return body, false, nil
	}
	result, err := json.Marshal(req)
	if err != nil {
		return body, false, fmt.Errorf("normalize Astra request: %w", err)
	}
	return result, true, nil
}
