package service

import (
	"net/http"
	"strings"
)

var upstreamModelNotFoundKeywords = []string{"model not found", "unknown model", "not found"}

// isUpstreamAccountModelUnsupportedError identifies the two provider responses
// that mean the selected credential cannot serve this model. Keep this narrow:
// ordinary 400 request validation errors must remain client errors rather than
// triggering account rotation.
func isUpstreamAccountModelUnsupportedError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}

	normalized := normalizeModelNotFoundBody(body)
	return isUpstreamChatGPTCodexModelUnsupportedErrorNormalized(normalized) ||
		strings.Contains(normalized, "requested model is not supported by this api key/group")
}

// IsUpstreamAccountModelUnsupportedError exposes the narrow capability-error
// classifier to gateway handlers that need to preserve the client-error
// contract after a multi-account failover loop is exhausted.
func IsUpstreamAccountModelUnsupportedError(statusCode int, body []byte) bool {
	return isUpstreamAccountModelUnsupportedError(statusCode, body)
}

func isUpstreamChatGPTCodexModelUnsupportedError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	return isUpstreamChatGPTCodexModelUnsupportedErrorNormalized(normalizeModelNotFoundBody(body))
}

func isUpstreamChatGPTCodexModelUnsupportedErrorNormalized(normalized string) bool {
	return strings.Contains(normalized, "model is not supported when using codex with a chatgpt account")
}

func isUpstreamModelUnavailableError(statusCode int, body []byte) bool {
	return isUpstreamModelNotFoundError(statusCode, body) ||
		isUpstreamAccountModelUnsupportedError(statusCode, body)
}

func isUpstreamModelNotFoundError(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	normalized := normalizeModelNotFoundBody(body)
	if normalized == "" || !strings.Contains(normalized, "model") {
		return false
	}
	return containsModelNotFoundKeyword(normalized)
}

func isModelNotFoundError(statusCode int, body []byte) bool {
	return isUpstreamModelNotFoundError(statusCode, body) || statusCode == http.StatusNotFound
}

// openAICodexPlanGatedModelPhrase matches the deterministic Codex 400 returned
// when a ChatGPT OAuth account's plan cannot serve the requested model, e.g.
// {"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}
// The phrase is compared against the normalized body (lowercased, "_"/"-"
// folded to spaces), so it also matches the same message embedded in
// error.message-style payloads.
const openAICodexPlanGatedModelPhrase = "model is not supported when using codex"

// isOpenAICodexPlanGatedModelError reports whether the upstream response is the
// deterministic Codex rejection of a plan-gated model on a ChatGPT account.
// Unlike transient failures, retrying the same account cannot succeed until the
// account's plan changes, so callers should treat it like model-not-found and
// cool the (account, model) pair down instead of re-selecting the account.
func isOpenAICodexPlanGatedModelError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	normalized := normalizeModelNotFoundBody(body)
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, openAICodexPlanGatedModelPhrase)
}

func containsModelNotFoundKeyword(normalizedBody string) bool {
	if normalizedBody == "" {
		return false
	}
	for _, keyword := range upstreamModelNotFoundKeywords {
		if strings.Contains(normalizedBody, keyword) {
			return true
		}
	}
	return false
}

func normalizeModelNotFoundBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	normalized := strings.ToLower(string(body))
	normalized = strings.NewReplacer("_", " ", "-", " ", "\n", " ", "\r", " ", "\t", " ").Replace(normalized)
	return strings.Join(strings.Fields(normalized), " ")
}
