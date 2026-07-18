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
