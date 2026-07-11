package middleware

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ClaudeCodeOnlyEndpointRestriction prevents restricted groups from using
// inference endpoints that the Claude Code client does not use.
func ClaudeCodeOnlyEndpointRestriction() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.Group == nil || !apiKey.Group.ClaudeCodeOnly {
			c.Next()
			return
		}
		if isClaudeCodeOnlyAllowedEndpoint(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}

		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"type":    "permission_error",
				"message": "This group is restricted to Claude Code clients (/v1/messages only)",
			},
		})
	}
}

func isClaudeCodeOnlyAllowedEndpoint(method, path string) bool {
	switch {
	case method == http.MethodPost && path == "/v1/messages":
		return true
	case method == http.MethodPost && path == "/v1/messages/count_tokens":
		return true
	case method == http.MethodGet && (path == "/v1/models" || path == "/v1/usage"):
		return true
	default:
		return false
	}
}
