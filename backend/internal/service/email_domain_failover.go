package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

func NormalizeEmailDomainSuffix(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.TrimPrefix(raw, "@")
	return raw
}

func normalizeEmailDomainSuffixes(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := NormalizeEmailDomainSuffix(value)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func EmailDomainSuffixFromEmail(email string) string {
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return NormalizeEmailDomainSuffix(email[at+1:])
}

func (a *Account) EmailDomainSuffix() string {
	if a == nil {
		return ""
	}
	return EmailDomainSuffixFromEmail(a.GetCredential("email"))
}

func ShouldPreferDifferentEmailDomainSuffixForFailover(platform string, failoverErr *UpstreamFailoverError) bool {
	if platform != PlatformAntigravity || failoverErr == nil || len(failoverErr.ResponseBody) == 0 {
		return false
	}
	switch failoverErr.StatusCode {
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		return isAntigravityCapacityOrQuotaExhaustedBody(failoverErr.ResponseBody)
	default:
		return false
	}
}

func isAntigravityCapacityOrQuotaExhaustedBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	parsed := gjson.ParseBytes(body)
	message := strings.ToLower(strings.TrimSpace(parsed.Get("error.message").String()))
	if strings.Contains(message, "no capacity available for model") ||
		strings.Contains(message, "exhausted your capacity on this model") {
		return true
	}

	for _, detail := range parsed.Get("error.details").Array() {
		if strings.EqualFold(strings.TrimSpace(detail.Get("reason").String()), googleRPCReasonModelCapacityExhausted) {
			return true
		}
	}

	return strings.Contains(strings.ToUpper(string(body)), googleRPCReasonModelCapacityExhausted)
}
