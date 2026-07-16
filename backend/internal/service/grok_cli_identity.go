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
	if c != nil && c.Request != nil {
		return explicitOpenAISessionID(c, body)
	}
	return grokPromptCacheKeyFromBody(body)
}

func grokPromptCacheKeyFromBody(body []byte) string {
	return strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
}
