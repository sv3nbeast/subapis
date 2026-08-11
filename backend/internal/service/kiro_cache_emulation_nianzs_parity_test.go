// Mechanically namespaced tests from nianzs/sub2api at
// d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/anthropictokenizer"
	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/stretchr/testify/require"
)

func TestNianzsKiroCacheEmulationGroupDefaultsAndNonKiro(t *testing.T) {
	kiro := &Group{Platform: PlatformKiro, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: 0.5}
	if !kiro.EffectiveKiroCacheEmulationEnabled() {
		t.Fatal("kiro group should enable cache emulation")
	}
	if got := kiro.EffectiveKiroCacheEmulationRatio(); got != 0.5 {
		t.Fatalf("ratio = %v, want 0.5", got)
	}
	nonKiro := &Group{Platform: PlatformAnthropic, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: 1}
	NormalizeGroupRuntimeFields(nonKiro)
	if nonKiro.KiroCacheEmulationEnabled || nonKiro.KiroCacheEmulationRatio != 0 {
		t.Fatalf("non-kiro fields were not normalized: %+v", nonKiro)
	}
}

func TestNianzsKiroCacheEmulationUsesSnapshotGroupWithoutRepo(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 34, Platform: PlatformKiro}
	group := nianzsTestKiroCacheGroup(1)
	first := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, nianzsTestKiroCacheRequestBody("stable", false), "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 || first.CacheReadInputTokens != 0 || first.InputTokens != 0 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, nianzsTestKiroCacheRequestBody("stable", false), "claude-sonnet-4-6", 2000)
	if second == nil || second.CacheReadInputTokens != 2000 || second.CacheCreationInputTokens != 0 || second.InputTokens != 0 {
		t.Fatalf("unexpected second usage: %+v", second)
	}
}

// prepareKiroCacheEmulationUsageNianzs 在 commit() 之前不得改动 tracker：连续两次 prepare
// 且都不 commit，应当观察到完全相同的（未命中）状态，证明 prepare 从未写入缓存条目。
func TestNianzsKiroCacheEmulationPrepareDoesNotMutateUntilCommit(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 55, Platform: PlatformKiro}
	group := nianzsTestKiroCacheGroup(1)
	body := nianzsTestKiroCacheRequestBody("deferred commit", false)

	planA := svc.prepareKiroCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-sonnet-4-6", 2000)
	require.NotNil(t, planA)
	usageA := planA.result()
	require.NotNil(t, usageA)
	require.Equal(t, 2000, usageA.CacheCreationInputTokens)
	require.Equal(t, 0, usageA.CacheReadInputTokens)

	// 未 commit：同样内容的第二次 prepare 仍应观察到未命中，
	// 证明第一次 prepare 没有写入 tracker。
	planB := svc.prepareKiroCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-sonnet-4-6", 2000)
	require.NotNil(t, planB)
	usageB := planB.result()
	require.NotNil(t, usageB)
	require.Equal(t, 2000, usageB.CacheCreationInputTokens)
	require.Equal(t, 0, usageB.CacheReadInputTokens)

	// 提交后，后续 prepare 应观察到缓存命中。
	planB.commit()
	planC := svc.prepareKiroCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-sonnet-4-6", 2000)
	require.NotNil(t, planC)
	usageC := planC.result()
	require.NotNil(t, usageC)
	require.Equal(t, 2000, usageC.CacheReadInputTokens)
	require.Equal(t, 0, usageC.CacheCreationInputTokens)
}

func TestNianzsKiroCacheEmulationRatioScalesTokens(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 78, Platform: PlatformKiro}
	usage := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, nianzsTestKiroCacheGroup(0.5), nianzsTestKiroCacheRequestBody("ratio", false), "claude-sonnet-4-6", 2000)
	if usage == nil || usage.CacheCreationInputTokens != 1000 || usage.InputTokens != 1000 {
		t.Fatalf("unexpected scaled usage: %+v", usage)
	}
	disabled := nianzsTestKiroCacheGroup(1)
	disabled.KiroCacheEmulationEnabled = false
	if got := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, disabled, nianzsTestKiroCacheRequestBody("disabled", false), "claude-sonnet-4-6", 2000); got != nil {
		t.Fatalf("disabled group should skip cache emulation, got %+v", got)
	}
}

func TestNianzsKiroCacheEmulationIndependentRatiosScaleSeparately(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 79, Platform: PlatformKiro}
	group := nianzsTestKiroCacheGroup(1)
	group.KiroCacheEmulationMode = KiroCacheEmulationModeIndependent
	group.KiroCacheCreationEmulationRatio = 0.75
	group.KiroCacheReadEmulationRatio = 0.25
	body := nianzsTestKiroCacheRequestBody("independent ratios", false)

	first := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-sonnet-4-6", 2000)
	require.NotNil(t, first)
	require.Equal(t, 1500, first.CacheCreationInputTokens)
	require.Zero(t, first.CacheReadInputTokens)
	require.Equal(t, 500, first.InputTokens)

	second := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-sonnet-4-6", 2000)
	require.NotNil(t, second)
	require.Zero(t, second.CacheCreationInputTokens)
	require.Equal(t, 500, second.CacheReadInputTokens)
	require.Equal(t, 1500, second.InputTokens)
}

func TestNianzsScaleKiroCacheCreationTTLTokensPreservesScaledTotal(t *testing.T) {
	tokens5m, tokens1h := nianzsScaleKiroCacheCreationTTLTokens(2000, 0, 1000, 0.5)
	require.Equal(t, 1000, tokens5m+tokens1h)
	require.Equal(t, 1000, tokens5m)
	require.Zero(t, tokens1h)

	tokens5m, tokens1h = nianzsScaleKiroCacheCreationTTLTokens(0, 2000, 1000, 0.5)
	require.Equal(t, 1000, tokens5m+tokens1h)
	require.Zero(t, tokens5m)
	require.Equal(t, 1000, tokens1h)

	tokens5m, tokens1h = nianzsScaleKiroCacheCreationTTLTokens(0, 0, 1000, 0.5)
	require.Zero(t, tokens5m)
	require.Zero(t, tokens1h)
}

func TestNianzsScaleKiroCacheCreationTTLTokensHandlesFutureMixedBuckets(t *testing.T) {
	tokens5m, tokens1h := nianzsScaleKiroCacheCreationTTLTokens(1001, 999, 1000, 0.5)
	require.Equal(t, 1000, tokens5m+tokens1h)
	require.Equal(t, 501, tokens5m)
	require.Equal(t, 499, tokens1h)

	tokens5m, tokens1h = nianzsScaleKiroCacheCreationTTLTokens(3000, 1, 1000, 0.5)
	require.Equal(t, 1000, tokens5m)
	require.Zero(t, tokens1h)
}

func TestNianzsKiroCacheEmulationAccountIsolation(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	group := nianzsTestKiroCacheGroup(1)
	body := nianzsTestKiroCacheRequestBody("account isolation", false)
	first := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), nianzsTestKiroCacheAccount(1, "refresh-a", "access-a"), group, body, "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	otherAccount := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), nianzsTestKiroCacheAccount(2, "refresh-b", "access-b"), group, body, "claude-sonnet-4-6", 2000)
	if otherAccount == nil || otherAccount.CacheCreationInputTokens != 2000 || otherAccount.CacheReadInputTokens != 0 {
		t.Fatalf("cache should be isolated by account: %+v", otherAccount)
	}
}

func TestNianzsKiroCacheEmulationStableCredentialIsolation(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	group := nianzsTestKiroCacheGroup(1)
	body := nianzsTestKiroCacheRequestBody("credential isolation", false)
	first := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), nianzsTestKiroCacheAccount(7, "refresh-same", "access-a"), group, body, "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	rotatedAccessToken := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), nianzsTestKiroCacheAccount(7, "refresh-same", "access-b"), group, body, "claude-sonnet-4-6", 2000)
	if rotatedAccessToken == nil || rotatedAccessToken.CacheReadInputTokens != 2000 || rotatedAccessToken.CacheCreationInputTokens != 0 {
		t.Fatalf("access token rotation should not break cache: %+v", rotatedAccessToken)
	}
	differentCredential := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), nianzsTestKiroCacheAccount(7, "refresh-other", "access-c"), group, body, "claude-sonnet-4-6", 2000)
	if differentCredential == nil || differentCredential.CacheReadInputTokens != 0 || differentCredential.CacheCreationInputTokens != 2000 {
		t.Fatalf("different stable credential should not share cache: %+v", differentCredential)
	}
}

func TestNianzsKiroCacheEmulationContentChangeMisses(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 3, Platform: PlatformKiro}
	group := nianzsTestKiroCacheGroup(1)
	_ = svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, nianzsTestKiroCacheRequestBody("before", false), "claude-sonnet-4-6", 2000)
	changed := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, nianzsTestKiroCacheRequestBody("after", false), "claude-sonnet-4-6", 2000)
	if changed == nil || changed.CacheCreationInputTokens != 2000 || changed.CacheReadInputTokens != 0 {
		t.Fatalf("changed content should miss: %+v", changed)
	}
}

func TestNianzsKiroCacheEmulationTTLExpiry(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 4, Platform: PlatformKiro}
	group := nianzsTestKiroCacheGroup(1)
	body := nianzsTestKiroCacheRequestBody("ttl", false)
	_ = svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-sonnet-4-6", 2000)
	nianzsGlobalKiroCacheTracker.mu.Lock()
	for accountID, entries := range nianzsGlobalKiroCacheTracker.entries {
		for fp, entry := range entries {
			entry.expiresAt = time.Now().Add(-time.Second)
			nianzsGlobalKiroCacheTracker.entries[accountID][fp] = entry
		}
	}
	nianzsGlobalKiroCacheTracker.mu.Unlock()
	afterExpiry := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-sonnet-4-6", 2000)
	if afterExpiry == nil || afterExpiry.CacheCreationInputTokens != 2000 || afterExpiry.CacheReadInputTokens != 0 {
		t.Fatalf("expired cache should be recreated: %+v", afterExpiry)
	}
}

func TestNianzsKiroCacheEmulationOneHourBucket(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	usage := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), &Account{ID: 5, Platform: PlatformKiro}, nianzsTestKiroCacheGroup(1), nianzsTestKiroCacheRequestBody("1h", true), "claude-sonnet-4-6", 2000)
	if usage == nil || usage.CacheCreationInputTokens != 2000 || usage.CacheCreation1hInputTokens != 2000 || usage.CacheCreation5mInputTokens != 0 {
		t.Fatalf("unexpected 1h bucket usage: %+v", usage)
	}
}

func TestNianzsKiroCacheEmulationPrefixPartialHit(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 6, Platform: PlatformKiro}
	group := nianzsTestKiroCacheGroup(1)
	firstBody := nianzsTestKiroCacheMultiMessageBody("cached prefix", "tail one")
	secondBody := nianzsTestKiroCacheMultiMessageBody("cached prefix", "tail two")
	first := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, firstBody, "claude-sonnet-4-6", 6000)
	if first == nil || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, secondBody, "claude-sonnet-4-6", 6000)
	if second == nil || second.CacheReadInputTokens <= 0 || second.CacheReadInputTokens >= first.CacheCreationInputTokens || second.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected partial prefix hit: %+v", second)
	}
}

func TestNianzsKiroInputTokenEstimateIgnoresClientMetadata(t *testing.T) {
	bodyWithoutMetadata := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello world"}]}`)
	bodyWithMetadata := []byte(`{"model":"claude-sonnet-4-6","metadata":{"input_tokens":999999},"messages":[{"role":"user","content":"hello world"}]}`)
	withoutMetadata := nianzsEstimateKiroInputTokens(context.Background(), bodyWithoutMetadata)
	withMetadata := nianzsEstimateKiroInputTokens(context.Background(), bodyWithMetadata)
	if withMetadata == 999999 {
		t.Fatal("client metadata.input_tokens must not be trusted")
	}
	if withMetadata <= 0 || withoutMetadata <= 0 || withMetadata > withoutMetadata*2 {
		t.Fatalf("unexpected estimates without=%d with=%d", withoutMetadata, withMetadata)
	}
}

func TestNianzsKiroTokenCountersMatchReferenceRules(t *testing.T) {
	if got := anthropictokenizer.CountTokens("abc def"); got != 1 {
		t.Fatalf("english tokens = %d, want 1", got)
	}
	if got := anthropictokenizer.CountTokens("你好世界"); got != 1 {
		t.Fatalf("cjk tokens = %d, want 1", got)
	}
	if nianzsKiroTokensPerTool != 150 {
		t.Fatalf("tool tokens = %d, want 150", nianzsKiroTokensPerTool)
	}
	if got := nianzsCountKiroMessageContentTokens(context.Background(), map[string]any{"thinking": "abc def"}); got != 1 {
		t.Fatalf("thinking tokens = %d, want 1", got)
	}
	if got := nianzsCountKiroMessageContentTokens(context.Background(), map[string]any{"input": map[string]any{"path": "/tmp/a.txt"}}); got <= 0 {
		t.Fatalf("tool input tokens should be positive, got %d", got)
	}
	if got := nianzsCountKiroMessageContentTokens(context.Background(), map[string]any{"content": []any{map[string]any{"text": "abc"}, map[string]any{"text": "你好"}}}); got != 2 {
		t.Fatalf("tool result content tokens = %d, want 2", got)
	}
}

func TestNianzsKiroInputTokenEstimateSeparatesVisualTokensFromBase64(t *testing.T) {
	dataURL := nianzsTestKiroPNGDataURL(t, 512, 512, color.RGBA{R: 37, G: 89, B: 151, A: 255})
	body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}]}`, strings.TrimPrefix(dataURL, "data:image/png;base64,")))

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	sanitized, imageTokens := nianzsSanitizeKiroImagesForTokenEstimate(context.Background(), payload["messages"])
	canonical, err := nianzsCanonicalJSON(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte(strings.TrimPrefix(dataURL, "data:image/png;base64,"))) {
		t.Fatal("sanitized token payload must not retain image base64")
	}
	if imageTokens != 350 {
		t.Fatalf("image tokens = %d, want 350", imageTokens)
	}

	got := nianzsEstimateKiroInputTokens(context.Background(), body)
	want := anthropictokenizer.CountTokens("describe") + imageTokens
	if got < want || got > want+50 {
		t.Fatalf("input token estimate = %d, expected visual-aware estimate near %d", got, want)
	}
}

func TestNianzsKiroImageTokenSourcesSupportAnthropicAndOpenAIShapes(t *testing.T) {
	dataURL := nianzsTestKiroPNGDataURL(t, 200, 200, color.RGBA{A: 255})
	base64Data := strings.TrimPrefix(dataURL, "data:image/png;base64,")
	tests := []map[string]any{
		{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": base64Data}},
		{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
		{"type": "input_image", "image_url": dataURL},
	}
	for _, block := range tests {
		if got := nianzsCountKiroMessageContentTokens(context.Background(), block); got != 54 {
			t.Fatalf("image block %#v tokens = %d, want 54", block, got)
		}
	}
}

func TestNianzsKiroCacheEmulationIncludesImageTokensAndKeepsImageFingerprint(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(91, "refresh-image", "access-image")
	group := nianzsTestKiroCacheGroup(1)
	prefix := strings.Repeat("cacheable visual prompt ", 700)
	body := nianzsTestKiroCacheImageRequestBody(t, prefix, color.RGBA{R: 1, A: 255})
	inputTokens := nianzsEstimateKiroInputTokens(context.Background(), body)

	first := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-sonnet-4-6", inputTokens)
	if first == nil || first.CacheCreationInputTokens <= 0 || first.CacheReadInputTokens != 0 {
		t.Fatalf("unexpected first image cache usage: %+v", first)
	}
	if first.InputTokens+first.CacheCreationInputTokens+first.CacheReadInputTokens != inputTokens {
		t.Fatalf("first image cache token totals do not balance: usage=%+v total=%d", first, inputTokens)
	}

	second := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-sonnet-4-6", inputTokens)
	if second == nil || second.CacheReadInputTokens <= 0 {
		t.Fatalf("same image should hit cache: %+v", second)
	}
	if second.InputTokens+second.CacheCreationInputTokens+second.CacheReadInputTokens != inputTokens {
		t.Fatalf("second image cache token totals do not balance: usage=%+v total=%d", second, inputTokens)
	}

	changedBody := nianzsTestKiroCacheImageRequestBody(t, prefix, color.RGBA{G: 1, A: 255})
	changedTokens := nianzsEstimateKiroInputTokens(context.Background(), changedBody)
	changed := svc.buildKiroCacheEmulationUsageNianzs(context.Background(), account, group, changedBody, "claude-sonnet-4-6", changedTokens)
	if changed == nil || changed.CacheReadInputTokens != 0 || changed.CacheCreationInputTokens <= 0 {
		t.Fatalf("different image must miss cache: %+v", changed)
	}
}

func TestNianzsKiroResponsesCacheEmulationUsesFullInputPrefix(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(101, "refresh-responses", "access-responses")
	group := nianzsTestKiroCacheGroup(1)
	body := nianzsTestKiroResponsesCacheRequestBody("stable", "workspace-a", "resp-a")

	first := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, body, "gpt-5", 2400)
	if first == nil || first.CacheCreationInputTokens != 2400 || first.CacheReadInputTokens != 0 || first.InputTokens != 0 {
		t.Fatalf("unexpected first responses usage: %+v", first)
	}

	second := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, body, "gpt-5", 2400)
	if second == nil || second.CacheReadInputTokens != 2400 || second.CacheCreationInputTokens != 0 || second.InputTokens != 0 {
		t.Fatalf("unexpected second responses usage: %+v", second)
	}
}

func TestNianzsKiroResponsesCacheEmulationHitsStableHistoryPrefix(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(106, "refresh-responses-history", "access-responses-history")
	group := nianzsTestKiroCacheGroup(1)
	prefixText := strings.Repeat("stable codex history prefix ", 640)
	firstBody := nianzsTestKiroResponsesConversationRequestBody("workspace-history", []string{prefixText})
	secondBody := nianzsTestKiroResponsesConversationRequestBody("workspace-history", []string{prefixText, strings.Repeat("new codex tail ", 160)})

	first := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, firstBody, "gpt-5", 2600)
	if first == nil || first.CacheReadInputTokens != 0 || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("first request should create cache: %+v", first)
	}

	second := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, secondBody, "gpt-5", 3200)
	if second == nil || second.CacheReadInputTokens <= 0 || second.CacheCreationInputTokens <= 0 {
		t.Fatalf("grown conversation should read stable prefix and create tail: %+v", second)
	}
	if second.CacheReadInputTokens >= 3200 {
		t.Fatalf("grown conversation should not treat the whole request as cache read: %+v", second)
	}
}

func TestNianzsKiroResponsesCacheEmulationDoesNotReadChangedLatestItem(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(107, "refresh-responses-tail", "access-responses-tail")
	group := nianzsTestKiroCacheGroup(1)
	stablePrefix := strings.Repeat("stable codex history prefix ", 640)
	firstBody := nianzsTestKiroResponsesConversationRequestBody("workspace-tail", []string{stablePrefix, strings.Repeat("first latest item ", 180)})
	secondBody := nianzsTestKiroResponsesConversationRequestBody("workspace-tail", []string{stablePrefix, strings.Repeat("changed latest item ", 180)})

	first := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, firstBody, "gpt-5", 3200)
	if first == nil || first.CacheCreationInputTokens <= 0 || first.CacheReadInputTokens != 0 {
		t.Fatalf("first request should create cache: %+v", first)
	}

	second := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, secondBody, "gpt-5", 3200)
	if second == nil || second.CacheReadInputTokens <= 0 || second.CacheCreationInputTokens <= 0 {
		t.Fatalf("changed latest item should read stable prefix and create changed tail: %+v", second)
	}
	if second.CacheReadInputTokens >= first.CacheCreationInputTokens {
		t.Fatalf("changed latest item should not be treated as a full cache read: first=%+v second=%+v", first, second)
	}
}

func TestNianzsKiroResponsesCacheEmulationPromptCacheKeyIsolatesNamespaces(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(102, "refresh-responses-key", "access-responses-key")
	group := nianzsTestKiroCacheGroup(1)
	bodyA := nianzsTestKiroResponsesCacheRequestBody("same", "workspace-a", "resp-a")
	bodyB := nianzsTestKiroResponsesCacheRequestBody("same", "workspace-b", "resp-a")

	_ = svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, bodyA, "gpt-5", 2400)
	otherNamespace := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, bodyB, "gpt-5", 2400)
	if otherNamespace == nil || otherNamespace.CacheReadInputTokens != 0 || otherNamespace.CacheCreationInputTokens != 2400 {
		t.Fatalf("different prompt_cache_key should miss: %+v", otherNamespace)
	}
}

func TestNianzsKiroResponsesCacheEmulationPreviousResponseIDIsolatesNamespaces(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(103, "refresh-responses-prev", "access-responses-prev")
	group := nianzsTestKiroCacheGroup(1)
	bodyA := nianzsTestKiroResponsesCacheRequestBody("same", "workspace-a", "resp-a")
	bodyB := nianzsTestKiroResponsesCacheRequestBody("same", "workspace-a", "resp-b")

	_ = svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, bodyA, "gpt-5", 2400)
	otherPrevious := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, bodyB, "gpt-5", 2400)
	if otherPrevious == nil || otherPrevious.CacheReadInputTokens != 0 || otherPrevious.CacheCreationInputTokens != 2400 {
		t.Fatalf("different previous_response_id should miss: %+v", otherPrevious)
	}
}

func TestNianzsKiroResponsesCacheEmulationPreludeFieldsAffectFingerprint(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(104, "refresh-responses-prelude", "access-responses-prelude")
	group := nianzsTestKiroCacheGroup(1)
	base := nianzsTestKiroResponsesCacheRequestBodyWithOptions("same", "workspace-a", "resp-a", "gpt-5", "auto", "medium", `{"type":"json_object"}`, "lookup")
	changed := nianzsTestKiroResponsesCacheRequestBodyWithOptions("same", "workspace-a", "resp-a", "gpt-5-mini", "required", "high", `{"type":"text"}`, "search")

	_ = svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, base, "gpt-5", 2400)
	miss := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, changed, "gpt-5-mini", 2400)
	if miss == nil || miss.CacheReadInputTokens != 0 || miss.CacheCreationInputTokens != 2400 {
		t.Fatalf("model/tools/tool_choice/reasoning/text changes should miss: %+v", miss)
	}
}

func TestNianzsKiroResponsesCacheEmulationIncludesImageFingerprint(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(105, "refresh-responses-image", "access-responses-image")
	group := nianzsTestKiroCacheGroup(1)
	body := nianzsTestKiroResponsesImageCacheRequestBody(t, "same", color.RGBA{R: 1, A: 255})

	first := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, body, "gpt-5", 2400)
	if first == nil || first.CacheCreationInputTokens != 2400 || first.CacheReadInputTokens != 0 {
		t.Fatalf("unexpected first responses image usage: %+v", first)
	}
	second := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, body, "gpt-5", 2400)
	if second == nil || second.CacheReadInputTokens != 2400 || second.CacheCreationInputTokens != 0 {
		t.Fatalf("same responses image should hit: %+v", second)
	}

	changed := nianzsTestKiroResponsesImageCacheRequestBody(t, "same", color.RGBA{G: 1, A: 255})
	miss := svc.buildKiroResponsesCacheEmulationUsageNianzs(context.Background(), account, group, changed, "gpt-5", 2400)
	if miss == nil || miss.CacheReadInputTokens != 0 || miss.CacheCreationInputTokens != 2400 {
		t.Fatalf("different responses image should miss: %+v", miss)
	}
}

func TestNianzsKiroChatCompletionsCacheEmulationHitsStableHistoryPrefix(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(701, "refresh-chat", "access-chat")
	group := nianzsTestKiroCacheGroup(1)

	firstMessage := strings.Repeat("stable chat history chunk ", 700)
	secondMessage := strings.Repeat("latest chat turn chunk one ", 180)
	thirdMessage := strings.Repeat("latest chat turn chunk two ", 180)

	mappedModel := "claude-sonnet-4-6"
	inputTokens := 6000
	firstBody := nianzsTestKiroChatCompletionsConversationBody([]string{firstMessage, secondMessage})
	first := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, firstBody, mappedModel, inputTokens)
	require.NotNil(t, first)
	require.Equal(t, 0, first.CacheReadInputTokens)
	require.Greater(t, first.CacheCreationInputTokens, 0)

	secondBody := nianzsTestKiroChatCompletionsConversationBody([]string{firstMessage, thirdMessage})
	second := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, secondBody, mappedModel, inputTokens)
	require.NotNil(t, second)
	require.Greater(t, second.CacheReadInputTokens, 0)
	require.Greater(t, second.CacheCreationInputTokens, 0)
	require.Less(t, second.CacheCreationInputTokens, first.CacheCreationInputTokens)
}

func TestNianzsKiroChatCompletionsCacheEmulationDoesNotReadChangedHistory(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(702, "refresh-chat", "access-chat")
	group := nianzsTestKiroCacheGroup(1)

	stable := strings.Repeat("stable chat history chunk ", 700)
	latest := strings.Repeat("latest chat turn chunk ", 180)

	mappedModel := "claude-sonnet-4-6"
	inputTokens := 6000
	firstBody := nianzsTestKiroChatCompletionsConversationBody([]string{stable, latest})
	first := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, firstBody, mappedModel, inputTokens)
	require.NotNil(t, first)
	require.Equal(t, 0, first.CacheReadInputTokens)

	changedHistory := strings.Repeat("changed chat history chunk ", 700)
	secondBody := nianzsTestKiroChatCompletionsConversationBody([]string{changedHistory, latest})
	second := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, secondBody, mappedModel, inputTokens)
	require.NotNil(t, second)
	require.Equal(t, 0, second.CacheReadInputTokens)
	require.Greater(t, second.CacheCreationInputTokens, 0)
}

func TestNianzsKiroChatCompletionsCacheEmulationIncludesModelAndToolsInIdentity(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(703, "refresh-chat", "access-chat")
	group := nianzsTestKiroCacheGroup(1)

	stable := strings.Repeat("stable chat history chunk ", 700)
	latest := strings.Repeat("latest chat turn chunk ", 180)
	body := nianzsTestKiroChatCompletionsConversationBody([]string{stable, latest})

	mappedModel := "claude-sonnet-4-6"
	inputTokens := 6000
	first := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, body, mappedModel, inputTokens)
	require.NotNil(t, first)

	otherModel := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, body, "claude-opus-4-1", inputTokens)
	require.NotNil(t, otherModel)
	require.Equal(t, 0, otherModel.CacheReadInputTokens)

	changedToolsBody := []byte(strings.Replace(string(body), `"name":"lookup"`, `"name":"search"`, 1))
	changedTools := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, changedToolsBody, mappedModel, inputTokens)
	require.NotNil(t, changedTools)
	require.Equal(t, 0, changedTools.CacheReadInputTokens)
}

func TestNianzsKiroChatCompletionsCacheEmulationIncludesMessageNameInIdentity(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(704, "refresh-chat", "access-chat")
	group := nianzsTestKiroCacheGroup(1)

	stable := strings.Repeat("stable chat history chunk ", 700)
	latest := strings.Repeat("latest chat turn chunk ", 180)
	mappedModel := "claude-sonnet-4-6"
	inputTokens := 6000

	firstBody := []byte(fmt.Sprintf(`{"model":"gpt-5","messages":[{"role":"user","name":"alice","content":%q},{"role":"user","content":%q}]}`, stable, latest))
	first := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, firstBody, mappedModel, inputTokens)
	require.NotNil(t, first)
	require.Equal(t, 0, first.CacheReadInputTokens)

	changedNameBody := []byte(fmt.Sprintf(`{"model":"gpt-5","messages":[{"role":"user","name":"bob","content":%q},{"role":"user","content":%q}]}`, stable, latest))
	changedName := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, changedNameBody, mappedModel, inputTokens)
	require.NotNil(t, changedName)
	require.Equal(t, 0, changedName.CacheReadInputTokens)
	require.Greater(t, changedName.CacheCreationInputTokens, 0)
}

func TestNianzsKiroChatCompletionsCacheEmulationDoesNotReadInstructionsOnlyPrefix(t *testing.T) {
	resetNianzsKiroCacheTracker()
	svc := &GatewayService{}
	account := nianzsTestKiroCacheAccount(705, "refresh-chat", "access-chat")
	group := nianzsTestKiroCacheGroup(1)

	instructions := strings.Repeat("stable instruction chunk ", 700)
	firstHistory := strings.Repeat("first chat history chunk ", 700)
	secondHistory := strings.Repeat("second chat history chunk ", 700)
	latest := strings.Repeat("latest chat turn chunk ", 180)
	mappedModel := "claude-sonnet-4-6"
	inputTokens := 9000

	firstBody := []byte(fmt.Sprintf(`{"model":"gpt-5","instructions":%q,"messages":[{"role":"user","content":%q},{"role":"user","content":%q}]}`, instructions, firstHistory, latest))
	first := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, firstBody, mappedModel, inputTokens)
	require.NotNil(t, first)
	require.Equal(t, 0, first.CacheReadInputTokens)

	secondBody := []byte(fmt.Sprintf(`{"model":"gpt-5","instructions":%q,"messages":[{"role":"user","content":%q},{"role":"user","content":%q}]}`, instructions, secondHistory, latest))
	second := svc.buildKiroChatCompletionsCacheEmulationUsageNianzs(context.Background(), account, group, secondBody, mappedModel, inputTokens)
	require.NotNil(t, second)
	require.Equal(t, 0, second.CacheReadInputTokens)
	require.Greater(t, second.CacheCreationInputTokens, 0)
}

func resetNianzsKiroCacheTracker() {
	nianzsGlobalKiroCacheTracker = &nianzsKiroCacheTracker{entries: make(map[uint64]map[[32]byte]nianzsKiroCacheEntry)}
}

func nianzsTestKiroPNGDataURL(t *testing.T, width, height int, fill color.RGBA) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func nianzsTestKiroCacheImageRequestBody(t *testing.T, text string, fill color.RGBA) []byte {
	t.Helper()
	dataURL := nianzsTestKiroPNGDataURL(t, 200, 200, fill)
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q},"cache_control":{"type":"ephemeral"}}]}]}`, text, strings.TrimPrefix(dataURL, "data:image/png;base64,")))
}

// nianzsKiroMinimumCacheableTokens 决定「一个前缀至少要多少 token 才值得记进缓存」，直接
// 影响模拟出的 cache_creation / cache_read 分布，进而影响账单。这里把每个 Kiro 实际
// 暴露的模型的阈值钉死：GPT-5.6 三兄弟必须是 1024（对齐 OpenAI 官方最小缓存粒度），
// opus 系必须是 4096。特例断言写字面量、默认档断言写常量名，这样任何一侧被单独改动
// 都会让测试失败，而不是静默漂移。
func TestNianzsKiroMinimumCacheableTokens(t *testing.T) {
	t.Parallel()

	// GPT-5.6 是本用例的核心诉求：1024 是显式契约，不是「恰好等于默认档」。
	for _, model := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		require.Equal(t, 1024, nianzsKiroMinimumCacheableTokens(model), model)
	}

	// opus 系走 4096，含 -thinking 变体与带日期后缀的 4.5。
	for _, model := range []string{
		"claude-opus-5", "claude-opus-5-thinking",
		"claude-opus-4-8", "claude-opus-4-8-thinking",
		"claude-opus-4-5-20251101", "claude-opus-4-5-20251101-thinking",
	} {
		require.Equal(t, 4096, nianzsKiroMinimumCacheableTokens(model), model)
	}

	// 非 opus 的 Claude 走默认档。断言常量而非字面量：默认档本身允许调整，
	// 但调整时必须同步 GPT 的显式 case（GPT 那侧断言的是字面量 1024）。
	for _, model := range []string{
		"claude-sonnet-5", "claude-sonnet-4-6", "claude-haiku-4-5-20251001",
	} {
		require.Equal(t, nianzsKiroCacheMinTokensDefault, nianzsKiroMinimumCacheableTokens(model), model)
	}

	// 遍历 Kiro 实际暴露的全量模型，确保没有未归类的漏网之鱼。上游新增模型时，
	// 这里会立刻因缺少 expected 条目而失败，迫使新模型被显式归类。
	expected := map[string]int{
		"gpt-5.6-sol":                         1024,
		"gpt-5.6-terra":                       1024,
		"gpt-5.6-luna":                        1024,
		"claude-opus-4-8":                     4096,
		"claude-opus-4-8-thinking":            4096,
		"claude-opus-4-7":                     4096,
		"claude-opus-4-7-thinking":            4096,
		"claude-opus-4-6":                     4096,
		"claude-opus-4-6-thinking":            4096,
		"claude-opus-5":                       4096,
		"claude-opus-5-thinking":              4096,
		"claude-opus-4-5-20251101":            4096,
		"claude-opus-4-5-20251101-thinking":   4096,
		"claude-sonnet-5":                     1024,
		"claude-sonnet-5-thinking":            1024,
		"claude-sonnet-4-6":                   1024,
		"claude-sonnet-4-6-thinking":          1024,
		"claude-sonnet-4-5-20250929":          1024,
		"claude-sonnet-4-5-20250929-thinking": 1024,
		"claude-haiku-4-5-20251001":           1024,
		"claude-haiku-4-5-20251001-thinking":  1024,
	}
	for _, model := range nianzskiro.DefaultModels {
		want, ok := expected[model.ID]
		require.Truef(t, ok, "model %q 未在本测试中归类，请为其显式指定最小可缓存 token 数", model.ID)
		require.Equal(t, want, nianzsKiroMinimumCacheableTokens(model.ID), model.ID)
	}
}

func nianzsTestKiroCacheGroup(ratio float64) *Group {
	return &Group{ID: 12, Platform: PlatformKiro, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: ratio}
}

func nianzsTestKiroCacheAccount(id int64, refreshToken string, accessToken string) *Account {
	return &Account{ID: id, Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{
		"client_id":     "client-id",
		"refresh_token": refreshToken,
		"access_token":  accessToken,
	}}
}

func nianzsTestKiroCacheRequestBody(label string, oneHour bool) []byte {
	ttl := ""
	if oneHour {
		ttl = `,"ttl":"1h"`
	}
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q,"cache_control":{"type":"ephemeral"%s}}]}]}`, strings.Repeat("cacheable prompt chunk "+label+" ", 512), ttl))
}

func nianzsTestKiroCacheMultiMessageBody(prefixLabel, tailLabel string) []byte {
	prefix := strings.Repeat("cacheable prompt chunk "+prefixLabel+" ", 512)
	tail := strings.Repeat("conversation growth chunk "+tailLabel+" ", 160)
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q,"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":%q}]}]}`, prefix, tail))
}

func nianzsTestKiroChatCompletionsConversationBody(messages []string) []byte {
	items := make([]string, 0, len(messages)+1)
	items = append(items, `{"role":"system","content":"You are a precise assistant."}`)
	for _, message := range messages {
		items = append(items, fmt.Sprintf(`{"role":"user","content":%q}`, message))
	}
	return []byte(fmt.Sprintf(`{"model":"gpt-5","tool_choice":"auto","tools":[{"type":"function","function":{"name":"lookup","description":"lookup data","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}}],"messages":[%s]}`, strings.Join(items, ",")))
}

func nianzsTestKiroResponsesCacheRequestBody(label, promptCacheKey, previousResponseID string) []byte {
	return nianzsTestKiroResponsesCacheRequestBodyWithOptions(label, promptCacheKey, previousResponseID, "gpt-5", "auto", "medium", `{"type":"json_object"}`, "lookup")
}

func nianzsTestKiroResponsesCacheRequestBodyWithOptions(label, promptCacheKey, previousResponseID, model, toolChoice, effort, textFormat, toolName string) []byte {
	prompt := strings.Repeat("cacheable responses prompt chunk "+label+" ", 512)
	return []byte(fmt.Sprintf(`{"model":%q,"instructions":"You are a precise assistant.","prompt_cache_key":%q,"previous_response_id":%q,"tool_choice":%q,"reasoning":{"effort":%q},"text":{"format":%s},"tools":[{"type":"function","name":%q,"description":"lookup data","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}],"input":[{"role":"user","content":[{"type":"input_text","text":%q}]}]}`, model, promptCacheKey, previousResponseID, toolChoice, effort, textFormat, toolName, prompt))
}

func nianzsTestKiroResponsesConversationRequestBody(promptCacheKey string, messages []string) []byte {
	items := make([]string, 0, len(messages))
	for _, message := range messages {
		items = append(items, fmt.Sprintf(`{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}`, message))
	}
	return []byte(fmt.Sprintf(`{"model":"gpt-5","instructions":"You are a precise assistant.","prompt_cache_key":%q,"tool_choice":"auto","tools":[{"type":"function","name":"lookup","description":"lookup data","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}],"input":[%s]}`, promptCacheKey, strings.Join(items, ",")))
}

func nianzsTestKiroResponsesImageCacheRequestBody(t *testing.T, label string, fill color.RGBA) []byte {
	prompt := strings.Repeat("cacheable responses visual prompt "+label+" ", 512)
	imageURL := nianzsTestKiroPNGDataURL(t, 384, 256, fill)
	return []byte(fmt.Sprintf(`{"model":"gpt-5","instructions":"Describe visual changes precisely.","prompt_cache_key":"workspace-image","previous_response_id":"resp-image","input":[{"role":"user","content":[{"type":"input_text","text":%q},{"type":"input_image","image_url":%q}]}]}`, prompt, imageURL))
}
