package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/anthropictokenizer"
	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

const (
	kiroCacheDefaultTTL          = 5 * time.Minute
	kiroCacheOneHourTTL          = time.Hour
	kiroCacheMaxSupportedTTL     = time.Hour
	kiroTokensPerTool            = 150
	kiroTokensPerMessage         = 4
	kiroCacheMinTokensDefault    = 1024
	kiroCacheMinTokensOpus       = 4096
	kiroCacheMinTokensHaiku3     = 2048
	kiroCachePrefixLookbackLimit = 128
	kiroCachePrefixLookbackMax   = 512
)

type kiroCacheEmulationUsage struct {
	InputTokens                int
	CacheReadInputTokens       int
	CacheCreationInputTokens   int
	CacheCreation5mInputTokens int
	CacheCreation1hInputTokens int
}

type kiroCacheEntry struct {
	tokens    int
	ttl       time.Duration
	expiresAt time.Time
}

type kiroCacheTracker struct {
	mu      sync.Mutex
	entries map[uint64]map[[32]byte]kiroCacheEntry
}

var globalKiroCacheTracker = &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}

func (s *GatewayService) buildKiroCacheEmulationUsage(account *Account, group *Group, body []byte, model string, inputTokens int) *kiroCacheEmulationUsage {
	return s.buildKiroCacheEmulationUsageWithContext(context.Background(), account, group, body, model, inputTokens)
}

func (s *GatewayService) buildKiroCacheEmulationUsageWithContext(ctx context.Context, account *Account, group *Group, body []byte, model string, inputTokens int) *kiroCacheEmulationUsage {
	if account == nil || account.ID <= 0 || !account.IsKiro() || len(body) == 0 {
		return nil
	}
	enabled := account.EffectiveKiroCacheEmulationEnabled()
	ratio := account.GetKiroCacheEmulationRatio()
	if !enabled && group != nil {
		enabled, ratio = kiroGroupCacheEmulationFallback(group)
	}
	if !enabled || ratio <= 0 {
		return nil
	}
	profile, ok := buildKiroCacheProfile(body, model, inputTokens)
	if !ok {
		return nil
	}
	cacheKey := kiroCacheCredentialKey(account)
	if cacheKey == 0 {
		return nil
	}
	stableKey := strings.TrimSpace(kiroCacheCredentialIdentity(account))
	persistence := s.kiroCachePersistenceStore()
	match := globalKiroCacheTracker.match(ctx, stableKey, cacheKey, profile, persistence)
	if persistEntries := globalKiroCacheTracker.update(cacheKey, profile); persistence != nil && stableKey != "" && len(persistEntries) > 0 {
		_ = persistence.UpsertKiroCacheFingerprints(ctx, stableKey, persistEntries)
	}
	result := &kiroCacheEmulationUsage{}
	if match != nil {
		result.CacheReadInputTokens = min(match.cumulativeTokens, profile.totalInputTokens)
	}
	lastBreakpoint := profile.lastCacheableBreakpoint()
	if lastBreakpoint == nil {
		return nil
	}
	lastBreakpointTokens := min(lastBreakpoint.cumulativeTokens, profile.totalInputTokens)
	result.CacheCreationInputTokens = max(lastBreakpointTokens-result.CacheReadInputTokens, 0)
	result.CacheCreation5mInputTokens, result.CacheCreation1hInputTokens = profile.ttlBreakdown(result.CacheReadInputTokens)
	result.CacheReadInputTokens = scaleKiroCacheTokens(result.CacheReadInputTokens, ratio)
	result.CacheCreationInputTokens = scaleKiroCacheTokens(result.CacheCreationInputTokens, ratio)
	result.CacheCreation5mInputTokens = scaleKiroCacheTokens(result.CacheCreation5mInputTokens, ratio)
	result.CacheCreation1hInputTokens = scaleKiroCacheTokens(result.CacheCreation1hInputTokens, ratio)
	result.InputTokens = inputTokens - result.CacheReadInputTokens - result.CacheCreationInputTokens
	if result.InputTokens < 0 {
		result.InputTokens = 0
	}
	if result.CacheReadInputTokens == 0 && result.CacheCreationInputTokens == 0 {
		return nil
	}
	return result
}

func (s *GatewayService) kiroCachePersistenceStore() KiroCachePersistenceStore {
	if s == nil || s.cache == nil {
		return nil
	}
	persistence, _ := s.cache.(KiroCachePersistenceStore)
	return persistence
}

func kiroGroupCacheEmulationFallback(group *Group) (bool, float64) {
	if group == nil || !group.KiroCacheEmulationEnabled {
		return false, 0
	}
	ratio := group.KiroCacheEmulationRatio
	if ratio == 0 {
		ratio = 1
	}
	ratio = normalizeKiroCacheEmulationRatio(ratio)
	return ratio > 0, ratio
}

func scaleKiroCacheTokens(tokens int, ratio float64) int {
	if tokens <= 0 || ratio <= 0 {
		return 0
	}
	if ratio >= 1 {
		return tokens
	}
	return int(math.Round(float64(tokens) * ratio))
}

type kiroCacheProfile struct {
	totalInputTokens int
	minCacheable     int
	blocks           []kiroCacheBlock
	breakpoints      []kiroCacheBreakpoint
	hasExplicitTTL   bool
}

type kiroCacheBlock struct {
	prefixFingerprint [32]byte
	cumulativeTokens  int
}

type kiroCacheBreakpoint struct {
	blockIndex int
	ttl        time.Duration
}

type kiroResolvedBreakpoint struct {
	blockIndex       int
	cumulativeTokens int
	ttl              time.Duration
}

type kiroPendingBlock struct {
	value         any
	tokens        int
	breakpointTTL *time.Duration
	messageIndex  *int
	isMessageEnd  bool
	role          string
	blockType     string
}

type kiroCacheLookupCandidate struct {
	fingerprint      [32]byte
	fingerprintHex   string
	cumulativeTokens int
	ttl              time.Duration
}

func buildKiroCacheProfile(body []byte, model string, inputTokens int) (*kiroCacheProfile, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	blocks := flattenKiroCacheBlocks(payload)
	if len(blocks) == 0 {
		return nil, false
	}
	totalTokens := inputTokens
	if totalTokens <= 0 {
		totalTokens = countKiroInputTokensFromPayload(payload)
	}
	prelude, err := canonicalJSON(map[string]any{
		"model":       payload["model"],
		"tool_choice": payload["tool_choice"],
	})
	if err != nil {
		return nil, false
	}
	prefixState := make([]byte, 8+len(prelude))
	binary.BigEndian.PutUint64(prefixState[:8], uint64(len(prelude)))
	copy(prefixState[8:], prelude)

	profile := &kiroCacheProfile{totalInputTokens: max(totalTokens, 0), minCacheable: kiroMinimumCacheableTokens(model)}
	cumulativeTokens := 0
	var activeTTL *time.Duration
	seenBreakpoints := make(map[int]struct{})
	for index, block := range blocks {
		cumulativeTokens += max(block.tokens, 0)
		blockJSON, err := canonicalJSON(block.value)
		if err != nil {
			return nil, false
		}
		blockHash := sha256.Sum256(blockJSON)
		h := sha256.New()
		_, _ = h.Write(prefixState)
		_, _ = h.Write(blockHash[:])
		prefixFingerprint := [32]byte(h.Sum(nil))
		prefixState = prefixFingerprint[:]
		profile.blocks = append(profile.blocks, kiroCacheBlock{prefixFingerprint: prefixFingerprint, cumulativeTokens: cumulativeTokens})

		if block.breakpointTTL != nil {
			ttl := minDuration(*block.breakpointTTL, kiroCacheMaxSupportedTTL)
			activeTTL = &ttl
			profile.hasExplicitTTL = true
			if _, ok := seenBreakpoints[index]; !ok {
				profile.breakpoints = append(profile.breakpoints, kiroCacheBreakpoint{blockIndex: index, ttl: ttl})
				seenBreakpoints[index] = struct{}{}
			}
		}
		if block.isMessageEnd && block.messageIndex != nil && activeTTL != nil {
			if _, ok := seenBreakpoints[index]; !ok {
				profile.breakpoints = append(profile.breakpoints, kiroCacheBreakpoint{blockIndex: index, ttl: *activeTTL})
				seenBreakpoints[index] = struct{}{}
			}
		}
	}
	if len(profile.breakpoints) == 0 && !profile.hasExplicitTTL {
		profile.addAutomaticBreakpoints(blocks)
	}
	if profile.lastCacheableBreakpoint() == nil {
		return nil, false
	}
	return profile, true
}

func (p *kiroCacheProfile) addAutomaticBreakpoints(blocks []kiroPendingBlock) {
	if p == nil || len(blocks) == 0 || len(p.blocks) != len(blocks) {
		return
	}
	finalMessageIndex := -1
	for _, block := range blocks {
		if block.messageIndex != nil && *block.messageIndex > finalMessageIndex {
			finalMessageIndex = *block.messageIndex
		}
	}

	candidateTTLs := make(map[int]time.Duration, min(len(blocks), kiroCachePrefixLookbackLimit))
	orderedCandidateIndexes := make([]int, 0, min(len(blocks), kiroCachePrefixLookbackLimit))
	addCandidate := func(index int, ttl time.Duration) {
		if index < 0 {
			return
		}
		if _, exists := candidateTTLs[index]; !exists {
			orderedCandidateIndexes = append(orderedCandidateIndexes, index)
		}
		if ttl > candidateTTLs[index] {
			candidateTTLs[index] = ttl
		}
	}
	for i, block := range blocks {
		if p.blocks[i].cumulativeTokens < p.minCacheable {
			continue
		}
		if shouldAddKiroToolContextBreakpoint(block, finalMessageIndex) {
			addCandidate(i, kiroCacheDefaultTTL)
		}
		if block.messageIndex != nil {
			// 自动断点只放在完整消息边界，避免把一条消息的中间内容误当成
			// 可稳定复用前缀；当前最后一条消息通常是本轮新增输入，也不参与
			// 自动缓存。显式 cache_control 仍按用户指定逻辑处理，不受这里影响。
			if !block.isMessageEnd {
				continue
			}
			if finalMessageIndex >= 0 && *block.messageIndex == finalMessageIndex {
				continue
			}
		}
		addCandidate(i, kiroCacheDefaultTTL)
	}
	if len(orderedCandidateIndexes) == 0 {
		for i := len(blocks) - 1; i >= 0; i-- {
			if p.blocks[i].cumulativeTokens >= p.minCacheable {
				addCandidate(i, kiroCacheDefaultTTL)
				break
			}
		}
	}
	limit := kiroCacheEffectiveLookbackLimit(len(orderedCandidateIndexes))
	if len(orderedCandidateIndexes) > limit {
		orderedCandidateIndexes = orderedCandidateIndexes[len(orderedCandidateIndexes)-limit:]
	}
	for _, candidateIndex := range orderedCandidateIndexes {
		if candidateIndex >= 0 {
			p.breakpoints = append(p.breakpoints, kiroCacheBreakpoint{blockIndex: candidateIndex, ttl: candidateTTLs[candidateIndex]})
		}
	}
}

func shouldAddKiroToolContextBreakpoint(block kiroPendingBlock, finalMessageIndex int) bool {
	if block.messageIndex == nil || !isKiroToolContextBlock(block.blockType) {
		return false
	}
	if finalMessageIndex < 0 {
		return true
	}
	// 最后一条消息里的 tool_use/tool_result 若后面还有新的文本块，前面的工具上下文
	// 仍然是稳定历史，可以单独落断点，避免当前 tail 增长导致整段重建。
	if *block.messageIndex == finalMessageIndex {
		return !block.isMessageEnd
	}
	return true
}

func isKiroToolContextBlock(blockType string) bool {
	switch strings.ToLower(strings.TrimSpace(blockType)) {
	case "tool_use", "tool_result", "server_tool_use", "server_tool_result", "mcp_tool_use", "mcp_tool_result":
		return true
	default:
		return false
	}
}

func flattenKiroCacheBlocks(payload map[string]any) []kiroPendingBlock {
	var blocks []kiroPendingBlock
	if tools, ok := payload["tools"].([]any); ok {
		for toolIndex, tool := range tools {
			value := stripKiroCacheControl(tool)
			value = normalizeKiroCacheBlockValue(value)
			blocks = append(blocks, kiroPendingBlock{
				value:  map[string]any{"kind": "tool", "tool_index": toolIndex, "tool": value},
				tokens: kiroTokensPerTool, breakpointTTL: extractKiroCacheTTL(tool), blockType: "tool_definition",
			})
		}
	}
	for systemIndex, systemBlock := range normalizeKiroSystemBlocks(payload["system"]) {
		value := stripKiroCacheControl(systemBlock)
		value = normalizeKiroCacheBlockValue(value)
		canonicalizeKiroSystemBlock(value)
		blocks = append(blocks, kiroPendingBlock{
			value:  map[string]any{"kind": "system", "system_index": systemIndex, "block": value},
			tokens: countKiroSystemBlockTokens(systemBlock), breakpointTTL: extractKiroCacheTTL(systemBlock), blockType: classifyKiroBlockType(value),
		})
	}
	messages, _ := payload["messages"].([]any)
	for messageIndex, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		role, _ := message["role"].(string)
		content := message["content"]
		switch typed := content.(type) {
		case string:
			mi := messageIndex
			block := map[string]any{"type": "text", "text": typed}
			blocks = append(blocks, kiroPendingBlock{
				value:  map[string]any{"kind": "message", "message_index": messageIndex, "role": role, "block_index": 0, "block": block},
				tokens: countKiroMessageContentTokens(block), messageIndex: &mi, isMessageEnd: true, role: role, blockType: "text",
			})
		case []any:
			lastBlockIndex := len(typed) - 1
			for blockIndex, rawBlock := range typed {
				mi := messageIndex
				value := stripKiroCacheControl(rawBlock)
				value = normalizeKiroCacheBlockValue(value)
				blocks = append(blocks, kiroPendingBlock{
					value:  map[string]any{"kind": "message", "message_index": messageIndex, "role": role, "block_index": blockIndex, "block": value},
					tokens: countKiroMessageContentTokens(rawBlock), breakpointTTL: extractKiroCacheTTL(rawBlock), messageIndex: &mi, isMessageEnd: blockIndex == lastBlockIndex, role: role, blockType: classifyKiroBlockType(value),
				})
			}
		}
	}
	return blocks
}

func classifyKiroBlockType(value any) string {
	switch typed := value.(type) {
	case string:
		return "text"
	case map[string]any:
		return strings.ToLower(strings.TrimSpace(kiroCacheAsString(typed["type"])))
	default:
		return ""
	}
}

func normalizeKiroCacheBlockValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		blockType := classifyKiroBlockType(x)
		out := make(map[string]any, len(x))
		for k, child := range x {
			if shouldStripKiroVolatileField(blockType, k) {
				continue
			}
			out[k] = normalizeKiroCacheBlockValue(child)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = normalizeKiroCacheBlockValue(child)
		}
		return out
	default:
		return v
	}
}

func shouldStripKiroVolatileField(blockType string, key string) bool {
	normalizedKey := strings.ToLower(strings.TrimSpace(key))
	switch blockType {
	case "tool_use", "tool_result", "server_tool_use", "server_tool_result", "mcp_tool_use", "mcp_tool_result":
		switch normalizedKey {
		case "id", "tool_use_id", "tooluseid", "call_id", "callid", "request_id", "requestid", "session_id", "sessionid", "invocation_id", "invocationid", "trace_id", "traceid":
			return true
		}
	}
	return false
}

func normalizeKiroSystemBlocks(system any) []any {
	switch typed := system.(type) {
	case nil:
		return nil
	case string:
		return []any{map[string]any{"type": "text", "text": typed}}
	case []any:
		return typed
	default:
		return []any{typed}
	}
}

func canonicalizeKiroSystemBlock(value any) {
	obj, ok := value.(map[string]any)
	if !ok {
		return
	}
	blockType, _ := obj["type"].(string)
	if blockType != "" && blockType != "text" {
		return
	}
	text, _ := obj["text"].(string)
	if strings.HasPrefix(text, "x-anthropic-billing-header:") {
		obj["text"] = "__anthropic_billing_header__"
	}
}

func extractKiroCacheTTL(value any) *time.Duration {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	cc, ok := obj["cache_control"].(map[string]any)
	if !ok || !strings.EqualFold(strings.TrimSpace(kiroCacheAsString(cc["type"])), "ephemeral") {
		return nil
	}
	ttl := kiroCacheDefaultTTL
	if strings.EqualFold(strings.TrimSpace(kiroCacheAsString(cc["ttl"])), "1h") {
		ttl = kiroCacheOneHourTTL
	}
	return &ttl
}

func (p *kiroCacheProfile) cacheableBreakpoints() []kiroResolvedBreakpoint {
	if p == nil {
		return nil
	}
	resolved := make([]kiroResolvedBreakpoint, 0, len(p.breakpoints))
	for _, breakpoint := range p.breakpoints {
		if breakpoint.blockIndex < 0 || breakpoint.blockIndex >= len(p.blocks) {
			continue
		}
		block := p.blocks[breakpoint.blockIndex]
		if block.cumulativeTokens < p.minCacheable {
			continue
		}
		resolved = append(resolved, kiroResolvedBreakpoint{blockIndex: breakpoint.blockIndex, cumulativeTokens: block.cumulativeTokens, ttl: breakpoint.ttl})
	}
	return resolved
}

func (p *kiroCacheProfile) lastCacheableBreakpoint() *kiroResolvedBreakpoint {
	breakpoints := p.cacheableBreakpoints()
	if len(breakpoints) == 0 {
		return nil
	}
	last := breakpoints[len(breakpoints)-1]
	return &last
}

func kiroCacheEffectiveLookbackLimit(total int) int {
	if total <= 0 {
		return kiroCachePrefixLookbackLimit
	}
	if total < kiroCachePrefixLookbackLimit {
		return total
	}
	if total > kiroCachePrefixLookbackMax {
		return kiroCachePrefixLookbackMax
	}
	return total
}

func buildKiroCacheLookupCandidates(profile *kiroCacheProfile) []kiroCacheLookupCandidate {
	if profile == nil {
		return nil
	}
	breakpoints := profile.cacheableBreakpoints()
	if len(breakpoints) == 0 {
		return nil
	}
	limit := kiroCacheEffectiveLookbackLimit(len(breakpoints))
	if len(breakpoints) > limit {
		breakpoints = breakpoints[len(breakpoints)-limit:]
	}
	candidates := make([]kiroCacheLookupCandidate, 0, len(breakpoints))
	for i := len(breakpoints) - 1; i >= 0; i-- {
		breakpoint := breakpoints[i]
		candidate := profile.blocks[breakpoint.blockIndex]
		candidates = append(candidates, kiroCacheLookupCandidate{
			fingerprint:      candidate.prefixFingerprint,
			fingerprintHex:   hex.EncodeToString(candidate.prefixFingerprint[:]),
			cumulativeTokens: min(breakpoint.cumulativeTokens, profile.totalInputTokens),
			ttl:              breakpoint.ttl,
		})
	}
	return candidates
}

func (t *kiroCacheTracker) match(ctx context.Context, stableKey string, cacheKey uint64, profile *kiroCacheProfile, persistence KiroCachePersistenceStore) *kiroCacheLookupCandidate {
	if t == nil || profile == nil || cacheKey == 0 {
		return nil
	}
	candidates := buildKiroCacheLookupCandidates(profile)
	if len(candidates) == 0 {
		return nil
	}
	now := time.Now()
	if matched := t.matchInMemoryLocked(cacheKey, candidates, now); matched != nil {
		return matched
	}
	if persistence == nil || stableKey == "" {
		return nil
	}
	fingerprints := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		fingerprints = append(fingerprints, candidate.fingerprintHex)
	}
	hits, err := persistence.GetKiroCacheFingerprints(ctx, stableKey, fingerprints)
	if err != nil {
		return nil
	}
	for _, candidate := range candidates {
		if !hits[candidate.fingerprintHex] {
			continue
		}
		t.mu.Lock()
		t.pruneLocked(now)
		accountEntries := t.entries[cacheKey]
		if accountEntries == nil {
			accountEntries = make(map[[32]byte]kiroCacheEntry)
			t.entries[cacheKey] = accountEntries
		}
		accountEntries[candidate.fingerprint] = kiroCacheEntry{
			tokens:    candidate.cumulativeTokens,
			ttl:       candidate.ttl,
			expiresAt: now.Add(candidate.ttl),
		}
		t.mu.Unlock()
		matched := candidate
		return &matched
	}
	return nil
}

func (t *kiroCacheTracker) matchInMemoryLocked(cacheKey uint64, candidates []kiroCacheLookupCandidate, now time.Time) *kiroCacheLookupCandidate {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	accountEntries := t.entries[cacheKey]
	if len(accountEntries) == 0 {
		return nil
	}
	for _, candidate := range candidates {
		entry, ok := accountEntries[candidate.fingerprint]
		if !ok || !entry.expiresAt.After(now) {
			continue
		}
		entry.expiresAt = now.Add(maxDuration(entry.ttl, candidate.ttl))
		accountEntries[candidate.fingerprint] = entry
		matched := candidate
		return &matched
	}
	return nil
}

func (p *kiroCacheProfile) ttlBreakdown(matchedTokens int) (int, int) {
	lastBreakpoint := p.lastCacheableBreakpoint()
	if lastBreakpoint == nil {
		return 0, 0
	}
	newTokens := max(min(lastBreakpoint.cumulativeTokens, p.totalInputTokens)-matchedTokens, 0)
	if newTokens == 0 {
		return 0, 0
	}
	if lastBreakpoint.ttl >= kiroCacheOneHourTTL {
		return 0, newTokens
	}
	return newTokens, 0
}

func (t *kiroCacheTracker) update(cacheKey uint64, profile *kiroCacheProfile) map[string]time.Duration {
	if t == nil || profile == nil || cacheKey == 0 {
		return nil
	}
	persistEntries := make(map[string]time.Duration)
	for _, breakpoint := range profile.cacheableBreakpoints() {
		block := profile.blocks[breakpoint.blockIndex]
		persistEntries[hex.EncodeToString(block.prefixFingerprint[:])] = breakpoint.ttl
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	accountEntries := t.entries[cacheKey]
	if accountEntries == nil {
		accountEntries = make(map[[32]byte]kiroCacheEntry)
		t.entries[cacheKey] = accountEntries
	}
	for _, breakpoint := range profile.cacheableBreakpoints() {
		block := profile.blocks[breakpoint.blockIndex]
		expiresAt := now.Add(breakpoint.ttl)
		entry, ok := accountEntries[block.prefixFingerprint]
		if ok {
			entry.tokens = max(entry.tokens, block.cumulativeTokens)
			entry.ttl = maxDuration(entry.ttl, breakpoint.ttl)
			if expiresAt.After(entry.expiresAt) {
				entry.expiresAt = expiresAt
			}
			accountEntries[block.prefixFingerprint] = entry
			continue
		}
		accountEntries[block.prefixFingerprint] = kiroCacheEntry{tokens: block.cumulativeTokens, ttl: breakpoint.ttl, expiresAt: expiresAt}
	}
	return persistEntries
}

func (t *kiroCacheTracker) pruneLocked(now time.Time) {
	for cacheKey, accountEntries := range t.entries {
		for fp, entry := range accountEntries {
			if !entry.expiresAt.After(now) {
				delete(accountEntries, fp)
			}
		}
		if len(accountEntries) == 0 {
			delete(t.entries, cacheKey)
		}
	}
}

func kiroCacheCredentialKey(account *Account) uint64 {
	stableKey := strings.TrimSpace(kiroCacheCredentialIdentity(account))
	if stableKey == "" {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(stableKey))
	return h.Sum64()
}

func kiroCacheCredentialIdentity(account *Account) string {
	if account == nil {
		return ""
	}
	parts := make([]string, 0, 8)
	for _, key := range []string{"client_id_hash", "client_id", "refresh_token", "profile_arn", "kiro_api_key", "kiroApiKey", "api_key"} {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			parts = append(parts, key+":"+value)
		}
	}
	if len(parts) == 0 && account.ID > 0 {
		parts = append(parts, "account:"+fmt.Sprint(account.ID))
	}
	return strings.Join(parts, "|")
}

func kiroMinimumCacheableTokens(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "opus"):
		return kiroCacheMinTokensOpus
	case strings.Contains(m, "haiku-3") || strings.Contains(m, "haiku_3"):
		return kiroCacheMinTokensHaiku3
	default:
		return kiroCacheMinTokensDefault
	}
}

func stripKiroCacheControl(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, child := range x {
			if k == "cache_control" {
				continue
			}
			out[k] = stripKiroCacheControl(child)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = stripKiroCacheControl(child)
		}
		return out
	default:
		return v
	}
}

func countKiroInputTokensFromPayload(payload map[string]any) int {
	if payload == nil {
		return 1
	}
	tokens := 0
	for _, block := range normalizeKiroSystemBlocks(payload["system"]) {
		tokens += countKiroSystemBlockTokens(block)
	}
	messages, _ := payload["messages"].([]any)
	if len(messages) > 0 {
		canonical, err := canonicalJSON(messages)
		if err == nil {
			tokens += anthropictokenizer.CountTokens(string(canonical))
		}
		tokens += len(messages) * kiroTokensPerMessage
	}
	if tools, ok := payload["tools"].([]any); ok {
		tokens += len(tools) * kiroTokensPerTool
	}
	return max(tokens, 1)
}

func countKiroSystemBlockTokens(value any) int {
	switch typed := value.(type) {
	case string:
		return anthropictokenizer.CountTokens(typed)
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return anthropictokenizer.CountTokens(text)
		}
		return 0
	default:
		return 0
	}
}

func countKiroMessageContentTokens(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case string:
		return anthropictokenizer.CountTokens(typed)
	case []any:
		total := 0
		for _, item := range typed {
			total += countKiroMessageContentTokens(item)
		}
		return total
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return anthropictokenizer.CountTokens(text)
		}
		if thinking, ok := typed["thinking"].(string); ok {
			return anthropictokenizer.CountTokens(thinking)
		}
		if input, ok := typed["input"]; ok {
			return countKiroSerializedValueTokens(input)
		}
		if content, ok := typed["content"]; ok {
			return countKiroMessageContentTokens(content)
		}
		return 0
	default:
		return 0
	}
}

func countKiroSerializedValueTokens(value any) int {
	canonical, err := canonicalJSON(value)
	if err != nil {
		return 0
	}
	return anthropictokenizer.CountTokens(string(canonical))
}

func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonicalJSON(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonicalJSON(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		_ = buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				_ = buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			_, _ = buf.Write(kb)
			_ = buf.WriteByte(':')
			if err := writeCanonicalJSON(buf, x[k]); err != nil {
				return err
			}
		}
		_ = buf.WriteByte('}')
		return nil
	case []any:
		_ = buf.WriteByte('[')
		for i, child := range x {
			if i > 0 {
				_ = buf.WriteByte(',')
			}
			if err := writeCanonicalJSON(buf, child); err != nil {
				return err
			}
		}
		_ = buf.WriteByte(']')
		return nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, _ = buf.Write(b)
		return nil
	}
}

func kiroCacheAsString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func (u *kiroCacheEmulationUsage) toKiroUsage() *kiropkg.Usage {
	if u == nil {
		return nil
	}
	return &kiropkg.Usage{
		InputTokens:                u.InputTokens,
		CacheReadInputTokens:       u.CacheReadInputTokens,
		CacheCreationInputTokens:   u.CacheCreationInputTokens,
		CacheCreation5mInputTokens: u.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: u.CacheCreation1hInputTokens,
	}
}
