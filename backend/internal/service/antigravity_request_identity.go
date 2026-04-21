package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	antigravityCloudCodeSessionIDExtraKey = "cloud_code_session_id"
	antigravityLineageFallbackKey         = "__fallback__"
	antigravityLineageTTL                 = 24 * time.Hour
	antigravityLineageCleanupInterval     = time.Hour
)

type antigravityRequestIdentity struct {
	SessionID       string
	RequestID       string
	UserAgent       string
	ConversationKey string
}

type antigravityRequestLineage struct {
	UUID      string
	Seq       uint64
	UpdatedAt time.Time
}

type antigravityRequestLineageStore struct {
	mu            sync.Mutex
	lineages      map[string]*antigravityRequestLineage
	lastCleanedAt time.Time
}

func newAntigravityRequestLineageStore() *antigravityRequestLineageStore {
	return &antigravityRequestLineageStore{
		lineages: make(map[string]*antigravityRequestLineage),
	}
}

func (s *antigravityRequestLineageStore) nextRequestID(accountID int64, conversationKey string, now time.Time) string {
	if s == nil {
		return fmt.Sprintf("agent/%d/%s/%d", now.UnixMilli(), antigravityLineageFallbackKey, 1)
	}
	if now.IsZero() {
		now = time.Now()
	}
	normalizedKey := strings.TrimSpace(conversationKey)
	if normalizedKey == "" {
		normalizedKey = antigravityLineageFallbackKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastCleanedAt.IsZero() || now.Sub(s.lastCleanedAt) >= antigravityLineageCleanupInterval {
		s.cleanupLocked(now)
		s.lastCleanedAt = now
	}

	storeKey := fmt.Sprintf("%d::%s", accountID, normalizedKey)
	lineage := s.lineages[storeKey]
	if lineage == nil {
		lineage = &antigravityRequestLineage{
			UUID:      generateAntigravityConversationUUID(),
			UpdatedAt: now,
		}
		s.lineages[storeKey] = lineage
	}

	lineage.Seq++
	lineage.UpdatedAt = now
	return fmt.Sprintf("agent/%d/%s/%d", now.UnixMilli(), lineage.UUID, lineage.Seq)
}

func (s *antigravityRequestLineageStore) cleanupLocked(now time.Time) {
	expireBefore := now.Add(-antigravityLineageTTL)
	for key, lineage := range s.lineages {
		if lineage == nil || lineage.UpdatedAt.Before(expireBefore) {
			delete(s.lineages, key)
		}
	}
}

func generateAntigravityConversationUUID() string {
	return uuid.New().String()
}

func generateAntigravityCloudCodeSessionID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		sum := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
		copy(buf, sum[:8])
	}
	unsigned := binary.BigEndian.Uint64(buf)
	signed := int64(unsigned)
	return strconv.FormatInt(signed, 10)
}

func stableAntigravityConversationSeed(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	n := int64(binary.BigEndian.Uint64(sum[:8])) & 0x7FFFFFFFFFFFFFFF
	return "-" + strconv.FormatInt(n, 10)
}

func extractFirstClaudeUserText(messages []antigravity.ClaudeMessage) string {
	for _, message := range messages {
		if message.Role != "user" || len(message.Content) == 0 {
			continue
		}

		var text string
		if err := json.Unmarshal(message.Content, &text); err == nil {
			if strings.TrimSpace(text) != "" {
				return text
			}
			continue
		}

		gjson.ParseBytes(message.Content).ForEach(func(_, value gjson.Result) bool {
			if value.Get("type").String() == "text" {
				text = strings.TrimSpace(value.Get("text").String())
				return text == ""
			}
			return true
		})
		if text != "" {
			return text
		}
	}
	return ""
}

func extractConversationKeyFromHeaders(c *gin.Context) string {
	if c == nil {
		return ""
	}
	candidates := []string{
		c.GetHeader("session_id"),
		c.GetHeader("session-id"),
		c.GetHeader("conversation_id"),
		c.GetHeader("conversation-id"),
		c.GetHeader("X-Claude-Code-Session-Id"),
	}
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func extractConversationKeyFromGeminiBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	candidates := []string{
		strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()),
		strings.TrimSpace(gjson.GetBytes(body, "request.sessionId").String()),
		strings.TrimSpace(gjson.GetBytes(body, "sessionId").String()),
		strings.TrimSpace(gjson.GetBytes(body, "requestId").String()),
	}
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}

	gjson.ParseBytes(body).Get("request.contents").ForEach(func(_, value gjson.Result) bool {
		if value.Get("role").String() != "user" {
			return true
		}
		text := strings.TrimSpace(value.Get("parts.0.text").String())
		candidates = append(candidates, stableAntigravityConversationSeed(text))
		return false
	})
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func deriveAntigravityConversationKey(c *gin.Context, body []byte, claudeReq *antigravity.ClaudeRequest) string {
	if claudeReq != nil && claudeReq.Metadata != nil {
		if userID := strings.TrimSpace(claudeReq.Metadata.UserID); userID != "" {
			return userID
		}
	}

	if headerKey := extractConversationKeyFromHeaders(c); headerKey != "" {
		return headerKey
	}

	if bodyKey := extractConversationKeyFromGeminiBody(body); bodyKey != "" {
		return bodyKey
	}

	if claudeReq != nil {
		if seed := stableAntigravityConversationSeed(extractFirstClaudeUserText(claudeReq.Messages)); seed != "" {
			return seed
		}
	}

	return antigravityLineageFallbackKey
}

func (s *AntigravityGatewayService) ensureCloudCodeSessionID(ctx context.Context, account *Account) string {
	if account == nil {
		return generateAntigravityCloudCodeSessionID()
	}
	worker := s.antigravityWorker(account)
	if existing := strings.TrimSpace(worker.getSessionID()); existing != "" {
		return existing
	}
	if existing := strings.TrimSpace(account.GetExtraString(antigravityCloudCodeSessionIDExtraKey)); existing != "" {
		worker.setSessionID(existing)
		return existing
	}

	s.requestIdentityMu.Lock()
	defer s.requestIdentityMu.Unlock()

	if existing := strings.TrimSpace(account.GetExtraString(antigravityCloudCodeSessionIDExtraKey)); existing != "" {
		return existing
	}

	generated := generateAntigravityCloudCodeSessionID()
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	account.Extra[antigravityCloudCodeSessionIDExtraKey] = generated

	if s.accountRepo != nil && account.ID > 0 {
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
			antigravityCloudCodeSessionIDExtraKey: generated,
		}); err != nil {
			logger.LegacyPrintf("service.antigravity_gateway", "persist cloud code session id failed: account=%d err=%v", account.ID, err)
		}
	}

	worker.setSessionID(generated)
	return generated
}

func (s *AntigravityGatewayService) buildCloudCodeRequestIdentity(
	ctx context.Context,
	account *Account,
	c *gin.Context,
	body []byte,
	claudeReq *antigravity.ClaudeRequest,
) antigravityRequestIdentity {
	now := time.Now()
	conversationKey := deriveAntigravityConversationKey(c, body, claudeReq)
	sessionID := s.ensureCloudCodeSessionID(ctx, account)
	worker := s.antigravityWorker(account)
	requestID := fmt.Sprintf("agent/%d/%s/%d", now.UnixMilli(), antigravityLineageFallbackKey, 1)
	if worker != nil {
		requestID = worker.nextRequestID(conversationKey, now)
	} else if s.requestLineage != nil {
		requestID = s.requestLineage.nextRequestID(account.ID, conversationKey, now)
	}
	return antigravityRequestIdentity{
		SessionID:       sessionID,
		RequestID:       requestID,
		UserAgent:       "antigravity",
		ConversationKey: conversationKey,
	}
}

func (s *AntigravityGatewayService) buildFreshSessionRecoveryIdentity(account *Account, conversationKey string) antigravityRequestIdentity {
	now := time.Now()
	worker := s.antigravityWorker(account)
	if worker != nil {
		worker.touch(now)
	}
	return antigravityRequestIdentity{
		SessionID:       generateAntigravityCloudCodeSessionID(),
		RequestID:       fmt.Sprintf("agent/%d/%s/%d", now.UnixMilli(), generateAntigravityConversationUUID(), 1),
		UserAgent:       "antigravity",
		ConversationKey: strings.TrimSpace(conversationKey),
	}
}
