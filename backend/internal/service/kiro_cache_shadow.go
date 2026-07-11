package service

import (
	"context"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	kiroCacheShadowSchemaVersion = "v1"
	kiroCacheProtocolLookback    = 20
)

type KiroCacheShadowUsage struct {
	InputTokens                int `json:"input_tokens"`
	CacheReadInputTokens       int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens   int `json:"cache_creation_input_tokens"`
	CacheCreation5mInputTokens int `json:"cache_creation_5m_input_tokens"`
	CacheCreation1hInputTokens int `json:"cache_creation_1h_input_tokens"`
}

func (u KiroCacheShadowUsage) totalInputTokens() int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

type KiroCacheShadowCandidate struct {
	Usage             KiroCacheShadowUsage `json:"usage"`
	InputSideCost     float64              `json:"input_side_cost"`
	CacheHit          bool                 `json:"cache_hit"`
	MatchedTokens     int                  `json:"matched_tokens"`
	MatchedBlockDepth int                  `json:"matched_block_depth"`
}

type KiroCacheShadowSample struct {
	SchemaVersion       string                   `json:"schema_version"`
	CreatedAt           time.Time                `json:"created_at"`
	RequestID           string                   `json:"request_id"`
	AccountID           int64                    `json:"account_id"`
	GroupID             int64                    `json:"group_id"`
	Model               string                   `json:"model"`
	UAForm              string                   `json:"ua_form"`
	ContextBucket       string                   `json:"context_bucket"`
	BlockCount          int                      `json:"block_count"`
	ExplicitBreakpoints int                      `json:"explicit_breakpoints"`
	CurrentStateWarm    bool                     `json:"current_state_warm"`
	ProtocolStateWarm   bool                     `json:"protocol_state_warm"`
	Actual              KiroCacheShadowCandidate `json:"actual"`
	CurrentRatio09      KiroCacheShadowCandidate `json:"current_ratio_0_9"`
	CurrentRatio1       KiroCacheShadowCandidate `json:"current_ratio_1"`
	ProtocolV2          KiroCacheShadowCandidate `json:"protocol_v2"`
}

// KiroCacheShadowStore is an optional Redis-backed sink for cache-emulation
// shadow samples. Shadow data is observational only and never participates in
// request routing, usage logs, or billing.
type KiroCacheShadowStore interface {
	RecordKiroCacheShadowSample(ctx context.Context, sample *KiroCacheShadowSample) error
}

type kiroCacheShadowEvaluation struct {
	accountID           int64
	groupID             int64
	model               string
	uaForm              string
	cacheKey            uint64
	profile             *kiroCacheProfile
	currentRatio09      KiroCacheShadowCandidate
	currentRatio1       KiroCacheShadowCandidate
	protocolV2          KiroCacheShadowCandidate
	protocolMatch       *kiroProtocolLookupCandidate
	currentStateWarm    bool
	protocolStateWarm   bool
	explicitBreakpoints int
}

type kiroProtocolLookupCandidate struct {
	kiroCacheLookupCandidate
	blockDepth int
}

var (
	globalKiroCacheShadowCurrentTracker  = &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}
	globalKiroCacheShadowProtocolTracker = &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}
)

func kiroCacheShadowEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_KIRO_CACHE_SHADOW_ENABLED"))) {
	case "0", "false", "off", "disabled":
		return false
	default:
		return true
	}
}

func (s *GatewayService) kiroCacheShadowStore() KiroCacheShadowStore {
	if s == nil || s.cache == nil {
		return nil
	}
	store, _ := s.cache.(KiroCacheShadowStore)
	return store
}

func (s *GatewayService) beginKiroCacheShadow(ctx context.Context, account *Account, parsed *ParsedRequest, body []byte, model string) *kiroCacheShadowEvaluation {
	if !kiroCacheShadowEnabled() || s.kiroCacheShadowStore() == nil || account == nil || !account.IsAnthropicOAuthOrSetupToken() || len(body) == 0 {
		return nil
	}
	inputTokens := estimateKiroInputTokens(body)
	profile, ok := buildKiroCacheProfile(body, model, inputTokens)
	if !ok || profile == nil {
		return nil
	}
	cacheKey := uint64(account.ID)
	currentWarm := kiroShadowTrackerHasEntries(globalKiroCacheShadowCurrentTracker, cacheKey)
	protocolWarm := kiroShadowTrackerHasEntries(globalKiroCacheShadowProtocolTracker, cacheKey)

	currentMatch := matchKiroShadowCurrentTracker(globalKiroCacheShadowCurrentTracker, cacheKey, buildKiroCacheLookupCandidates(profile), time.Now())
	currentNatural := buildKiroShadowCurrentUsage(profile, currentMatch, 1)
	currentRatio09 := buildKiroShadowCurrentUsage(profile, currentMatch, 0.9)
	protocolUsage, protocolMatch, explicitCount := buildKiroShadowProtocolUsage(globalKiroCacheShadowProtocolTracker, cacheKey, profile)

	groupID := int64(0)
	if parsed != nil {
		groupID = derefGroupID(parsed.GroupID)
	}
	return &kiroCacheShadowEvaluation{
		accountID:           account.ID,
		groupID:             groupID,
		model:               strings.TrimSpace(model),
		uaForm:              string(ClassifyUAForm(ClaudeCodeUserAgent(ctx))),
		cacheKey:            cacheKey,
		profile:             profile,
		currentRatio09:      shadowCandidate(currentRatio09, currentMatch, -1),
		currentRatio1:       shadowCandidate(currentNatural, currentMatch, -1),
		protocolV2:          shadowCandidate(protocolUsage, protocolMatchCandidate(protocolMatch), protocolMatchDepth(protocolMatch)),
		protocolMatch:       protocolMatch,
		currentStateWarm:    currentWarm,
		protocolStateWarm:   protocolWarm,
		explicitBreakpoints: explicitCount,
	}
}

func (s *GatewayService) finishKiroCacheShadow(eval *kiroCacheShadowEvaluation, requestID string, actual ClaudeUsage) {
	if eval == nil || eval.profile == nil {
		return
	}
	actualUsage := KiroCacheShadowUsage{
		InputTokens:                actual.InputTokens,
		CacheReadInputTokens:       actual.CacheReadInputTokens,
		CacheCreationInputTokens:   actual.CacheCreationInputTokens,
		CacheCreation5mInputTokens: actual.CacheCreation5mTokens,
		CacheCreation1hInputTokens: actual.CacheCreation1hTokens,
	}
	budget := actualUsage.totalInputTokens()
	if budget <= 0 {
		return
	}

	eval.currentRatio09.Usage = normalizeKiroShadowUsage(eval.currentRatio09.Usage, budget)
	eval.currentRatio1.Usage = normalizeKiroShadowUsage(eval.currentRatio1.Usage, budget)
	eval.protocolV2.Usage = normalizeKiroShadowUsage(eval.protocolV2.Usage, budget)
	actualCandidate := KiroCacheShadowCandidate{Usage: actualUsage, CacheHit: actual.CacheReadInputTokens > 0}
	actualCandidate.InputSideCost = s.kiroCacheShadowInputCost(eval.model, actualUsage)
	eval.currentRatio09.InputSideCost = s.kiroCacheShadowInputCost(eval.model, eval.currentRatio09.Usage)
	eval.currentRatio1.InputSideCost = s.kiroCacheShadowInputCost(eval.model, eval.currentRatio1.Usage)
	eval.protocolV2.InputSideCost = s.kiroCacheShadowInputCost(eval.model, eval.protocolV2.Usage)

	// Only successful requests reach finish. Commit after the real response so
	// failed or incomplete upstream attempts cannot warm the shadow cache.
	globalKiroCacheShadowCurrentTracker.update(eval.cacheKey, eval.profile)
	refreshKiroShadowProtocolMatch(globalKiroCacheShadowProtocolTracker, eval.cacheKey, eval.protocolMatch, time.Now())
	updateKiroShadowProtocolTracker(globalKiroCacheShadowProtocolTracker, eval.cacheKey, eval.profile)

	sample := &KiroCacheShadowSample{
		SchemaVersion:       kiroCacheShadowSchemaVersion,
		CreatedAt:           time.Now(),
		RequestID:           strings.TrimSpace(requestID),
		AccountID:           eval.accountID,
		GroupID:             eval.groupID,
		Model:               eval.model,
		UAForm:              eval.uaForm,
		ContextBucket:       kiroCacheShadowContextBucket(budget),
		BlockCount:          len(eval.profile.blocks),
		ExplicitBreakpoints: eval.explicitBreakpoints,
		CurrentStateWarm:    eval.currentStateWarm,
		ProtocolStateWarm:   eval.protocolStateWarm,
		Actual:              actualCandidate,
		CurrentRatio09:      eval.currentRatio09,
		CurrentRatio1:       eval.currentRatio1,
		ProtocolV2:          eval.protocolV2,
	}
	store := s.kiroCacheShadowStore()
	if store == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := store.RecordKiroCacheShadowSample(ctx, sample); err != nil {
			logger.L().Warn("kiro.cache_shadow_record_failed", zap.Error(err))
		}
	}()
}

func buildKiroShadowCurrentUsage(profile *kiroCacheProfile, match *kiroCacheLookupCandidate, ratio float64) KiroCacheShadowUsage {
	if profile == nil {
		return KiroCacheShadowUsage{}
	}
	readTokens := 0
	if match != nil {
		readTokens = min(match.cumulativeTokens, profile.totalInputTokens)
	}
	lastBreakpoint := profile.lastCacheableBreakpoint()
	if lastBreakpoint == nil {
		return KiroCacheShadowUsage{InputTokens: profile.totalInputTokens}
	}
	creationTokens := max(min(lastBreakpoint.cumulativeTokens, profile.totalInputTokens)-readTokens, 0)
	creation5m, creation1h := profile.ttlBreakdown(readTokens)
	readTokens = scaleKiroCacheTokens(readTokens, ratio)
	creationTokens = scaleKiroCacheTokens(creationTokens, ratio)
	creation5m = scaleKiroCacheTokens(creation5m, ratio)
	creation1h = scaleKiroCacheTokens(creation1h, ratio)
	return KiroCacheShadowUsage{
		InputTokens:                max(profile.totalInputTokens-readTokens-creationTokens, 0),
		CacheReadInputTokens:       readTokens,
		CacheCreationInputTokens:   creationTokens,
		CacheCreation5mInputTokens: creation5m,
		CacheCreation1hInputTokens: creation1h,
	}
}

func matchKiroShadowCurrentTracker(tracker *kiroCacheTracker, cacheKey uint64, candidates []kiroCacheLookupCandidate, now time.Time) *kiroCacheLookupCandidate {
	if tracker == nil || cacheKey == 0 || len(candidates) == 0 {
		return nil
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.pruneLocked(now)
	entries := tracker.entries[cacheKey]
	for _, candidate := range candidates {
		entry, ok := entries[candidate.fingerprint]
		if !ok || !entry.expiresAt.After(now) {
			continue
		}
		matched := candidate
		return &matched
	}
	return nil
}

func buildKiroShadowProtocolUsage(tracker *kiroCacheTracker, cacheKey uint64, profile *kiroCacheProfile) (KiroCacheShadowUsage, *kiroProtocolLookupCandidate, int) {
	if profile == nil {
		return KiroCacheShadowUsage{}, nil, 0
	}
	breakpoints := profile.explicitCacheableBreakpoints()
	if len(breakpoints) == 0 {
		return KiroCacheShadowUsage{InputTokens: profile.totalInputTokens}, nil, 0
	}
	candidates := buildKiroShadowProtocolLookupCandidates(profile, breakpoints)
	match := matchKiroShadowProtocolTracker(tracker, cacheKey, candidates, time.Now())
	readTokens := 0
	if match != nil {
		readTokens = min(match.cumulativeTokens, profile.totalInputTokens)
	}
	creation5m, creation1h := kiroShadowProtocolCreationBreakdown(profile, breakpoints, readTokens)
	creationTokens := creation5m + creation1h
	usage := KiroCacheShadowUsage{
		InputTokens:                max(profile.totalInputTokens-readTokens-creationTokens, 0),
		CacheReadInputTokens:       readTokens,
		CacheCreationInputTokens:   creationTokens,
		CacheCreation5mInputTokens: creation5m,
		CacheCreation1hInputTokens: creation1h,
	}
	return usage, match, len(breakpoints)
}

func (p *kiroCacheProfile) explicitCacheableBreakpoints() []kiroResolvedBreakpoint {
	if p == nil {
		return nil
	}
	out := make([]kiroResolvedBreakpoint, 0, len(p.breakpoints))
	for _, breakpoint := range p.breakpoints {
		if !breakpoint.explicit || breakpoint.blockIndex < 0 || breakpoint.blockIndex >= len(p.blocks) {
			continue
		}
		block := p.blocks[breakpoint.blockIndex]
		if block.cumulativeTokens < p.minCacheable {
			continue
		}
		out = append(out, kiroResolvedBreakpoint{blockIndex: breakpoint.blockIndex, cumulativeTokens: block.cumulativeTokens, ttl: breakpoint.ttl})
	}
	return out
}

func buildKiroShadowProtocolLookupCandidates(profile *kiroCacheProfile, breakpoints []kiroResolvedBreakpoint) []kiroProtocolLookupCandidate {
	if profile == nil || len(breakpoints) == 0 {
		return nil
	}
	byFingerprint := make(map[[32]byte]kiroProtocolLookupCandidate)
	for i := len(breakpoints) - 1; i >= 0; i-- {
		breakpoint := breakpoints[i]
		start := max(breakpoint.blockIndex-kiroCacheProtocolLookback, 0)
		for blockIndex := breakpoint.blockIndex; blockIndex >= start; blockIndex-- {
			block := profile.blocks[blockIndex]
			candidate := kiroProtocolLookupCandidate{
				kiroCacheLookupCandidate: kiroCacheLookupCandidate{
					fingerprint:      block.prefixFingerprint,
					cumulativeTokens: min(block.cumulativeTokens, profile.totalInputTokens),
					ttl:              breakpoint.ttl,
				},
				blockDepth: breakpoint.blockIndex - blockIndex,
			}
			if existing, ok := byFingerprint[block.prefixFingerprint]; !ok || candidate.cumulativeTokens > existing.cumulativeTokens || candidate.blockDepth < existing.blockDepth {
				byFingerprint[block.prefixFingerprint] = candidate
			}
		}
	}
	out := make([]kiroProtocolLookupCandidate, 0, len(byFingerprint))
	for _, candidate := range byFingerprint {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].cumulativeTokens == out[j].cumulativeTokens {
			return out[i].blockDepth < out[j].blockDepth
		}
		return out[i].cumulativeTokens > out[j].cumulativeTokens
	})
	return out
}

func matchKiroShadowProtocolTracker(tracker *kiroCacheTracker, cacheKey uint64, candidates []kiroProtocolLookupCandidate, now time.Time) *kiroProtocolLookupCandidate {
	if tracker == nil || cacheKey == 0 || len(candidates) == 0 {
		return nil
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.pruneLocked(now)
	entries := tracker.entries[cacheKey]
	for _, candidate := range candidates {
		entry, ok := entries[candidate.fingerprint]
		if !ok || !entry.expiresAt.After(now) {
			continue
		}
		matched := candidate
		return &matched
	}
	return nil
}

func refreshKiroShadowProtocolMatch(tracker *kiroCacheTracker, cacheKey uint64, match *kiroProtocolLookupCandidate, now time.Time) {
	if tracker == nil || cacheKey == 0 || match == nil {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.pruneLocked(now)
	entries := tracker.entries[cacheKey]
	entry, ok := entries[match.fingerprint]
	if !ok || !entry.expiresAt.After(now) {
		return
	}
	entry.expiresAt = now.Add(entry.ttl)
	entries[match.fingerprint] = entry
}

func updateKiroShadowProtocolTracker(tracker *kiroCacheTracker, cacheKey uint64, profile *kiroCacheProfile) {
	if tracker == nil || cacheKey == 0 || profile == nil {
		return
	}
	breakpoints := profile.explicitCacheableBreakpoints()
	if len(breakpoints) == 0 {
		return
	}
	now := time.Now()
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.pruneLocked(now)
	entries := tracker.entries[cacheKey]
	if entries == nil {
		entries = make(map[[32]byte]kiroCacheEntry)
		tracker.entries[cacheKey] = entries
	}
	for _, breakpoint := range breakpoints {
		block := profile.blocks[breakpoint.blockIndex]
		entries[block.prefixFingerprint] = kiroCacheEntry{
			tokens:    block.cumulativeTokens,
			ttl:       breakpoint.ttl,
			expiresAt: now.Add(breakpoint.ttl),
		}
	}
}

func kiroShadowProtocolCreationBreakdown(profile *kiroCacheProfile, breakpoints []kiroResolvedBreakpoint, readTokens int) (int, int) {
	if profile == nil || len(breakpoints) == 0 {
		return 0, 0
	}
	cursor := min(max(readTokens, 0), profile.totalInputTokens)
	creation5m := 0
	creation1h := 0
	for _, breakpoint := range breakpoints {
		end := min(breakpoint.cumulativeTokens, profile.totalInputTokens)
		if end <= cursor {
			continue
		}
		segment := end - cursor
		if breakpoint.ttl >= kiroCacheOneHourTTL {
			creation1h += segment
		} else {
			creation5m += segment
		}
		cursor = end
	}
	return creation5m, creation1h
}

func normalizeKiroShadowUsage(usage KiroCacheShadowUsage, budget int) KiroCacheShadowUsage {
	if budget <= 0 {
		return usage
	}
	total := usage.totalInputTokens()
	if total <= 0 {
		usage.InputTokens = budget
		return usage
	}
	if total < budget {
		usage.InputTokens += budget - total
		return usage
	}
	if total == budget {
		return usage
	}
	scale := func(value int) int {
		return int(math.Round(float64(value) * float64(budget) / float64(total)))
	}
	usage.CacheReadInputTokens = scale(usage.CacheReadInputTokens)
	usage.CacheCreationInputTokens = scale(usage.CacheCreationInputTokens)
	usage.CacheCreation5mInputTokens = scale(usage.CacheCreation5mInputTokens)
	usage.CacheCreation1hInputTokens = scale(usage.CacheCreation1hInputTokens)
	if usage.CacheReadInputTokens+usage.CacheCreationInputTokens > budget {
		usage.CacheCreationInputTokens = max(budget-usage.CacheReadInputTokens, 0)
	}
	usage.InputTokens = max(budget-usage.CacheReadInputTokens-usage.CacheCreationInputTokens, 0)
	breakdown := usage.CacheCreation5mInputTokens + usage.CacheCreation1hInputTokens
	if breakdown > 0 && breakdown != usage.CacheCreationInputTokens {
		usage.CacheCreation5mInputTokens = int(math.Round(float64(usage.CacheCreation5mInputTokens) * float64(usage.CacheCreationInputTokens) / float64(breakdown)))
		usage.CacheCreation1hInputTokens = max(usage.CacheCreationInputTokens-usage.CacheCreation5mInputTokens, 0)
	}
	return usage
}

func shadowCandidate(usage KiroCacheShadowUsage, match *kiroCacheLookupCandidate, depth int) KiroCacheShadowCandidate {
	candidate := KiroCacheShadowCandidate{Usage: usage, MatchedBlockDepth: depth}
	if match != nil {
		candidate.CacheHit = true
		candidate.MatchedTokens = match.cumulativeTokens
	}
	return candidate
}

func protocolMatchCandidate(match *kiroProtocolLookupCandidate) *kiroCacheLookupCandidate {
	if match == nil {
		return nil
	}
	return &match.kiroCacheLookupCandidate
}

func protocolMatchDepth(match *kiroProtocolLookupCandidate) int {
	if match == nil {
		return -1
	}
	return match.blockDepth
}

func kiroShadowTrackerHasEntries(tracker *kiroCacheTracker, cacheKey uint64) bool {
	if tracker == nil || cacheKey == 0 {
		return false
	}
	now := time.Now()
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.pruneLocked(now)
	return len(tracker.entries[cacheKey]) > 0
}

func (s *GatewayService) kiroCacheShadowInputCost(model string, usage KiroCacheShadowUsage) float64 {
	if s == nil || s.billingService == nil || strings.TrimSpace(model) == "" {
		return 0
	}
	cost, err := s.billingService.CalculateCost(model, UsageTokens{
		InputTokens:           usage.InputTokens,
		CacheReadTokens:       usage.CacheReadInputTokens,
		CacheCreationTokens:   usage.CacheCreationInputTokens,
		CacheCreation5mTokens: usage.CacheCreation5mInputTokens,
		CacheCreation1hTokens: usage.CacheCreation1hInputTokens,
	}, 1)
	if err != nil || cost == nil {
		return 0
	}
	return cost.InputCost + cost.CacheReadCost + cost.CacheCreationCost
}

func kiroCacheShadowContextBucket(tokens int) string {
	switch {
	case tokens < 50_000:
		return "lt_50k"
	case tokens < 100_000:
		return "50k_100k"
	case tokens < 180_000:
		return "100k_180k"
	case tokens <= 220_000:
		return "180k_220k"
	default:
		return "gt_220k"
	}
}
