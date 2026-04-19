package service

import "strings"

// opsExcludeUnauthenticatedResponsesProbeClause mirrors the repository-side SLA
// exclusion and is used by the realtime collector, which lives in the service
// package and cannot import repository helpers without a cycle.
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
