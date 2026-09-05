package service

import (
	"github.com/tidwall/gjson"
	"strings"
)

// Account affinity is model-scoped; upstream prompt-cache identity is separate.
func grokStickyAffinitySeed(sessionID string, body []byte) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	model := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "model").String()))
	if model == "" {
		return "grok-affinity:v1:" + sessionID
	}
	return "grok-affinity:v1:" + model + ":" + sessionID
}
