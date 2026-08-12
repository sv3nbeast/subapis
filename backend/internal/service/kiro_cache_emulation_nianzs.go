// Source-faithful, namespaced integration of kiro_cache_emulation.go from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.
// Only package identifiers and the kiro package import are rewritten so the
// legacy engine remains available for an immediate rollback.

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/anthropictokenizer"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
)

const (
	nianzsKiroCacheDefaultTTL       = 5 * time.Minute
	nianzsKiroCacheOneHourTTL       = time.Hour
	nianzsKiroCacheMaxSupportedTTL  = time.Hour
	nianzsKiroTokensPerTool         = 150
	nianzsKiroTokensPerMessage      = 4
	nianzsKiroCacheMinTokensDefault = 1024
	nianzsKiroCacheMinTokensOpus    = 4096
	// nianzsKiroCacheMinTokensGPT 与 default 同值但语义独立：1024 对齐 OpenAI 官方的最小
	// 缓存粒度，不应随 default 一起调整。见 nianzsKiroMinimumCacheableTokens。
	nianzsKiroCacheMinTokensGPT        = 1024
	nianzsKiroCachePrefixLookbackLimit = 10

	// Claude 4.7+ changed its input accounting substantially, especially for
	// tool schemas. These constants are calibrated against provider-native
	// Messages usage: system text uses the modern text vocabulary, while every
	// tool also carries a sizeable provider envelope that is absent from the
	// client JSON. Keep this model-gated so the pinned 4.6-and-earlier cache
	// accounting remains unchanged.
	nianzsModernClaudeSystemScaleNumerator   = 1258
	nianzsModernClaudeSystemScaleDenominator = 1000
	nianzsModernClaudeSystemBlockOverhead    = 3
	nianzsModernClaudeToolScaleNumerator     = 930
	nianzsModernClaudeToolScaleDenominator   = 1000
	nianzsModernClaudeToolEnvelopeTokens     = 337
	nianzsModernClaudeCachedToolBoundaryNum  = 5
	nianzsModernClaudeCachedToolBoundaryDen  = 2
)

type nianzsKiroCacheEmulationUsage struct {
	InputTokens                int
	CacheReadInputTokens       int
	CacheCreationInputTokens   int
	CacheCreation5mInputTokens int
	CacheCreation1hInputTokens int
}

// nianzsKiroCacheEmulationPolicy is the compatibility boundary between the
// source-faithful nianzs cache tracker and Sub2API's existing account-level
// Kiro cache settings. Historically, account settings took precedence and a
// Kiro group supplied the fallback. Keep that contract so a Kiro account does
// not lose cache accounting merely because it is scheduled from a mixed
// Anthropic group.
type nianzsKiroCacheEmulationPolicy struct {
	enabled       bool
	mode          string
	creationRatio float64
	readRatio     float64
}

func resolveNianzsKiroCacheEmulationPolicy(account *Account, group *Group) nianzsKiroCacheEmulationPolicy {
	if account == nil || account.ID <= 0 || !account.IsKiro() {
		return nianzsKiroCacheEmulationPolicy{}
	}
	if account.EffectiveKiroCacheEmulationEnabled() {
		ratio := account.GetKiroCacheEmulationRatio()
		return nianzsKiroCacheEmulationPolicy{
			enabled:       ratio > 0,
			mode:          KiroCacheEmulationModeUniform,
			creationRatio: ratio,
			readRatio:     ratio,
		}
	}

	NormalizeGroupRuntimeFields(group)
	if group == nil || !group.EffectiveKiroCacheEmulationEnabled() {
		return nianzsKiroCacheEmulationPolicy{}
	}
	creationRatio, readRatio := group.EffectiveKiroCacheEmulationRatios()
	return nianzsKiroCacheEmulationPolicy{
		enabled:       creationRatio > 0 || readRatio > 0,
		mode:          group.EffectiveKiroCacheEmulationMode(),
		creationRatio: creationRatio,
		readRatio:     readRatio,
	}
}

type nianzsKiroCacheEntry struct {
	tokens    int
	ttl       time.Duration
	expiresAt time.Time
}

type nianzsKiroCacheTracker struct {
	mu      sync.Mutex
	entries map[uint64]map[[32]byte]nianzsKiroCacheEntry
}

var nianzsGlobalKiroCacheTracker = &nianzsKiroCacheTracker{entries: make(map[uint64]map[[32]byte]nianzsKiroCacheEntry)}

// nianzsKiroCacheEmulationPlan 把缓存估算拆成"计算"与"落盘"两步：prepare 阶段只读 tracker
// 得到估算结果，commit() 才会把本次前缀写入 tracker。调用方应在确认上游请求成功后
// 再 commit()，避免请求失败/未发出时就把内容错误标记为已缓存，污染下一次请求的估算。
type nianzsKiroCacheEmulationPlan struct {
	usage    *nianzsKiroCacheEmulationUsage
	cacheKey uint64
	profile  *nianzsKiroCacheProfile
}

func (p *nianzsKiroCacheEmulationPlan) result() *nianzsKiroCacheEmulationUsage {
	if p == nil {
		return nil
	}
	return p.usage
}

func (p *nianzsKiroCacheEmulationPlan) commit() {
	if p == nil || p.profile == nil || p.cacheKey == 0 {
		return
	}
	nianzsGlobalKiroCacheTracker.update(p.cacheKey, p.profile)
}

func (s *GatewayService) buildKiroCacheEmulationUsageNianzs(ctx context.Context, account *Account, group *Group, body []byte, model string, inputTokens int) *nianzsKiroCacheEmulationUsage {
	plan := s.prepareKiroCacheEmulationUsageNianzs(ctx, account, group, body, model, inputTokens)
	plan.commit()
	return plan.result()
}

func (s *GatewayService) prepareKiroCacheEmulationUsageNianzs(ctx context.Context, account *Account, group *Group, body []byte, model string, inputTokens int) *nianzsKiroCacheEmulationPlan {
	policy := resolveNianzsKiroCacheEmulationPolicy(account, group)
	if !policy.enabled || len(body) == 0 {
		return nil
	}
	profile, ok := nianzsBuildKiroCacheProfile(ctx, body, model, inputTokens)
	if !ok {
		return nil
	}
	return s.prepareKiroCacheEmulationPlanFromProfileNianzs(account, policy, profile, inputTokens)
}

func (s *GatewayService) buildKiroResponsesCacheEmulationUsageNianzs(ctx context.Context, account *Account, group *Group, body []byte, model string, inputTokens int) *nianzsKiroCacheEmulationUsage {
	plan := s.prepareKiroResponsesCacheEmulationUsageNianzs(ctx, account, group, body, model, inputTokens)
	plan.commit()
	return plan.result()
}

func (s *GatewayService) prepareKiroResponsesCacheEmulationUsageNianzs(ctx context.Context, account *Account, group *Group, body []byte, model string, inputTokens int) *nianzsKiroCacheEmulationPlan {
	policy := resolveNianzsKiroCacheEmulationPolicy(account, group)
	if !policy.enabled || len(body) == 0 {
		return nil
	}
	profile, ok := nianzsBuildKiroResponsesCacheProfile(ctx, body, model, inputTokens)
	if !ok {
		return nil
	}
	return s.prepareKiroCacheEmulationPlanFromProfileNianzs(account, policy, profile, inputTokens)
}

func (s *GatewayService) buildKiroChatCompletionsCacheEmulationUsageNianzs(ctx context.Context, account *Account, group *Group, body []byte, model string, inputTokens int) *nianzsKiroCacheEmulationUsage {
	plan := s.prepareKiroChatCompletionsCacheEmulationUsageNianzs(ctx, account, group, body, model, inputTokens)
	plan.commit()
	return plan.result()
}

func (s *GatewayService) prepareKiroChatCompletionsCacheEmulationUsageNianzs(ctx context.Context, account *Account, group *Group, body []byte, model string, inputTokens int) *nianzsKiroCacheEmulationPlan {
	policy := resolveNianzsKiroCacheEmulationPolicy(account, group)
	if !policy.enabled || len(body) == 0 {
		return nil
	}
	profile, ok := nianzsBuildKiroChatCompletionsCacheProfile(ctx, body, model, inputTokens)
	if !ok {
		return nil
	}
	effectiveInputTokens := inputTokens
	if effectiveInputTokens <= 0 {
		effectiveInputTokens = profile.totalInputTokens
	}
	return s.prepareKiroCacheEmulationPlanFromProfileNianzs(account, policy, profile, effectiveInputTokens)
}

func (s *GatewayService) prepareKiroCacheEmulationPlanFromProfileNianzs(account *Account, policy nianzsKiroCacheEmulationPolicy, profile *nianzsKiroCacheProfile, inputTokens int) *nianzsKiroCacheEmulationPlan {
	if account == nil || account.ID <= 0 || !policy.enabled || profile == nil {
		return nil
	}
	cacheKey := nianzsKiroCacheCredentialKey(account)
	if cacheKey == 0 {
		return nil
	}
	result := nianzsGlobalKiroCacheTracker.compute(cacheKey, profile)
	if policy.mode == KiroCacheEmulationModeUniform {
		ratio := policy.readRatio
		result.CacheReadInputTokens = nianzsScaleKiroCacheTokens(result.CacheReadInputTokens, ratio)
		result.CacheCreationInputTokens = nianzsScaleKiroCacheTokens(result.CacheCreationInputTokens, policy.creationRatio)
		result.CacheCreation5mInputTokens = nianzsScaleKiroCacheTokens(result.CacheCreation5mInputTokens, policy.creationRatio)
		result.CacheCreation1hInputTokens = nianzsScaleKiroCacheTokens(result.CacheCreation1hInputTokens, policy.creationRatio)
	} else {
		creationRatio, readRatio := policy.creationRatio, policy.readRatio
		result.CacheReadInputTokens = nianzsScaleKiroCacheTokens(result.CacheReadInputTokens, readRatio)
		result.CacheCreationInputTokens = nianzsScaleKiroCacheTokens(result.CacheCreationInputTokens, creationRatio)
		result.CacheCreation5mInputTokens, result.CacheCreation1hInputTokens = nianzsScaleKiroCacheCreationTTLTokens(
			result.CacheCreation5mInputTokens,
			result.CacheCreation1hInputTokens,
			result.CacheCreationInputTokens,
			creationRatio,
		)
	}
	result.InputTokens = inputTokens - result.CacheReadInputTokens - result.CacheCreationInputTokens
	if result.InputTokens < 0 {
		result.InputTokens = 0
	}
	if result.CacheReadInputTokens == 0 && result.CacheCreationInputTokens == 0 {
		result = nil
	}
	return &nianzsKiroCacheEmulationPlan{usage: result, cacheKey: cacheKey, profile: profile}
}

func nianzsScaleKiroCacheCreationTTLTokens(tokens5m, tokens1h, scaledTotal int, ratio float64) (int, int) {
	if scaledTotal <= 0 || ratio <= 0 {
		return 0, 0
	}
	if tokens5m <= 0 && tokens1h <= 0 {
		return 0, 0
	}
	if tokens1h <= 0 {
		return scaledTotal, 0
	}
	if tokens5m <= 0 {
		return 0, scaledTotal
	}
	scaled5m := nianzsScaleKiroCacheTokens(tokens5m, ratio)
	if scaled5m > scaledTotal {
		scaled5m = scaledTotal
	}
	scaled1h := scaledTotal - scaled5m
	return scaled5m, scaled1h
}

func nianzsScaleKiroCacheTokens(tokens int, ratio float64) int {
	if tokens <= 0 || ratio <= 0 {
		return 0
	}
	if ratio >= 1 {
		return tokens
	}
	return int(math.Round(float64(tokens) * ratio))
}

type nianzsKiroCacheProfile struct {
	totalInputTokens              int
	minCacheable                  int
	scaleBreakpointsToInputTokens bool
	blocks                        []nianzsKiroCacheBlock
	breakpoints                   []nianzsKiroCacheBreakpoint
}

type nianzsKiroCacheBlock struct {
	prefixFingerprint [32]byte
	cumulativeTokens  int
}

type nianzsKiroCacheBreakpoint struct {
	blockIndex int
	ttl        time.Duration
}

type nianzsKiroResolvedBreakpoint struct {
	blockIndex       int
	cumulativeTokens int
	ttl              time.Duration
}

type nianzsKiroPendingBlock struct {
	value         any
	tokens        int
	breakpointTTL *time.Duration
	messageIndex  *int
	isMessageEnd  bool
}

func nianzsBuildKiroCacheProfile(ctx context.Context, body []byte, model string, inputTokens int) (*nianzsKiroCacheProfile, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	blocks := nianzsFlattenKiroCacheBlocks(ctx, payload)
	if len(blocks) == 0 {
		return nil, false
	}
	if nianzsUsesModernClaudeInputAccounting(model) {
		nianzsApplyModernClaudeCacheBlockTokens(ctx, blocks)
	}
	totalTokens := inputTokens
	if totalTokens <= 0 {
		totalTokens = nianzsCountKiroInputTokensFromPayload(ctx, payload)
	}
	prelude := map[string]any{
		"model":         payload["model"],
		"tool_choice":   payload["tool_choice"],
		"thinking":      payload["thinking"],
		"output_config": payload["output_config"],
	}
	return nianzsBuildKiroCacheProfileFromBlocks(model, totalTokens, prelude, blocks)
}

func nianzsBuildKiroResponsesCacheProfile(ctx context.Context, body []byte, model string, inputTokens int) (*nianzsKiroCacheProfile, bool) {
	var req apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	blocks := nianzsFlattenKiroResponsesCacheBlocks(ctx, payload)
	if len(blocks) == 0 {
		return nil, false
	}
	ttl := nianzsKiroCacheDefaultTTL
	nianzsApplyKiroResponsesDefaultBreakpoints(blocks, ttl)

	effectiveTools, err := apicompat.EffectiveResponsesTools(&req)
	if err != nil {
		return nil, false
	}
	effectiveModel := strings.TrimSpace(model)
	if effectiveModel == "" {
		effectiveModel, _ = payload["model"].(string)
	}
	totalTokens := inputTokens
	if totalTokens <= 0 {
		totalTokens = nianzsCountKiroResponsesInputTokens(payload, blocks, len(effectiveTools))
	}
	prelude := map[string]any{
		"protocol":             "responses",
		"model":                effectiveModel,
		"tool_choice":          payload["tool_choice"],
		"tools":                nianzsKiroJSONCompatibleValue(effectiveTools),
		"prompt_cache_key":     payload["prompt_cache_key"],
		"previous_response_id": payload["previous_response_id"],
		"reasoning_effort":     nianzsKiroNestedValue(payload, "reasoning", "effort"),
		"text_format":          nianzsKiroNestedValue(payload, "text", "format"),
	}
	profile, ok := nianzsBuildKiroCacheProfileFromBlocks(effectiveModel, totalTokens, prelude, blocks)
	if ok {
		profile.scaleBreakpointsToInputTokens = true
	}
	return profile, ok
}

func nianzsBuildKiroChatCompletionsCacheProfile(ctx context.Context, body []byte, model string, inputTokens int) (*nianzsKiroCacheProfile, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	blocks := nianzsFlattenKiroChatCompletionsCacheBlocks(ctx, payload)
	if len(blocks) == 0 {
		return nil, false
	}
	nianzsApplyKiroResponsesDefaultBreakpoints(blocks, nianzsKiroCacheDefaultTTL)

	effectiveModel := strings.TrimSpace(model)
	if effectiveModel == "" {
		effectiveModel, _ = payload["model"].(string)
	}
	tools, _ := payload["tools"].([]any)
	functions, _ := payload["functions"].([]any)
	totalTokens := inputTokens
	if totalTokens <= 0 {
		totalTokens = nianzsCountKiroChatCompletionsInputTokens(payload, blocks, len(tools)+len(functions))
	}
	prelude := map[string]any{
		"protocol":            "chat_completions",
		"model":               effectiveModel,
		"instructions":        payload["instructions"],
		"tool_choice":         payload["tool_choice"],
		"function_call":       payload["function_call"],
		"tools":               nianzsKiroJSONCompatibleValue(payload["tools"]),
		"functions":           nianzsKiroJSONCompatibleValue(payload["functions"]),
		"parallel_tool_calls": payload["parallel_tool_calls"],
		"reasoning_effort":    payload["reasoning_effort"],
		"response_format":     nianzsKiroJSONCompatibleValue(payload["response_format"]),
	}
	profile, ok := nianzsBuildKiroCacheProfileFromBlocks(effectiveModel, totalTokens, prelude, blocks)
	if ok {
		profile.scaleBreakpointsToInputTokens = true
	}
	return profile, ok
}

func nianzsBuildKiroCacheProfileFromBlocks(model string, totalTokens int, preludeValue any, blocks []nianzsKiroPendingBlock) (*nianzsKiroCacheProfile, bool) {
	prelude, err := nianzsCanonicalJSON(preludeValue)
	if err != nil {
		return nil, false
	}
	prefixState := make([]byte, 8+len(prelude))
	binary.BigEndian.PutUint64(prefixState[:8], uint64(len(prelude)))
	copy(prefixState[8:], prelude)

	profile := &nianzsKiroCacheProfile{totalInputTokens: max(totalTokens, 0), minCacheable: nianzsKiroMinimumCacheableTokens(model)}
	cumulativeTokens := 0
	var activeTTL *time.Duration
	seenBreakpoints := make(map[int]struct{})
	for index, block := range blocks {
		cumulativeTokens += max(block.tokens, 0)
		blockJSON, err := nianzsCanonicalJSON(block.value)
		if err != nil {
			return nil, false
		}
		blockHash := sha256.Sum256(blockJSON)
		h := sha256.New()
		_, _ = h.Write(prefixState)
		_, _ = h.Write(blockHash[:])
		prefixFingerprint := [32]byte(h.Sum(nil))
		prefixState = prefixFingerprint[:]
		profile.blocks = append(profile.blocks, nianzsKiroCacheBlock{prefixFingerprint: prefixFingerprint, cumulativeTokens: cumulativeTokens})

		if block.breakpointTTL != nil {
			ttl := nianzsMinDuration(*block.breakpointTTL, nianzsKiroCacheMaxSupportedTTL)
			activeTTL = &ttl
			if _, ok := seenBreakpoints[index]; !ok {
				profile.breakpoints = append(profile.breakpoints, nianzsKiroCacheBreakpoint{blockIndex: index, ttl: ttl})
				seenBreakpoints[index] = struct{}{}
			}
		}
		if block.isMessageEnd && block.messageIndex != nil && activeTTL != nil {
			if _, ok := seenBreakpoints[index]; !ok {
				profile.breakpoints = append(profile.breakpoints, nianzsKiroCacheBreakpoint{blockIndex: index, ttl: *activeTTL})
				seenBreakpoints[index] = struct{}{}
			}
		}
	}
	if profile.lastCacheableBreakpoint() == nil {
		return nil, false
	}
	return profile, true
}

func nianzsFlattenKiroCacheBlocks(ctx context.Context, payload map[string]any) []nianzsKiroPendingBlock {
	var blocks []nianzsKiroPendingBlock
	if tools, ok := payload["tools"].([]any); ok {
		for toolIndex, tool := range tools {
			value := nianzsStripKiroCacheControl(tool)
			blocks = append(blocks, nianzsKiroPendingBlock{
				value:  map[string]any{"kind": "tool", "tool_index": toolIndex, "tool": value},
				tokens: nianzsCountKiroToolDefinitionTokens(tool), breakpointTTL: nianzsExtractKiroCacheTTL(tool),
			})
		}
	}
	for systemIndex, systemBlock := range nianzsNormalizeKiroSystemBlocks(payload["system"]) {
		value := nianzsStripKiroCacheControl(systemBlock)
		nianzsCanonicalizeKiroSystemBlock(value)
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "system", "system_index": systemIndex, "block": value},
			tokens: nianzsCountKiroSystemBlockTokens(systemBlock), breakpointTTL: nianzsExtractKiroCacheTTL(systemBlock),
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
			blocks = append(blocks, nianzsKiroPendingBlock{
				value:        map[string]any{"kind": "message", "message_index": messageIndex, "role": role, "block_index": 0, "block": block},
				tokens:       nianzsCountKiroMessageContentTokens(ctx, block),
				messageIndex: &mi,
			})
		case []any:
			for blockIndex, rawBlock := range typed {
				mi := messageIndex
				value := nianzsStripKiroCacheControl(rawBlock)
				blocks = append(blocks, nianzsKiroPendingBlock{
					value:         map[string]any{"kind": "message", "message_index": messageIndex, "role": role, "block_index": blockIndex, "block": value},
					tokens:        nianzsCountKiroMessageContentTokens(ctx, rawBlock),
					breakpointTTL: nianzsExtractKiroCacheTTL(rawBlock),
					messageIndex:  &mi,
				})
			}
		}
	}
	return blocks
}

func nianzsFlattenKiroResponsesCacheBlocks(ctx context.Context, payload map[string]any) []nianzsKiroPendingBlock {
	var blocks []nianzsKiroPendingBlock
	if instructions, ok := payload["instructions"].(string); strings.TrimSpace(instructions) != "" && ok {
		block := map[string]any{"type": "input_text", "text": strings.TrimSpace(instructions)}
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:        map[string]any{"kind": "instructions", "block": block},
			tokens:       nianzsCountKiroMessageContentTokens(ctx, block),
			isMessageEnd: true,
		})
	}

	switch input := payload["input"].(type) {
	case string:
		block := map[string]any{"type": "input_text", "text": input}
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:        map[string]any{"kind": "input", "input_index": 0, "role": "user", "block_index": 0, "block": block},
			tokens:       nianzsCountKiroMessageContentTokens(ctx, block),
			isMessageEnd: true,
		})
	case []any:
		cacheableItemCount := nianzsCountKiroResponsesCacheableInputItems(input)
		seenCacheableItems := 0
		for itemIndex, rawItem := range input {
			if !nianzsIsKiroResponsesCacheableInputItem(rawItem) {
				blocks = nianzsAppendKiroResponsesInputItemBlocks(ctx, blocks, itemIndex, rawItem, false)
				continue
			}
			seenCacheableItems++
			isStableHistoryItem := seenCacheableItems < cacheableItemCount
			blocks = nianzsAppendKiroResponsesInputItemBlocks(ctx, blocks, itemIndex, rawItem, isStableHistoryItem)
		}
	}
	return blocks
}

func nianzsFlattenKiroChatCompletionsCacheBlocks(ctx context.Context, payload map[string]any) []nianzsKiroPendingBlock {
	var blocks []nianzsKiroPendingBlock
	if instructions, ok := payload["instructions"].(string); ok && strings.TrimSpace(instructions) != "" {
		block := map[string]any{"type": "input_text", "text": strings.TrimSpace(instructions)}
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "instructions", "block": block},
			tokens: nianzsCountKiroMessageContentTokens(ctx, block),
		})
	}

	messages, _ := payload["messages"].([]any)
	for messageIndex, rawMessage := range messages {
		markMessageEnd := nianzsIsKiroChatCompletionsCacheableMessage(rawMessage)
		blocks = nianzsAppendKiroChatCompletionsMessageBlocks(ctx, blocks, messageIndex, rawMessage, markMessageEnd)
	}
	return blocks
}

func nianzsIsKiroChatCompletionsCacheableMessage(rawMessage any) bool {
	message, ok := rawMessage.(map[string]any)
	if !ok {
		return false
	}
	role, _ := message["role"].(string)
	return strings.TrimSpace(role) != ""
}

func nianzsAppendKiroChatCompletionsMessageBlocks(ctx context.Context, blocks []nianzsKiroPendingBlock, messageIndex int, rawMessage any, markMessageEnd bool) []nianzsKiroPendingBlock {
	start := len(blocks)
	message, ok := rawMessage.(map[string]any)
	if !ok {
		return blocks
	}
	role, _ := message["role"].(string)
	name, _ := message["name"].(string)

	if content, ok := message["content"]; ok {
		blocks = nianzsAppendKiroChatCompletionsContentBlocks(ctx, blocks, messageIndex, role, name, content)
	}
	if reasoning, ok := message["reasoning_content"].(string); ok && reasoning != "" {
		block := map[string]any{"type": "reasoning", "thinking": reasoning}
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "message_reasoning", "message_index": messageIndex, "role": role, "name": name, "block": block},
			tokens: nianzsCountKiroMessageContentTokens(ctx, block),
		})
	}
	if toolCalls, ok := message["tool_calls"]; ok {
		value := nianzsKiroJSONCompatibleValue(toolCalls)
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "message_tool_calls", "message_index": messageIndex, "role": role, "name": name, "block": value},
			tokens: nianzsCountKiroSerializedValueTokens(value),
		})
	}
	if toolCallID, ok := message["tool_call_id"].(string); ok && toolCallID != "" {
		value := map[string]any{"tool_call_id": toolCallID}
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "message_tool_call_id", "message_index": messageIndex, "role": role, "name": name, "block": value},
			tokens: nianzsCountKiroSerializedValueTokens(value),
		})
	}
	if functionCall, ok := message["function_call"]; ok {
		value := nianzsKiroJSONCompatibleValue(functionCall)
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "message_function_call", "message_index": messageIndex, "role": role, "name": name, "block": value},
			tokens: nianzsCountKiroSerializedValueTokens(value),
		})
	}
	return nianzsMarkKiroResponsesInputItemEnd(blocks, start, messageIndex, markMessageEnd)
}

func nianzsAppendKiroChatCompletionsContentBlocks(ctx context.Context, blocks []nianzsKiroPendingBlock, messageIndex int, role string, name string, content any) []nianzsKiroPendingBlock {
	switch typed := content.(type) {
	case string:
		block := map[string]any{"type": "text", "text": typed}
		return append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "message", "message_index": messageIndex, "role": role, "name": name, "block_index": 0, "block": block},
			tokens: nianzsCountKiroMessageContentTokens(ctx, block),
		})
	case []any:
		for blockIndex, rawBlock := range typed {
			block := rawBlock
			if text, ok := rawBlock.(string); ok {
				block = map[string]any{"type": "text", "text": text}
			}
			blocks = append(blocks, nianzsKiroPendingBlock{
				value:  map[string]any{"kind": "message", "message_index": messageIndex, "role": role, "name": name, "block_index": blockIndex, "block": nianzsKiroJSONCompatibleValue(block)},
				tokens: nianzsCountKiroMessageContentTokens(ctx, block),
			})
		}
	case map[string]any:
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "message", "message_index": messageIndex, "role": role, "name": name, "block_index": 0, "block": nianzsKiroJSONCompatibleValue(typed)},
			tokens: nianzsCountKiroMessageContentTokens(ctx, typed),
		})
	}
	return blocks
}

func nianzsApplyKiroResponsesDefaultBreakpoints(blocks []nianzsKiroPendingBlock, ttl time.Duration) {
	if len(blocks) == 0 {
		return
	}
	for i := range blocks {
		if blocks[i].isMessageEnd {
			blocks[i].breakpointTTL = &ttl
		}
	}
	blocks[len(blocks)-1].breakpointTTL = &ttl
}

func nianzsCountKiroResponsesCacheableInputItems(input []any) int {
	count := 0
	for _, rawItem := range input {
		if nianzsIsKiroResponsesCacheableInputItem(rawItem) {
			count++
		}
	}
	return count
}

func nianzsIsKiroResponsesCacheableInputItem(rawItem any) bool {
	if _, ok := rawItem.(string); ok {
		return true
	}
	item, ok := rawItem.(map[string]any)
	if !ok {
		return false
	}
	itemType, _ := item["type"].(string)
	return itemType != "additional_tools"
}

func nianzsAppendKiroResponsesInputItemBlocks(ctx context.Context, blocks []nianzsKiroPendingBlock, itemIndex int, rawItem any, markMessageEnd bool) []nianzsKiroPendingBlock {
	start := len(blocks)
	if text, ok := rawItem.(string); ok {
		block := map[string]any{"type": "input_text", "text": text}
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "input", "input_index": itemIndex, "role": "user", "block_index": 0, "block": block},
			tokens: nianzsCountKiroMessageContentTokens(ctx, block),
		})
		return nianzsMarkKiroResponsesInputItemEnd(blocks, start, itemIndex, markMessageEnd)
	}
	item, ok := rawItem.(map[string]any)
	if !ok {
		return blocks
	}
	itemType, _ := item["type"].(string)
	if itemType == "additional_tools" {
		return blocks
	}
	role, _ := item["role"].(string)
	if role == "" && (itemType == "message" || itemType == "") {
		role = "user"
	}
	if content, ok := item["content"]; ok {
		blocks = nianzsAppendKiroResponsesContentBlocks(ctx, blocks, itemIndex, role, content)
		return nianzsMarkKiroResponsesInputItemEnd(blocks, start, itemIndex, markMessageEnd)
	}
	value := nianzsKiroJSONCompatibleValue(item)
	blocks = append(blocks, nianzsKiroPendingBlock{
		value:  map[string]any{"kind": "input_item", "input_index": itemIndex, "type": itemType, "block": value},
		tokens: nianzsCountKiroSerializedValueTokens(value),
	})
	return nianzsMarkKiroResponsesInputItemEnd(blocks, start, itemIndex, markMessageEnd)
}

func nianzsMarkKiroResponsesInputItemEnd(blocks []nianzsKiroPendingBlock, start, itemIndex int, markMessageEnd bool) []nianzsKiroPendingBlock {
	if !markMessageEnd || len(blocks) <= start {
		return blocks
	}
	last := len(blocks) - 1
	mi := itemIndex
	blocks[last].messageIndex = &mi
	blocks[last].isMessageEnd = true
	return blocks
}

func nianzsAppendKiroResponsesContentBlocks(ctx context.Context, blocks []nianzsKiroPendingBlock, itemIndex int, role string, content any) []nianzsKiroPendingBlock {
	switch typed := content.(type) {
	case string:
		block := map[string]any{"type": "input_text", "text": typed}
		return append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "input", "input_index": itemIndex, "role": role, "block_index": 0, "block": block},
			tokens: nianzsCountKiroMessageContentTokens(ctx, block),
		})
	case []any:
		for blockIndex, rawBlock := range typed {
			block := rawBlock
			if text, ok := rawBlock.(string); ok {
				block = map[string]any{"type": "input_text", "text": text}
			}
			blocks = append(blocks, nianzsKiroPendingBlock{
				value:  map[string]any{"kind": "input", "input_index": itemIndex, "role": role, "block_index": blockIndex, "block": nianzsKiroJSONCompatibleValue(block)},
				tokens: nianzsCountKiroMessageContentTokens(ctx, block),
			})
		}
	case map[string]any:
		blocks = append(blocks, nianzsKiroPendingBlock{
			value:  map[string]any{"kind": "input", "input_index": itemIndex, "role": role, "block_index": 0, "block": nianzsKiroJSONCompatibleValue(typed)},
			tokens: nianzsCountKiroMessageContentTokens(ctx, typed),
		})
	}
	return blocks
}

func nianzsCountKiroResponsesInputTokens(payload map[string]any, blocks []nianzsKiroPendingBlock, toolCount int) int {
	tokens := toolCount * nianzsKiroTokensPerTool
	for _, block := range blocks {
		tokens += max(block.tokens, 0)
	}
	if input, ok := payload["input"].([]any); ok {
		tokens += len(input) * nianzsKiroTokensPerMessage
	}
	return max(tokens, 1)
}

func nianzsCountKiroChatCompletionsInputTokens(payload map[string]any, blocks []nianzsKiroPendingBlock, toolCount int) int {
	tokens := toolCount * nianzsKiroTokensPerTool
	for _, block := range blocks {
		tokens += max(block.tokens, 0)
	}
	if messages, ok := payload["messages"].([]any); ok {
		tokens += len(messages) * nianzsKiroTokensPerMessage
	}
	return max(tokens, 1)
}

func nianzsKiroNestedValue(payload map[string]any, keys ...string) any {
	var current any = payload
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[key]
	}
	return current
}

func nianzsKiroJSONCompatibleValue(value any) any {
	b, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return value
	}
	return out
}

func nianzsNormalizeKiroSystemBlocks(system any) []any {
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

func nianzsCanonicalizeKiroSystemBlock(value any) {
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

func nianzsExtractKiroCacheTTL(value any) *time.Duration {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	cc, ok := obj["cache_control"].(map[string]any)
	if !ok || !strings.EqualFold(strings.TrimSpace(nianzsKiroCacheAsString(cc["type"])), "ephemeral") {
		return nil
	}
	ttl := nianzsKiroCacheDefaultTTL
	if strings.EqualFold(strings.TrimSpace(nianzsKiroCacheAsString(cc["ttl"])), "1h") {
		ttl = nianzsKiroCacheOneHourTTL
	}
	return &ttl
}

func (p *nianzsKiroCacheProfile) cacheableBreakpoints() []nianzsKiroResolvedBreakpoint {
	if p == nil {
		return nil
	}
	resolved := make([]nianzsKiroResolvedBreakpoint, 0, len(p.breakpoints))
	for _, breakpoint := range p.breakpoints {
		if breakpoint.blockIndex < 0 || breakpoint.blockIndex >= len(p.blocks) {
			continue
		}
		block := p.blocks[breakpoint.blockIndex]
		if block.cumulativeTokens < p.minCacheable {
			continue
		}
		resolved = append(resolved, nianzsKiroResolvedBreakpoint{blockIndex: breakpoint.blockIndex, cumulativeTokens: block.cumulativeTokens, ttl: breakpoint.ttl})
	}
	return resolved
}

func (p *nianzsKiroCacheProfile) lastCacheableBreakpoint() *nianzsKiroResolvedBreakpoint {
	breakpoints := p.cacheableBreakpoints()
	if len(breakpoints) == 0 {
		return nil
	}
	last := breakpoints[len(breakpoints)-1]
	return &last
}

func (t *nianzsKiroCacheTracker) compute(cacheKey uint64, profile *nianzsKiroCacheProfile) *nianzsKiroCacheEmulationUsage {
	out := &nianzsKiroCacheEmulationUsage{}
	if t == nil || profile == nil || cacheKey == 0 {
		return out
	}
	lastBreakpoint := profile.lastCacheableBreakpoint()
	if lastBreakpoint == nil {
		return out
	}
	lastBreakpointTokens := profile.cacheTokensForBreakpoint(lastBreakpoint.cumulativeTokens)
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)

	matchedTokens := 0
	if accountEntries := t.entries[cacheKey]; accountEntries != nil {
		breakpoints := profile.cacheableBreakpoints()
		for i, seen := len(breakpoints)-1, 0; i >= 0 && seen < nianzsKiroCachePrefixLookbackLimit; i, seen = i-1, seen+1 {
			breakpoint := breakpoints[i]
			candidate := profile.blocks[breakpoint.blockIndex]
			entry, ok := accountEntries[candidate.prefixFingerprint]
			if !ok || !entry.expiresAt.After(now) {
				continue
			}
			entry.expiresAt = now.Add(entry.ttl)
			accountEntries[candidate.prefixFingerprint] = entry
			matchedTokens = profile.cacheTokensForBreakpoint(breakpoint.cumulativeTokens)
			break
		}
	}
	newTokens := max(lastBreakpointTokens-matchedTokens, 0)
	out.CacheReadInputTokens = max(matchedTokens, 0)
	out.CacheCreationInputTokens = newTokens
	out.CacheCreation5mInputTokens, out.CacheCreation1hInputTokens = profile.ttlBreakdown(matchedTokens)
	return out
}

func (p *nianzsKiroCacheProfile) cacheTokensForBreakpoint(cumulativeTokens int) int {
	if p == nil {
		return 0
	}
	if !p.scaleBreakpointsToInputTokens {
		return min(max(cumulativeTokens, 0), p.totalInputTokens)
	}
	lastBreakpoint := p.lastCacheableBreakpoint()
	if lastBreakpoint == nil || lastBreakpoint.cumulativeTokens <= 0 {
		return min(max(cumulativeTokens, 0), p.totalInputTokens)
	}
	scaled := int(math.Round(float64(max(cumulativeTokens, 0)) * float64(p.totalInputTokens) / float64(lastBreakpoint.cumulativeTokens)))
	return min(max(scaled, 0), p.totalInputTokens)
}

func (p *nianzsKiroCacheProfile) ttlBreakdown(matchedTokens int) (int, int) {
	lastBreakpoint := p.lastCacheableBreakpoint()
	if lastBreakpoint == nil {
		return 0, 0
	}
	newTokens := max(p.cacheTokensForBreakpoint(lastBreakpoint.cumulativeTokens)-matchedTokens, 0)
	if newTokens == 0 {
		return 0, 0
	}
	if lastBreakpoint.ttl >= nianzsKiroCacheOneHourTTL {
		return 0, newTokens
	}
	return newTokens, 0
}

func (t *nianzsKiroCacheTracker) update(cacheKey uint64, profile *nianzsKiroCacheProfile) {
	if t == nil || profile == nil || cacheKey == 0 {
		return
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	accountEntries := t.entries[cacheKey]
	if accountEntries == nil {
		accountEntries = make(map[[32]byte]nianzsKiroCacheEntry)
		t.entries[cacheKey] = accountEntries
	}
	for _, breakpoint := range profile.cacheableBreakpoints() {
		block := profile.blocks[breakpoint.blockIndex]
		expiresAt := now.Add(breakpoint.ttl)
		entry, ok := accountEntries[block.prefixFingerprint]
		if ok {
			entry.tokens = max(entry.tokens, block.cumulativeTokens)
			entry.ttl = nianzsMaxDuration(entry.ttl, breakpoint.ttl)
			if expiresAt.After(entry.expiresAt) {
				entry.expiresAt = expiresAt
			}
			accountEntries[block.prefixFingerprint] = entry
			continue
		}
		accountEntries[block.prefixFingerprint] = nianzsKiroCacheEntry{tokens: block.cumulativeTokens, ttl: breakpoint.ttl, expiresAt: expiresAt}
	}
}

func (t *nianzsKiroCacheTracker) pruneLocked(now time.Time) {
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

func nianzsKiroCacheCredentialKey(account *Account) uint64 {
	stableKey := strings.TrimSpace(nianzsKiroCacheCredentialIdentity(account))
	if stableKey == "" {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(stableKey))
	return h.Sum64()
}

func nianzsKiroCacheCredentialIdentity(account *Account) string {
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

// nianzsKiroMinimumCacheableTokens 返回「前缀至少多少 token 才值得记进缓存」的阈值。
// 目前只有两档特例：GPT-5.6 系列取 1024（对齐 OpenAI 官方最小缓存粒度），opus 系取
// 4096；其余 Kiro 模型走默认档。GPT 用 nianzskiro.IsKiroGPTModel 精确匹配，opus 仍用
// 子串以覆盖 -thinking 与带日期后缀的变体。
// 各模型的期望值由 TestKiroMinimumCacheableTokens 钉死。
func nianzsKiroMinimumCacheableTokens(model string) int {
	if nianzskiro.IsKiroGPTModel(model) {
		return nianzsKiroCacheMinTokensGPT
	}
	if strings.Contains(strings.ToLower(model), "opus") {
		return nianzsKiroCacheMinTokensOpus
	}
	return nianzsKiroCacheMinTokensDefault
}

func nianzsStripKiroCacheControl(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, child := range x {
			if k == "cache_control" {
				continue
			}
			out[k] = nianzsStripKiroCacheControl(child)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = nianzsStripKiroCacheControl(child)
		}
		return out
	default:
		return v
	}
}

func nianzsCountKiroInputTokensFromPayload(ctx context.Context, payload map[string]any) int {
	if payload == nil {
		return 1
	}
	model, _ := payload["model"].(string)
	if nianzsUsesModernClaudeInputAccounting(model) {
		return nianzsCountModernClaudeInputTokensFromPayload(ctx, payload, model)
	}
	tokens := 0
	for _, block := range nianzsNormalizeKiroSystemBlocks(payload["system"]) {
		tokens += nianzsCountKiroSystemBlockTokens(block)
	}
	messages, _ := payload["messages"].([]any)
	if len(messages) > 0 {
		sanitizedMessages, imageTokens := nianzsSanitizeKiroImagesForTokenEstimate(ctx, messages)
		canonical, err := nianzsCanonicalJSON(sanitizedMessages)
		if err == nil {
			tokens += anthropictokenizer.CountTokens(string(canonical))
		}
		tokens += imageTokens
		tokens += len(messages) * nianzsKiroTokensPerMessage
	}
	if tools, ok := payload["tools"].([]any); ok {
		for _, tool := range tools {
			tokens += nianzsCountKiroToolDefinitionTokens(tool)
		}
	}
	return max(tokens, 1)
}

func nianzsUsesModernClaudeInputAccounting(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{
		"claude-opus-4-7", "claude-opus-4.7",
		"claude-opus-4-8", "claude-opus-4.8",
		"claude-opus-5", "claude-sonnet-5", "claude-haiku-5",
		"claude-fable-5", "claude-mythos-5",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func nianzsScaleModernClaudeTokens(tokens, numerator, denominator int) int {
	if tokens <= 0 || numerator <= 0 || denominator <= 0 {
		return 0
	}
	return int(math.Round(float64(tokens) * float64(numerator) / float64(denominator)))
}

func nianzsCountModernClaudeSystemBlockTokens(value any) int {
	legacy := nianzsCountKiroSystemBlockTokens(value)
	if legacy <= 0 {
		return 0
	}
	return nianzsScaleModernClaudeTokens(
		legacy,
		nianzsModernClaudeSystemScaleNumerator,
		nianzsModernClaudeSystemScaleDenominator,
	) + nianzsModernClaudeSystemBlockOverhead
}

func nianzsCountModernClaudeToolDefinitionTokens(value any) int {
	canonical, err := nianzsCanonicalJSON(nianzsStripKiroCacheControl(value))
	if err != nil {
		return nianzsModernClaudeToolEnvelopeTokens
	}
	raw := anthropictokenizer.CountTokens(string(canonical))
	return nianzsScaleModernClaudeTokens(
		raw,
		nianzsModernClaudeToolScaleNumerator,
		nianzsModernClaudeToolScaleDenominator,
	) + nianzsModernClaudeToolEnvelopeTokens
}

func nianzsCountModernClaudeMessageContentTokens(ctx context.Context, value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case string:
		return anthropictokenizer.EstimateModernClaudeTextTokens(typed)
	case []any:
		total := 0
		for _, item := range typed {
			total += nianzsCountModernClaudeMessageContentTokens(ctx, item)
		}
		return total
	case map[string]any:
		if mediaType, source, ok := nianzsKiroImageTokenSource(typed); ok {
			return nianzskiro.EstimateImageTokens(ctx, mediaType, source)
		}
		if text, ok := typed["text"].(string); ok {
			return anthropictokenizer.EstimateModernClaudeTextTokens(text)
		}
		if thinking, ok := typed["thinking"].(string); ok {
			return anthropictokenizer.EstimateModernClaudeTextTokens(thinking)
		}
		if input, ok := typed["input"]; ok {
			canonical, err := nianzsCanonicalJSON(input)
			if err == nil {
				return anthropictokenizer.EstimateModernClaudeTextTokens(string(canonical))
			}
		}
		if content, ok := typed["content"]; ok {
			return nianzsCountModernClaudeMessageContentTokens(ctx, content)
		}
	}
	return 0
}

func nianzsCountModernClaudeMessagesTokens(ctx context.Context, messages []any) int {
	if len(messages) == 0 {
		return 0
	}
	sanitizedMessages, imageTokens := nianzsSanitizeKiroImagesForTokenEstimate(ctx, messages)
	canonical, err := nianzsCanonicalJSON(sanitizedMessages)
	if err != nil {
		return len(messages) * nianzsKiroTokensPerMessage
	}
	return anthropictokenizer.EstimateModernClaudeTextTokens(string(canonical)) + imageTokens + len(messages)*nianzsKiroTokensPerMessage
}

func nianzsCountModernClaudeInputTokensFromPayload(ctx context.Context, payload map[string]any, model string) int {
	tokens := 0
	for _, block := range nianzsNormalizeKiroSystemBlocks(payload["system"]) {
		tokens += nianzsCountModernClaudeSystemBlockTokens(block)
	}
	tools, _ := payload["tools"].([]any)
	for _, tool := range tools {
		tokens += nianzsCountModernClaudeToolDefinitionTokens(tool)
	}
	messages, _ := payload["messages"].([]any)
	tokens += nianzsCountModernClaudeMessagesTokens(ctx, messages)
	tokens += nianzsModernClaudeCachedToolBoundaryTokens(payload, model)
	return max(tokens, 1)
}

func nianzsModernClaudeCachedToolBoundaryTokens(payload map[string]any, model string) int {
	tools, _ := payload["tools"].([]any)
	if len(tools) == 0 {
		return 0
	}
	cachedTools := 0
	for index, tool := range tools {
		if nianzsExtractKiroCacheTTL(tool) != nil {
			cachedTools = index + 1
		}
	}
	for _, block := range nianzsNormalizeKiroSystemBlocks(payload["system"]) {
		if nianzsExtractKiroCacheTTL(block) != nil {
			cachedTools = len(tools)
		}
	}
	messages, _ := payload["messages"].([]any)
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		if content, ok := message["content"].([]any); ok {
			for _, block := range content {
				if nianzsExtractKiroCacheTTL(block) != nil {
					cachedTools = len(tools)
				}
			}
		}
	}
	if cachedTools == 0 {
		return 0
	}
	cacheableEstimate := 0
	for _, tool := range tools[:cachedTools] {
		cacheableEstimate += nianzsCountModernClaudeToolDefinitionTokens(tool)
	}
	for _, block := range nianzsNormalizeKiroSystemBlocks(payload["system"]) {
		cacheableEstimate += nianzsCountModernClaudeSystemBlockTokens(block)
	}
	if cacheableEstimate < nianzsKiroMinimumCacheableTokens(model) {
		return 0
	}
	return int(math.Round(float64(cachedTools*nianzsModernClaudeCachedToolBoundaryNum) / float64(nianzsModernClaudeCachedToolBoundaryDen)))
}

func nianzsApplyModernClaudeCacheBlockTokens(ctx context.Context, blocks []nianzsKiroPendingBlock) {
	for index := range blocks {
		wrapper, _ := blocks[index].value.(map[string]any)
		kind, _ := wrapper["kind"].(string)
		switch kind {
		case "tool":
			blocks[index].tokens = nianzsCountModernClaudeToolDefinitionTokens(wrapper["tool"])
		case "system":
			blocks[index].tokens = nianzsCountModernClaudeSystemBlockTokens(wrapper["block"])
		case "message":
			blocks[index].tokens = nianzsCountModernClaudeMessageContentTokens(ctx, wrapper["block"])
		}
	}
}

func nianzsCountKiroToolDefinitionTokens(value any) int {
	canonical, err := nianzsCanonicalJSON(nianzsStripKiroCacheControl(value))
	if err != nil {
		return nianzsKiroTokensPerTool
	}
	return max(anthropictokenizer.CountTokens(string(canonical)), nianzsKiroTokensPerTool)
}

func nianzsCountKiroSystemBlockTokens(value any) int {
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

func nianzsCountKiroMessageContentTokens(ctx context.Context, value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case string:
		return anthropictokenizer.CountTokens(typed)
	case []any:
		total := 0
		for _, item := range typed {
			total += nianzsCountKiroMessageContentTokens(ctx, item)
		}
		return total
	case map[string]any:
		if mediaType, source, ok := nianzsKiroImageTokenSource(typed); ok {
			return nianzskiro.EstimateImageTokens(ctx, mediaType, source)
		}
		if text, ok := typed["text"].(string); ok {
			return anthropictokenizer.CountTokens(text)
		}
		if thinking, ok := typed["thinking"].(string); ok {
			return anthropictokenizer.CountTokens(thinking)
		}
		if input, ok := typed["input"]; ok {
			return nianzsCountKiroSerializedValueTokens(input)
		}
		if content, ok := typed["content"]; ok {
			return nianzsCountKiroMessageContentTokens(ctx, content)
		}
		return 0
	default:
		return 0
	}
}

func nianzsSanitizeKiroImagesForTokenEstimate(ctx context.Context, value any) (any, int) {
	switch typed := value.(type) {
	case []any:
		out := make([]any, len(typed))
		tokens := 0
		for i, item := range typed {
			out[i], tokens = nianzsSanitizeKiroImageItem(ctx, item, tokens)
		}
		return out, tokens
	case map[string]any:
		if mediaType, source, ok := nianzsKiroImageTokenSource(typed); ok {
			return nianzsSanitizeKiroImageBlock(typed), nianzskiro.EstimateImageTokens(ctx, mediaType, source)
		}
		out := make(map[string]any, len(typed))
		tokens := 0
		for key, item := range typed {
			out[key], tokens = nianzsSanitizeKiroImageItem(ctx, item, tokens)
		}
		return out, tokens
	default:
		return value, 0
	}
}

func nianzsSanitizeKiroImageItem(ctx context.Context, value any, currentTokens int) (any, int) {
	sanitized, tokens := nianzsSanitizeKiroImagesForTokenEstimate(ctx, value)
	return sanitized, currentTokens + tokens
}

func nianzsSanitizeKiroImageBlock(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		switch typed := item.(type) {
		case map[string]any:
			out[key] = nianzsSanitizeKiroImageBlock(typed)
		case []any:
			items := make([]any, len(typed))
			for i, child := range typed {
				if childMap, ok := child.(map[string]any); ok {
					items[i] = nianzsSanitizeKiroImageBlock(childMap)
				} else {
					items[i] = child
				}
			}
			out[key] = items
		case string:
			lowerKey := strings.ToLower(key)
			if lowerKey == "data" || lowerKey == "url" || lowerKey == "image_url" {
				out[key] = "[image]"
			} else {
				out[key] = typed
			}
		default:
			out[key] = item
		}
	}
	return out
}

func nianzsKiroImageTokenSource(value map[string]any) (mediaType, source string, ok bool) {
	kind, _ := value["type"].(string)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "image":
		mediaType, source = nianzsKiroImageSourceFields(value)
		return mediaType, source, true
	case "image_url", "input_image":
		mediaType, source = nianzsKiroImageSourceFields(value)
		if raw, exists := value["image_url"]; exists {
			switch typed := raw.(type) {
			case string:
				source = typed
			case map[string]any:
				if url, found := typed["url"].(string); found {
					source = url
				}
			}
		}
		return mediaType, source, true
	default:
		return "", "", false
	}
}

func nianzsKiroImageSourceFields(value map[string]any) (mediaType, source string) {
	container := value
	if nested, ok := value["source"].(map[string]any); ok {
		container = nested
	}
	for _, key := range []string{"media_type", "mediaType", "mime_type"} {
		if candidate, ok := container[key].(string); ok && strings.TrimSpace(candidate) != "" {
			mediaType = candidate
			break
		}
	}
	for _, key := range []string{"data", "url"} {
		if candidate, ok := container[key].(string); ok && strings.TrimSpace(candidate) != "" {
			source = candidate
			break
		}
	}
	return mediaType, source
}

func nianzsCountKiroSerializedValueTokens(value any) int {
	canonical, err := nianzsCanonicalJSON(value)
	if err != nil {
		return 0
	}
	return anthropictokenizer.CountTokens(string(canonical))
}

func nianzsCanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := nianzsWriteCanonicalJSON(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func nianzsWriteCanonicalJSON(buf *bytes.Buffer, v any) error {
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
			if err := nianzsWriteCanonicalJSON(buf, x[k]); err != nil {
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
			if err := nianzsWriteCanonicalJSON(buf, child); err != nil {
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

func nianzsKiroCacheAsString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func nianzsMinDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func nianzsMaxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func (u *nianzsKiroCacheEmulationUsage) toKiroUsage() *nianzskiro.Usage {
	if u == nil {
		return nil
	}
	return &nianzskiro.Usage{
		InputTokens:                u.InputTokens,
		CacheReadInputTokens:       u.CacheReadInputTokens,
		CacheCreationInputTokens:   u.CacheCreationInputTokens,
		CacheCreation5mInputTokens: u.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: u.CacheCreation1hInputTokens,
	}
}
