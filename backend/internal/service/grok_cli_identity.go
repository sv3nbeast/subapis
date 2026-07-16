package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const grokPromptCacheIdentityPrefix = "s2-grok-v1-"

const grokPromptCacheSeedMaxBytes = 1024

// grokCLIRequestMetadata maps Sub2API's tenant-isolated session identity to the
// header family emitted by the official Grok Build CLI.
func grokCLIRequestMetadata(c *gin.Context, account *Account, body []byte, model string) xai.CLIRequestMetadata {
	metadata := xai.CLIRequestMetadata{Model: strings.TrimSpace(model)}
	if account != nil {
		metadata.AccountID = account.ID
		metadata.UserID = strings.TrimSpace(firstNonEmpty(
			account.GetCredential("user_id"),
			account.GetCredential("principal_id"),
		))
	}
	if seed := grokExplicitSessionSeed(c, body); seed != "" {
		metadata.ConversationID = grokPromptCacheIdentity(c, model, "headers", seed)
	}
	return metadata
}

func grokPromptCacheIdentity(c *gin.Context, model, operation, seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return ""
	}
	if strings.HasPrefix(seed, grokPromptCacheIdentityPrefix) {
		return seed
	}
	apiKeyID := getAPIKeyIDFromContext(c)
	if apiKeyID <= 0 {
		return ""
	}
	source := fmt.Sprintf("sub2api:grok:prompt-cache:v1:%d:%s:%s:%s", apiKeyID, strings.ToLower(strings.TrimSpace(model)), strings.TrimSpace(operation), seed)
	digest := sha256.Sum256([]byte(source))
	return grokPromptCacheIdentityPrefix + hex.EncodeToString(digest[:16])
}

func injectGrokPromptCacheIdentity(c *gin.Context, body []byte, model, operation, explicit string) ([]byte, string, error) {
	seed := strings.TrimSpace(explicit)
	if seed == "" {
		seed = grokExplicitSessionSeed(c, body)
	}
	identity := grokPromptCacheIdentity(c, model, operation, seed)
	if identity == "" {
		return body, "", nil
	}
	updated, err := sjson.SetBytes(body, "prompt_cache_key", identity)
	if err != nil {
		return nil, "", err
	}
	return updated, identity, nil
}

func grokExplicitSessionSeed(c *gin.Context, body []byte) string {
	return grokPromptCacheSeedFromRequest(c, body)
}

// grokPromptCacheSeedFromRequest extracts the same stable Claude/OpenAI session
// signals understood by Grok Build clients. Free Build caching is keyed by the
// conversation identity, so generating a new x-grok-conv-id for every turn
// makes cache hits appear randomly and then disappear on the next request.
func grokPromptCacheSeedFromRequest(c *gin.Context, body []byte) string {
	if c != nil && c.Request != nil {
		for _, header := range []string{"X-Claude-Code-Session-Id", "session_id", "conversation_id"} {
			if seed := normalizeGrokPromptCacheSeed(c.GetHeader(header)); seed != "" {
				return seed
			}
		}
	}
	for _, path := range []string{"prompt_cache_key", "metadata.session_id", "metadata.sessionId"} {
		if seed := normalizeGrokPromptCacheSeed(gjson.GetBytes(body, path).String()); seed != "" {
			return seed
		}
	}
	if rawUserID := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()); rawUserID != "" {
		if parsed := ParseMetadataUserID(rawUserID); parsed != nil {
			if seed := normalizeGrokPromptCacheSeed(parsed.SessionID); seed != "" {
				return seed
			}
		}
		if gjson.Valid(rawUserID) {
			for _, path := range []string{"session_id", "sessionId"} {
				if seed := normalizeGrokPromptCacheSeed(gjson.Get(rawUserID, path).String()); seed != "" {
					return seed
				}
			}
		}
		if marker := strings.LastIndex(rawUserID, "_session_"); marker >= 0 {
			if seed := normalizeGrokPromptCacheSeed(rawUserID[marker+len("_session_"):]); seed != "" {
				return seed
			}
		}
	}

	// Clients such as OpenCode do not always send an explicit session field.
	// Hash the stable model/system/tools/first-user prefix used by the scheduler
	// so subsequent turns in the same conversation keep one upstream cache key.
	if contentSeed := deriveOpenAIContentSessionSeed(body); contentSeed != "" {
		digest := sha256.Sum256([]byte(contentSeed))
		return "grok-content-" + hex.EncodeToString(digest[:16])
	}
	return ""
}

func normalizeGrokPromptCacheSeed(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" || len(seed) > grokPromptCacheSeedMaxBytes {
		return ""
	}
	return seed
}

func grokPromptCacheKeyFromBody(body []byte) string {
	return strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
}
