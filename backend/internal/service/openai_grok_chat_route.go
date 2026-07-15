package service

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"

	"github.com/gin-gonic/gin"
)

func shouldUseGrokResponsesForChat(c *gin.Context, body []byte) bool {
	apiKey := getAPIKeyFromContext(c)
	group := apiKeyGroup(apiKey)
	if apiKey == nil || group == nil || group.Platform != PlatformGrok {
		return false
	}

	switch group.EffectiveGrokChatUpstreamMode() {
	case GrokChatUpstreamModeResponses:
		return true
	case GrokChatUpstreamModeGray:
		percent := group.EffectiveGrokChatResponsesGrayPercent()
		if percent <= 0 {
			return false
		}
		if percent >= 100 {
			return true
		}

		seed := explicitOpenAISessionID(c, body)
		if seed == "" {
			seed = deriveOpenAIContentSessionSeed(body)
		}
		if seed == "" {
			return false
		}
		digest := sha256.Sum256([]byte(strconv.FormatInt(apiKey.ID, 10) + "\x00" + seed))
		bucket := int(binary.BigEndian.Uint64(digest[:8]) % 100)
		return bucket < percent
	default:
		return false
	}
}
