package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

const (
	antigravityToolSignatureCacheTTL             = 21 * 24 * time.Hour
	antigravityToolSignatureCacheCleanupInterval = time.Hour
)

type antigravityToolSignatureCacheEntry struct {
	Signatures map[string]string
	UpdatedAt  time.Time
}

type antigravityToolSignatureCache struct {
	mu            sync.Mutex
	entries       map[string]*antigravityToolSignatureCacheEntry
	lastCleanedAt time.Time
	filePath      string
}

func newAntigravityToolSignatureCache() *antigravityToolSignatureCache {
	return newAntigravityToolSignatureCacheWithPath(resolveAntigravityToolSignatureCachePath())
}

func newAntigravityToolSignatureCacheWithPath(filePath string) *antigravityToolSignatureCache {
	cache := &antigravityToolSignatureCache{
		entries:  make(map[string]*antigravityToolSignatureCacheEntry),
		filePath: strings.TrimSpace(filePath),
	}
	cache.loadFromDisk()
	return cache
}

func (c *antigravityToolSignatureCache) snapshot(accountID int64, conversationKey string, now time.Time) map[string]string {
	if c == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	storeKey := antigravityToolSignatureCacheKey(accountID, conversationKey)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanupIfNeededLocked(now)

	entry := c.entries[storeKey]
	if entry == nil || len(entry.Signatures) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(entry.Signatures))
	for toolID, signature := range entry.Signatures {
		cloned[toolID] = signature
	}
	return cloned
}

func (c *antigravityToolSignatureCache) remember(accountID int64, conversationKey string, signatures map[string]string, now time.Time) {
	if c == nil || len(signatures) == 0 {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	storeKey := antigravityToolSignatureCacheKey(accountID, conversationKey)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanupIfNeededLocked(now)

	entry := c.entries[storeKey]
	if entry == nil {
		entry = &antigravityToolSignatureCacheEntry{
			Signatures: make(map[string]string),
		}
		c.entries[storeKey] = entry
	}

	for toolID, signature := range signatures {
		toolID = strings.TrimSpace(toolID)
		signature = strings.TrimSpace(signature)
		if toolID == "" || signature == "" {
			continue
		}
		entry.Signatures[toolID] = signature
	}
	entry.UpdatedAt = now
	c.persistLocked()
}

func (c *antigravityToolSignatureCache) clear(accountID int64, conversationKey string) {
	if c == nil {
		return
	}
	storeKey := antigravityToolSignatureCacheKey(accountID, conversationKey)

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[storeKey]; ok {
		delete(c.entries, storeKey)
		c.persistLocked()
	}
}

func (c *antigravityToolSignatureCache) cleanupIfNeededLocked(now time.Time) {
	if c.lastCleanedAt.IsZero() || now.Sub(c.lastCleanedAt) >= antigravityToolSignatureCacheCleanupInterval {
		c.cleanupLocked(now)
		c.lastCleanedAt = now
	}
}

func (c *antigravityToolSignatureCache) cleanupLocked(now time.Time) {
	changed := false
	expireBefore := now.Add(-antigravityToolSignatureCacheTTL)
	for key, entry := range c.entries {
		if entry == nil || entry.UpdatedAt.Before(expireBefore) {
			delete(c.entries, key)
			changed = true
		}
	}
	if changed {
		c.persistLocked()
	}
}

func antigravityToolSignatureCacheKey(accountID int64, conversationKey string) string {
	normalizedKey := strings.TrimSpace(conversationKey)
	if normalizedKey == "" {
		normalizedKey = antigravityLineageFallbackKey
	}
	return fmt.Sprintf("%d::%s", accountID, normalizedKey)
}

func resolveAntigravityToolSignatureCachePath() string {
	if dir := strings.TrimSpace(os.Getenv("DATA_DIR")); dir != "" {
		return filepath.Join(dir, "antigravity_tool_signatures.json")
	}
	if info, err := os.Stat("/app/data"); err == nil && info.IsDir() {
		testFile := filepath.Join("/app/data", ".tool_sig_write_test")
		if f, err := os.Create(testFile); err == nil {
			_ = f.Close()
			_ = os.Remove(testFile)
			return filepath.Join("/app/data", "antigravity_tool_signatures.json")
		}
	}
	if cacheDir, err := os.UserCacheDir(); err == nil && strings.TrimSpace(cacheDir) != "" {
		return filepath.Join(cacheDir, "sub2api", "antigravity_tool_signatures.json")
	}
	return "antigravity_tool_signatures.json"
}

func (c *antigravityToolSignatureCache) loadFromDisk() {
	if c == nil || strings.TrimSpace(c.filePath) == "" {
		return
	}

	data, err := os.ReadFile(c.filePath)
	if err != nil || len(data) == 0 {
		return
	}

	var persisted map[string]antigravityToolSignatureCacheEntry
	if err := json.Unmarshal(data, &persisted); err != nil {
		return
	}

	now := time.Now()
	for key, entry := range persisted {
		if entry.UpdatedAt.IsZero() || now.Sub(entry.UpdatedAt) > antigravityToolSignatureCacheTTL || len(entry.Signatures) == 0 {
			continue
		}
		cloned := make(map[string]string, len(entry.Signatures))
		for toolID, signature := range entry.Signatures {
			toolID = strings.TrimSpace(toolID)
			signature = strings.TrimSpace(signature)
			if toolID == "" || signature == "" {
				continue
			}
			cloned[toolID] = signature
		}
		if len(cloned) == 0 {
			continue
		}
		c.entries[key] = &antigravityToolSignatureCacheEntry{
			Signatures: cloned,
			UpdatedAt:  entry.UpdatedAt,
		}
	}
}

func (c *antigravityToolSignatureCache) persistLocked() {
	if c == nil || strings.TrimSpace(c.filePath) == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.filePath), 0o755); err != nil {
		return
	}

	persisted := make(map[string]antigravityToolSignatureCacheEntry, len(c.entries))
	for key, entry := range c.entries {
		if entry == nil || len(entry.Signatures) == 0 {
			continue
		}
		cloned := make(map[string]string, len(entry.Signatures))
		for toolID, signature := range entry.Signatures {
			toolID = strings.TrimSpace(toolID)
			signature = strings.TrimSpace(signature)
			if toolID == "" || signature == "" {
				continue
			}
			cloned[toolID] = signature
		}
		if len(cloned) == 0 {
			continue
		}
		persisted[key] = antigravityToolSignatureCacheEntry{
			Signatures: cloned,
			UpdatedAt:  entry.UpdatedAt,
		}
	}

	if len(persisted) == 0 {
		_ = os.Remove(c.filePath)
		return
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return
	}
	tmpPath := c.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmpPath, c.filePath)
}

func (s *AntigravityGatewayService) getClaudeToolUseSignatures(accountID int64, conversationKey string) map[string]string {
	if s == nil {
		return nil
	}
	worker := s.antigravityWorker(&Account{ID: accountID})
	if worker != nil {
		return worker.toolSignatures(conversationKey, time.Now())
	}
	if s.toolSignatureCache == nil {
		s.requestIdentityMu.Lock()
		if s.toolSignatureCache == nil {
			s.toolSignatureCache = newAntigravityToolSignatureCache()
		}
		s.requestIdentityMu.Unlock()
	}
	return s.toolSignatureCache.snapshot(accountID, conversationKey, time.Now())
}

func (s *AntigravityGatewayService) rememberClaudeToolUseSignatures(accountID int64, conversationKey string, signatures map[string]string) {
	if s == nil || len(signatures) == 0 {
		return
	}
	worker := s.antigravityWorker(&Account{ID: accountID})
	if worker != nil {
		worker.rememberToolSignatures(conversationKey, signatures, time.Now())
		return
	}
	if s.toolSignatureCache == nil {
		s.requestIdentityMu.Lock()
		if s.toolSignatureCache == nil {
			s.toolSignatureCache = newAntigravityToolSignatureCache()
		}
		s.requestIdentityMu.Unlock()
	}
	s.toolSignatureCache.remember(accountID, conversationKey, signatures, time.Now())
}

func (s *AntigravityGatewayService) clearClaudeToolUseSignatures(accountID int64, conversationKey string) {
	if s == nil {
		return
	}
	worker := s.antigravityWorker(&Account{ID: accountID})
	if worker != nil {
		worker.toolSignatureCache.clear(accountID, conversationKey)
		return
	}
	if s.toolSignatureCache == nil {
		return
	}
	s.toolSignatureCache.clear(accountID, conversationKey)
}

func (s *AntigravityGatewayService) rememberClaudeToolUseSignaturesFromBody(accountID int64, conversationKey string, body []byte) {
	signatures, err := antigravity.ExtractToolUseSignaturesFromClaudeResponse(body)
	if err != nil || len(signatures) == 0 {
		return
	}
	s.rememberClaudeToolUseSignatures(accountID, conversationKey, signatures)
}
