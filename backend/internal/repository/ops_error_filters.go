package repository

import "strings"

// opsExcludeUnauthenticatedResponsesProbeClause excludes unauthenticated OpenAI
// Responses probes from SLA/error statistics.
//
// These requests have all identity columns empty and fail before auth
// resolution, so counting them in ops_error_logs would pollute OpenAI SLA even
// though they are not attributable to any authenticated user/account request.
func opsExcludeUnauthenticatedResponsesProbeClause(alias string) string {
	prefix := opsSQLFieldPrefix(alias)
	return `NOT (
COALESCE(` + prefix + `status_code, 0) = 401
AND COALESCE(` + prefix + `user_id, 0) = 0
AND COALESCE(` + prefix + `api_key_id, 0) = 0
AND COALESCE(` + prefix + `account_id, 0) = 0
AND (
  COALESCE(` + prefix + `request_path, '') = '/responses'
  OR COALESCE(` + prefix + `request_path, '') LIKE '/responses/%'
  OR COALESCE(` + prefix + `request_path, '') = '/v1/responses'
  OR COALESCE(` + prefix + `request_path, '') LIKE '/v1/responses/%'
)
)`
}

func opsSQLFieldPrefix(alias string) string {
	trimmed := strings.TrimSpace(alias)
	if trimmed == "" {
		return ""
	}
	if strings.HasSuffix(trimmed, ".") {
		return trimmed
	}
	return trimmed + "."
}
