package service

import "strings"

// Synthetic tool/task IDs are already scoped to one billable operation. An
// ingress request ID may cover several tools or polls and must not replace them.
func isForcedUsageBillingRequestID(id string) bool {
	id = strings.TrimSpace(id)
	for _, prefix := range []string{"web_search:", "grok-video:", "grok_audio:", "grok_realtime:"} {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}
