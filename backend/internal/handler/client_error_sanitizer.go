package handler

import (
	"net/http"
	"strings"
)

const (
	clientUpstreamTemporarilyUnavailableMessage = "Upstream service temporarily unavailable, please retry later"
	clientUpstreamTemporarilyRateLimitedMessage = "Upstream service is temporarily rate limited, please retry later"
)

// sanitizeClientErrorMessage prevents internal provider identifiers from being
// exposed through HTTP, SSE, or WebSocket error responses.
func sanitizeClientErrorMessage(status int, message string) string {
	if !strings.Contains(strings.ToLower(message), "kiro") {
		return message
	}
	if status == http.StatusTooManyRequests {
		return clientUpstreamTemporarilyRateLimitedMessage
	}
	return clientUpstreamTemporarilyUnavailableMessage
}
