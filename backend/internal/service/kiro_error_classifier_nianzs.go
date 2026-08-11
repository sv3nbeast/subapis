// Source-faithful, namespaced integration of kiro_error_classifier.go from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.
// Only package identifiers and the kiro package import are rewritten so the
// legacy engine remains available for an immediate rollback.

package service

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	nianzsKiroErrorAuthError              = "auth_error"
	nianzsKiroErrorMonthlyRequest         = "monthly_request_count"
	nianzsKiroErrorProfileError           = "profile_error"
	nianzsKiroErrorQuotaExhausted         = "quota_exhausted"
	nianzsKiroErrorOverageExhausted       = "overage_exhausted"
	nianzsKiroErrorRateLimited            = "rate_limited"
	nianzsKiroErrorSuspended              = "suspended"
	nianzsKiroErrorUsageForbidden         = "usage_forbidden"
	nianzsKiroErrorUpstreamTransient      = "upstream_transient"
	nianzsKiroErrorBadRequestSchema       = "bad_request_schema"
	nianzsKiroErrorBadRequestToolPairing  = "bad_request_tool_pairing"
	nianzsKiroErrorBadRequestInvalidModel = "bad_request_invalid_model"
	nianzsKiroErrorBadRequestAuth         = "bad_request_auth"
	nianzsKiroErrorBadRequestQuota        = "bad_request_quota"
	nianzsKiroErrorBadRequestUnknown      = "bad_request_unknown"
	nianzsKiroErrorRefreshTokenInvalid    = "refresh_token_invalid"

	nianzsKiroQuotaStateNormal           = "normal"
	nianzsKiroQuotaStateOverageActive    = "overage_active"
	nianzsKiroQuotaStateCreditsExhausted = "credits_exhausted"
	nianzsKiroQuotaStateOverageExhausted = "overage_exhausted"
)

type nianzsKiroErrorClassification struct {
	Category   string
	StatusCode int
	Message    string
}

func nianzsClassifyKiroHTTPError(statusCode int, body string) nianzsKiroErrorClassification {
	trimmed := strings.TrimSpace(body)
	lower := strings.ToLower(trimmed)

	switch {
	case statusCode == http.StatusUnauthorized:
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorAuthError, StatusCode: statusCode, Message: trimmed}
	case statusCode == http.StatusPaymentRequired && nianzsLooksLikeKiroMonthlyRequestCountError(trimmed):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorMonthlyRequest, StatusCode: statusCode, Message: trimmed}
	case statusCode == http.StatusForbidden && nianzsIsKiroSuspendedBody([]byte(trimmed)):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorSuspended, StatusCode: statusCode, Message: trimmed}
	case nianzsLooksLikeKiroProfileError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorProfileError, StatusCode: statusCode, Message: trimmed}
	case statusCode == http.StatusBadRequest:
		return nianzsClassifyKiroBadRequest(trimmed, lower)
	case statusCode == http.StatusForbidden && nianzsIsKiroTokenErrorBody([]byte(trimmed)):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorAuthError, StatusCode: statusCode, Message: trimmed}
	case nianzsLooksLikeKiroOverageExhaustedError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorOverageExhausted, StatusCode: statusCode, Message: trimmed}
	case nianzsLooksLikeKiroQuotaExhaustedError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorQuotaExhausted, StatusCode: statusCode, Message: trimmed}
	case statusCode == http.StatusTooManyRequests:
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorRateLimited, StatusCode: statusCode, Message: trimmed}
	case statusCode == http.StatusForbidden:
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorUsageForbidden, StatusCode: statusCode, Message: trimmed}
	case statusCode >= 500:
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorUpstreamTransient, StatusCode: statusCode, Message: trimmed}
	default:
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorUpstreamTransient, StatusCode: statusCode, Message: trimmed}
	}
}

func nianzsClassifyKiroError(err error) nianzsKiroErrorClassification {
	if err == nil {
		return nianzsKiroErrorClassification{}
	}

	var httpErr *nianzsKiroUsageHTTPError
	if errors.As(err, &httpErr) && httpErr != nil {
		return nianzsClassifyKiroHTTPError(httpErr.StatusCode, httpErr.Body)
	}

	errStr := strings.TrimSpace(err.Error())
	lower := strings.ToLower(errStr)
	switch {
	case nianzsLooksLikeKiroInvalidGrantError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorRefreshTokenInvalid, Message: errStr}
	case nianzsLooksLikeKiroMonthlyRequestCountError(errStr):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorMonthlyRequest, Message: errStr}
	case nianzsLooksLikeKiroProfileError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorProfileError, Message: errStr}
	case nianzsLooksLikeKiroOverageExhaustedError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorOverageExhausted, Message: errStr}
	case nianzsLooksLikeKiroQuotaExhaustedError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorQuotaExhausted, Message: errStr}
	case strings.Contains(lower, "context deadline exceeded"),
		strings.Contains(lower, "timeout"),
		nianzsIsNetErr(err):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorUpstreamTransient, Message: errStr}
	default:
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorUpstreamTransient, Message: errStr}
	}
}

func nianzsClassifyKiroBadRequest(trimmed, lower string) nianzsKiroErrorClassification {
	switch {
	case nianzsLooksLikeKiroBadRequestSchemaError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorBadRequestSchema, StatusCode: http.StatusBadRequest, Message: trimmed}
	case nianzsLooksLikeKiroBadRequestToolPairingError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorBadRequestToolPairing, StatusCode: http.StatusBadRequest, Message: trimmed}
	case nianzsLooksLikeKiroBadRequestInvalidModelError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorBadRequestInvalidModel, StatusCode: http.StatusBadRequest, Message: trimmed}
	case nianzsLooksLikeKiroInvalidGrantError(lower) || nianzsLooksLikeKiroBadRequestAuthError(lower):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorBadRequestAuth, StatusCode: http.StatusBadRequest, Message: trimmed}
	case nianzsLooksLikeKiroQuotaExhaustedError(lower) || nianzsLooksLikeKiroMonthlyRequestCountError(trimmed):
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorBadRequestQuota, StatusCode: http.StatusBadRequest, Message: trimmed}
	default:
		return nianzsKiroErrorClassification{Category: nianzsKiroErrorBadRequestUnknown, StatusCode: http.StatusBadRequest, Message: trimmed}
	}
}

func nianzsLooksLikeKiroBadRequestSchemaError(lower string) bool {
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "schema") ||
		strings.Contains(lower, "inputschema") ||
		strings.Contains(lower, "improperly formed request") ||
		strings.Contains(lower, "additionalproperties") ||
		(strings.Contains(lower, "properties") && strings.Contains(lower, "required"))
}

func nianzsLooksLikeKiroBadRequestToolPairingError(lower string) bool {
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "tool_use") ||
		strings.Contains(lower, "tool_result") ||
		strings.Contains(lower, "tooluseid") ||
		strings.Contains(lower, "toolresults") ||
		strings.Contains(lower, "must be paired") ||
		strings.Contains(lower, "missing tool result")
}

func nianzsLooksLikeKiroBadRequestInvalidModelError(lower string) bool {
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "invalid model") ||
		strings.Contains(lower, "invalid_model_id") ||
		strings.Contains(lower, "model not supported") ||
		strings.Contains(lower, "unsupportedmodel") ||
		strings.Contains(lower, "modelid")
}

func nianzsLooksLikeKiroBadRequestAuthError(lower string) bool {
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "invalid token") ||
		strings.Contains(lower, "expired token") ||
		strings.Contains(lower, "access token") ||
		strings.Contains(lower, "refresh token")
}

func nianzsLooksLikeKiroInvalidGrantError(lower string) bool {
	return strings.Contains(lower, "invalid_grant")
}

func nianzsLooksLikeKiroMonthlyRequestCountError(body string) bool {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "MONTHLY_REQUEST_COUNT") {
		return true
	}
	if !gjson.Valid(trimmed) {
		return false
	}
	return gjson.Get(trimmed, "reason").String() == "MONTHLY_REQUEST_COUNT" ||
		gjson.Get(trimmed, "error.reason").String() == "MONTHLY_REQUEST_COUNT"
}

func nianzsLooksLikeKiroProfileError(lower string) bool {
	if lower == "" {
		return false
	}
	return (strings.Contains(lower, "profilearn") && strings.Contains(lower, "required")) ||
		(strings.Contains(lower, "profile arn") && strings.Contains(lower, "required")) ||
		(strings.Contains(lower, "profile") && strings.Contains(lower, "not found")) ||
		(strings.Contains(lower, "invalid profile")) ||
		(strings.Contains(lower, "listavailableprofiles"))
}

func nianzsLooksLikeKiroQuotaExhaustedError(lower string) bool {
	if lower == "" {
		return false
	}
	return (strings.Contains(lower, "credit") && (strings.Contains(lower, "exhaust") || strings.Contains(lower, "depleted"))) ||
		(strings.Contains(lower, "quota") && (strings.Contains(lower, "exhaust") || strings.Contains(lower, "exceeded") || strings.Contains(lower, "depleted"))) ||
		(strings.Contains(lower, "usage limit") && (strings.Contains(lower, "reached") || strings.Contains(lower, "exceeded"))) ||
		(strings.Contains(lower, "resource has been exhausted"))
}

func nianzsLooksLikeKiroOverageExhaustedError(lower string) bool {
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "overage") &&
		(strings.Contains(lower, "exhaust") ||
			strings.Contains(lower, "disabled") ||
			strings.Contains(lower, "not enabled") ||
			strings.Contains(lower, "not allowed") ||
			strings.Contains(lower, "limit"))
}

func nianzsIsNetErr(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr)
}
