package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/anthropictokenizer"
	"github.com/tidwall/gjson"
)

func TestKiroCacheEmulationGroupDefaultsAndNonKiro(t *testing.T) {
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

func TestKiroCacheEmulationUsesSnapshotGroupWithoutRepo(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 34, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	first := svc.buildKiroCacheEmulationUsage(account, group, kiroCacheRequestBody("stable", false), "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 || first.CacheReadInputTokens != 0 || first.InputTokens != 0 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, kiroCacheRequestBody("stable", false), "claude-sonnet-4-6", 2000)
	if second == nil || second.CacheReadInputTokens != 2000 || second.CacheCreationInputTokens != 0 || second.InputTokens != 0 {
		t.Fatalf("unexpected second usage: %+v", second)
	}
}

func TestKiroCacheEmulationAllowsKiroAccountInNonKiroGroup(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 35, Platform: PlatformKiro}
	group := &Group{ID: 13, Platform: PlatformAnthropic, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: 1}
	body := kiroCacheRequestBody("mixed group", false)

	first := svc.buildKiroCacheEmulationUsage(account, group, body, "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 || first.CacheReadInputTokens != 0 || first.InputTokens != 0 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, body, "claude-sonnet-4-6", 2000)
	if second == nil || second.CacheReadInputTokens != 2000 || second.CacheCreationInputTokens != 0 || second.InputTokens != 0 {
		t.Fatalf("unexpected second usage: %+v", second)
	}
}

func TestKiroCacheEmulationRejectsNonKiroAccount(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 36, Platform: PlatformAnthropic}
	group := &Group{ID: 14, Platform: PlatformAnthropic, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: 1}

	if got := svc.buildKiroCacheEmulationUsage(account, group, kiroCacheRequestBody("non kiro", false), "claude-sonnet-4-6", 2000); got != nil {
		t.Fatalf("non-kiro account should skip cache emulation, got %+v", got)
	}
}

func TestKiroCacheEmulationMixedGroupIsolation(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	group := kiroCacheGroup(1)
	body := kiroCacheRequestBodyWithoutControl("mixed isolation")

	if got := svc.buildKiroCacheEmulationUsage(&Account{ID: 41, Platform: PlatformAnthropic}, group, body, "claude-sonnet-4-6", 3000); got != nil {
		t.Fatalf("non-kiro account in mixed group should not seed or consume kiro cache, got %+v", got)
	}

	kiroA := kiroCacheAccount(42, "refresh-a", "access-a")
	first := svc.buildKiroCacheEmulationUsage(kiroA, group, body, "claude-sonnet-4-6", 3000)
	if first == nil || first.CacheCreationInputTokens != 3000 || first.CacheReadInputTokens != 0 {
		t.Fatalf("unexpected first kiro usage: %+v", first)
	}

	kiroB := kiroCacheAccount(43, "refresh-b", "access-b")
	otherKiro := svc.buildKiroCacheEmulationUsage(kiroB, group, body, "claude-sonnet-4-6", 3000)
	if otherKiro == nil || otherKiro.CacheCreationInputTokens != 3000 || otherKiro.CacheReadInputTokens != 0 {
		t.Fatalf("different kiro account in same group must not share cache: %+v", otherKiro)
	}

	second := svc.buildKiroCacheEmulationUsage(kiroA, group, body, "claude-sonnet-4-6", 3000)
	if second == nil || second.CacheReadInputTokens != 3000 || second.CacheCreationInputTokens != 0 {
		t.Fatalf("same kiro account should reuse its own cache: %+v", second)
	}
}

func TestKiroCacheEmulationAccountEnabledDefaultsRatioToOne(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 37, Platform: PlatformKiro, Extra: map[string]any{"kiro_cache_emulation_enabled": true}}

	usage := svc.buildKiroCacheEmulationUsage(account, nil, kiroCacheRequestBody("account default ratio", false), "claude-sonnet-4-6", 2000)
	if usage == nil || usage.CacheCreationInputTokens != 2000 || usage.InputTokens != 0 {
		t.Fatalf("account enabled without ratio should default to full emulation, got %+v", usage)
	}
}

func TestKiroCacheEmulationUsagePersistsToUsageLog(t *testing.T) {
	svc := &GatewayService{}
	result := &ForwardResult{
		RequestID: "gateway_kiro_cache_tokens",
		Usage: ClaudeUsage{
			InputTokens:              100,
			OutputTokens:             10,
			CacheCreationInputTokens: 1200,
			CacheReadInputTokens:     800,
			CacheCreation5mTokens:    1200,
		},
		Model:    "claude-sonnet-4-6",
		Duration: time.Second,
	}
	log := svc.buildRecordUsageLog(
		nil,
		&recordUsageCoreInput{},
		result,
		&APIKey{ID: 901},
		&User{ID: 902},
		&Account{ID: 903, Platform: PlatformKiro},
		nil,
		"claude-sonnet-4-6",
		1,
		1,
		1,
		BillingTypeBalance,
		false,
		nil,
		&recordUsageOpts{},
	)

	if log.InputTokens != 100 || log.CacheCreationTokens != 1200 || log.CacheReadTokens != 800 || log.CacheCreation5mTokens != 1200 {
		t.Fatalf("cache usage was not persisted to usage log: %+v", log)
	}
}

func TestKiroCacheEmulationRatioScalesTokens(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 78, Platform: PlatformKiro}
	usage := svc.buildKiroCacheEmulationUsage(account, kiroCacheGroup(0.5), kiroCacheRequestBody("ratio", false), "claude-sonnet-4-6", 2000)
	if usage == nil || usage.CacheCreationInputTokens != 1000 || usage.InputTokens != 1000 {
		t.Fatalf("unexpected scaled usage: %+v", usage)
	}
	disabled := kiroCacheGroup(1)
	disabled.KiroCacheEmulationEnabled = false
	if got := svc.buildKiroCacheEmulationUsage(account, disabled, kiroCacheRequestBody("disabled", false), "claude-sonnet-4-6", 2000); got != nil {
		t.Fatalf("disabled group should skip cache emulation, got %+v", got)
	}
}

func TestKiroCacheEmulationAccountIsolation(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	group := kiroCacheGroup(1)
	body := kiroCacheRequestBody("account isolation", false)
	first := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(1, "refresh-a", "access-a"), group, body, "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	otherAccount := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(2, "refresh-b", "access-b"), group, body, "claude-sonnet-4-6", 2000)
	if otherAccount == nil || otherAccount.CacheCreationInputTokens != 2000 || otherAccount.CacheReadInputTokens != 0 {
		t.Fatalf("cache should be isolated by account: %+v", otherAccount)
	}
}

func TestKiroCacheEmulationStableCredentialIsolation(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	group := kiroCacheGroup(1)
	body := kiroCacheRequestBody("credential isolation", false)
	first := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(7, "refresh-same", "access-a"), group, body, "claude-sonnet-4-6", 2000)
	if first == nil || first.CacheCreationInputTokens != 2000 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	rotatedAccessToken := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(7, "refresh-same", "access-b"), group, body, "claude-sonnet-4-6", 2000)
	if rotatedAccessToken == nil || rotatedAccessToken.CacheReadInputTokens != 2000 || rotatedAccessToken.CacheCreationInputTokens != 0 {
		t.Fatalf("access token rotation should not break cache: %+v", rotatedAccessToken)
	}
	differentCredential := svc.buildKiroCacheEmulationUsage(kiroCacheAccount(7, "refresh-other", "access-c"), group, body, "claude-sonnet-4-6", 2000)
	if differentCredential == nil || differentCredential.CacheReadInputTokens != 0 || differentCredential.CacheCreationInputTokens != 2000 {
		t.Fatalf("different stable credential should not share cache: %+v", differentCredential)
	}
}

func TestKiroCacheEmulationContentChangeMisses(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 3, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	_ = svc.buildKiroCacheEmulationUsage(account, group, kiroCacheRequestBody("before", false), "claude-sonnet-4-6", 2000)
	changed := svc.buildKiroCacheEmulationUsage(account, group, kiroCacheRequestBody("after", false), "claude-sonnet-4-6", 2000)
	if changed == nil || changed.CacheCreationInputTokens != 2000 || changed.CacheReadInputTokens != 0 {
		t.Fatalf("changed content should miss: %+v", changed)
	}
}

func TestKiroCacheEmulationTTLExpiry(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 4, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	body := kiroCacheRequestBody("ttl", false)
	_ = svc.buildKiroCacheEmulationUsage(account, group, body, "claude-sonnet-4-6", 2000)
	globalKiroCacheTracker.mu.Lock()
	for accountID, entries := range globalKiroCacheTracker.entries {
		for fp, entry := range entries {
			entry.expiresAt = time.Now().Add(-time.Second)
			globalKiroCacheTracker.entries[accountID][fp] = entry
		}
	}
	globalKiroCacheTracker.mu.Unlock()
	afterExpiry := svc.buildKiroCacheEmulationUsage(account, group, body, "claude-sonnet-4-6", 2000)
	if afterExpiry == nil || afterExpiry.CacheCreationInputTokens != 2000 || afterExpiry.CacheReadInputTokens != 0 {
		t.Fatalf("expired cache should be recreated: %+v", afterExpiry)
	}
}

func TestKiroCacheEmulationOneHourBucket(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	usage := svc.buildKiroCacheEmulationUsage(&Account{ID: 5, Platform: PlatformKiro}, kiroCacheGroup(1), kiroCacheRequestBody("1h", true), "claude-sonnet-4-6", 2000)
	if usage == nil || usage.CacheCreationInputTokens != 2000 || usage.CacheCreation1hInputTokens != 2000 || usage.CacheCreation5mInputTokens != 0 {
		t.Fatalf("unexpected 1h bucket usage: %+v", usage)
	}
}

func TestKiroCacheEmulationPrefixPartialHit(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 6, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	firstBody := kiroCacheMultiMessageBody("cached prefix", "tail one")
	secondBody := kiroCacheMultiMessageBody("cached prefix", "tail two")
	first := svc.buildKiroCacheEmulationUsage(account, group, firstBody, "claude-sonnet-4-6", 6000)
	if first == nil || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("unexpected first usage: %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, secondBody, "claude-sonnet-4-6", 6000)
	if second == nil || second.CacheReadInputTokens <= 0 || second.CacheReadInputTokens >= first.CacheCreationInputTokens || second.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected partial prefix hit: %+v", second)
	}
}

func TestKiroCacheEmulationAutoBreakpointWithoutCacheControl(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 90, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	body := kiroCacheRequestBodyWithoutControl("auto")

	first := svc.buildKiroCacheEmulationUsage(account, group, body, "claude-sonnet-4-6", 3000)
	if first == nil || first.CacheCreationInputTokens <= 0 || first.CacheReadInputTokens != 0 {
		t.Fatalf("expected automatic cache creation, got %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, body, "claude-sonnet-4-6", 3000)
	if second == nil || second.CacheReadInputTokens != first.CacheCreationInputTokens || second.CacheCreationInputTokens != 0 {
		t.Fatalf("expected automatic cache read, first=%+v second=%+v", first, second)
	}
}

func TestKiroCacheEmulationScalesBreakpointsToEstimatedInputTokens(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 98, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"short stable prompt"}]}]}`)

	first := svc.buildKiroCacheEmulationUsage(account, group, body, "claude-sonnet-4-6", 120000)
	if first == nil || first.CacheCreationInputTokens != 120000 || first.CacheReadInputTokens != 0 || first.InputTokens != 0 {
		t.Fatalf("first request should scale cache creation to estimated input total, got %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, body, "claude-sonnet-4-6", 120000)
	if second == nil || second.CacheReadInputTokens != 120000 || second.CacheCreationInputTokens != 0 || second.InputTokens != 0 {
		t.Fatalf("second request should scale cache read to estimated input total, got %+v", second)
	}
}

func TestKiroCacheEmulationAutoBreakpointPrefersStablePrefix(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 91, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	firstBody := kiroCacheMultiMessageBodyWithoutControl("stable prefix", "question one")
	secondBody := kiroCacheMultiMessageBodyWithoutControl("stable prefix", "question two")

	first := svc.buildKiroCacheEmulationUsage(account, group, firstBody, "claude-sonnet-4-6", 6000)
	if first == nil || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected automatic prefix creation, got %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, secondBody, "claude-sonnet-4-6", 6000)
	if second == nil || second.CacheReadInputTokens <= 0 || second.CacheReadInputTokens >= 6000 {
		t.Fatalf("expected stable prefix cache read, got %+v", second)
	}
	if second.InputTokens <= 0 {
		t.Fatalf("changed tail should remain uncached input, got %+v", second)
	}
}

func TestKiroCacheEmulationMatchesOlderPrefixBeyondTenBreakpoints(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 92, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	firstBody := kiroCacheManyMessageBody("stable", 14, 0)
	secondBody := kiroCacheManyMessageBody("stable", 14, 24)

	first := svc.buildKiroCacheEmulationUsage(account, group, firstBody, "claude-sonnet-4-6", 20000)
	if first == nil || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected first request to create cache, got %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, secondBody, "claude-sonnet-4-6", 26000)
	if second == nil || second.CacheReadInputTokens <= 0 || second.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected older stable prefix to be found beyond short lookback, got %+v", second)
	}
}

func TestKiroCacheEmulationAutoBreakpointsMatchRollingHistory(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 94, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	firstBody := kiroCacheManyMessageBodyWithoutControl("rolling", 12, 0)
	secondBody := kiroCacheManyMessageBodyWithoutControl("rolling", 12, 4)

	first := svc.buildKiroCacheEmulationUsage(account, group, firstBody, "claude-sonnet-4-6", 20000)
	if first == nil || first.CacheCreationInputTokens <= 0 || first.CacheReadInputTokens != 0 {
		t.Fatalf("expected first request to create automatic rolling cache, got %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, secondBody, "claude-sonnet-4-6", 26000)
	if second == nil || second.CacheReadInputTokens <= 0 {
		t.Fatalf("expected rolling history to read an older automatic breakpoint, got %+v", second)
	}
	if second.CacheCreationInputTokens <= 0 {
		t.Fatalf("newly stable history segment should be written after older prefix hit, got %+v", second)
	}
	if second.InputTokens <= 0 {
		t.Fatalf("current tail should remain normal input tokens, got %+v", second)
	}
}

func TestKiroCacheEmulationRecoversFromPersistentStoreAfterTrackerReset(t *testing.T) {
	resetKiroCacheTracker()
	cache := newFakeKiroGatewayCache()
	svc := &GatewayService{cache: cache}
	ctx := SetClaudeCodeUserAgent(context.Background(), "claude-cli/2.1.195 (external, cli)")
	account := kiroCacheAccount(95, "persist-refresh", "persist-access")
	group := kiroCacheGroup(1)
	body := kiroCacheManyMessageBodyWithoutControl("persistent", 12, 0)

	first := svc.buildKiroCacheEmulationUsageForRequest(ctx, account, group, body, "claude-sonnet-4-6", 20000)
	if first == nil || first.CacheCreationInputTokens <= 0 || first.CacheReadInputTokens != 0 {
		t.Fatalf("expected first request to seed persistent store, got %+v", first)
	}

	resetKiroCacheTracker()

	second := svc.buildKiroCacheEmulationUsageForRequest(ctx, account, group, body, "claude-sonnet-4-6", 20000)
	if second == nil || second.CacheReadInputTokens <= 0 {
		t.Fatalf("expected persistent store to recover cache hit after reset, got %+v", second)
	}
}

func TestKiroCacheEmulationIgnoresVolatileToolIdentifiers(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 96, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	firstBody := kiroCacheToolContextBodyWithoutControl("tool-stable", "toolu_01ABC", "tail one")
	secondBody := kiroCacheToolContextBodyWithoutControl("tool-stable", "toolu_99XYZ", "tail two")

	first := svc.buildKiroCacheEmulationUsage(account, group, firstBody, "claude-sonnet-4-6", 16000)
	if first == nil || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected first tool-context request to create cache, got %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, secondBody, "claude-sonnet-4-6", 16000)
	if second == nil || second.CacheReadInputTokens <= 0 || second.InputTokens <= 0 {
		t.Fatalf("volatile tool ids should not destroy stable prefix hit, got %+v", second)
	}
}

func TestKiroCacheEmulationMatchesVeryLongHistoryBeyondDefaultLookback(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	account := &Account{ID: 97, Platform: PlatformKiro}
	group := kiroCacheGroup(1)
	firstBody := kiroCacheManyMessageBodyWithoutControl("very-long", 180, 0)
	secondBody := kiroCacheManyMessageBodyWithoutControl("very-long", 180, 24)

	first := svc.buildKiroCacheEmulationUsage(account, group, firstBody, "claude-sonnet-4-6", 260000)
	if first == nil || first.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected first long-history request to create cache, got %+v", first)
	}
	second := svc.buildKiroCacheEmulationUsage(account, group, secondBody, "claude-sonnet-4-6", 320000)
	if second == nil || second.CacheReadInputTokens <= 0 || second.CacheCreationInputTokens <= 0 {
		t.Fatalf("expected long-history stable prefix to match beyond default lookback, got %+v", second)
	}
}

func TestPrepareKiroCacheEmulationProfileBodyUsesStableMessageAnchors(t *testing.T) {
	svc := &GatewayService{}
	ua := "claude-cli/2.1.197 (external, claude-desktop-3p, agent-sdk/0.3.197)"
	ctx := SetClaudeCodeUserAgent(context.Background(), ua)
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[` +
		`{"role":"user","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"5m"}}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"middle","cache_control":{"type":"ephemeral","ttl":"5m"}}]},` +
		`{"role":"system","content":[{"type":"text","text":"tail reminder","cache_control":{"type":"ephemeral","ttl":"5m"}}]}` +
		`],"tools":[{"name":"LongCustomToolName","input_schema":{"type":"object"}}]}`)

	out := svc.prepareKiroCacheEmulationProfileBody(ctx, &Account{ID: 93, Platform: PlatformKiro, Type: AccountTypeOAuth}, body)
	if got := gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String(); got != "1h" {
		t.Fatalf("stable message cache ttl = %q, want 1h; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.content.0.cache_control.ttl").String(); got != "1h" {
		t.Fatalf("last non-system message cache ttl = %q, want 1h; body=%s", got, out)
	}
	if gjson.GetBytes(out, "messages.2.content.0.cache_control").Exists() {
		t.Fatalf("tail system reminder should not keep drifting message cache_control: %s", out)
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "LongCustomToolName" {
		t.Fatalf("kiro bridge preparation must not rename tools, got %q", got)
	}
}

func TestPrepareKiroCacheEmulationProfileBodyCoversPlainClaudeCLI(t *testing.T) {
	svc := &GatewayService{}
	ctx := SetClaudeCodeUserAgent(context.Background(), "claude-cli/2.1.195 (external, cli)")
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[` +
		`{"role":"user","content":[{"type":"text","text":"stable"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"middle"}]},` +
		`{"role":"system","content":[{"type":"text","text":"tail reminder"}]}` +
		`]}`)

	out := svc.prepareKiroCacheEmulationProfileBody(ctx, &Account{ID: 1621, Platform: PlatformKiro, Type: AccountTypeOAuth}, body)
	if got := gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String(); got != "1h" {
		t.Fatalf("plain CLI stable message cache ttl = %q, want 1h; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.content.0.cache_control.ttl").String(); got != "1h" {
		t.Fatalf("plain CLI trailing message cache ttl = %q, want 1h; body=%s", got, out)
	}
	if gjson.GetBytes(out, "messages.2.content.0.cache_control").Exists() {
		t.Fatalf("plain CLI profile should avoid drifting tail system reminder: %s", out)
	}
	if strings.Contains(string(body), "cache_control") {
		t.Fatalf("profile preparation must not mutate caller-owned upstream body: %s", body)
	}
}

func TestBuildKiroCacheEmulationUsageForRequestPlainCLIUsesProfileOnly(t *testing.T) {
	resetKiroCacheTracker()
	svc := &GatewayService{}
	ctx := SetClaudeCodeUserAgent(context.Background(), "claude-cli/2.1.195 (external, cli)")
	account := &Account{ID: 1621, Platform: PlatformKiro, Type: AccountTypeOAuth}
	group := kiroCacheGroup(1)
	body := kiroCacheManyMessageBodyWithoutControl("plain-cli-profile", 12, 0)

	first := svc.buildKiroCacheEmulationUsageForRequest(ctx, account, group, body, "claude-sonnet-4-6", 20000)
	if first == nil || first.CacheCreationInputTokens <= 0 || first.CacheReadInputTokens != 0 {
		t.Fatalf("plain CLI first request should write stable profile cache, got %+v", first)
	}
	if strings.Contains(string(body), "cache_control") {
		t.Fatalf("cache emulation profile must not be written back into upstream body: %s", body)
	}
	second := svc.buildKiroCacheEmulationUsageForRequest(ctx, account, group, body, "claude-sonnet-4-6", 20000)
	if second == nil || second.CacheReadInputTokens <= 0 {
		t.Fatalf("plain CLI second request should read stable profile cache, got %+v", second)
	}
}

func TestPrepareKiroCacheEmulationProfileBodySkipsNonKiroAccounts(t *testing.T) {
	svc := &GatewayService{}
	ctx := SetClaudeCodeUserAgent(context.Background(), "claude-cli/2.1.195 (external, cli)")
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)

	out := svc.prepareKiroCacheEmulationProfileBody(ctx, &Account{ID: 93, Platform: PlatformAnthropic, Type: AccountTypeOAuth}, body)
	if string(out) != string(body) {
		t.Fatalf("non-kiro account should not receive kiro cache profile body: %s", out)
	}
}

func TestKiroInputTokenEstimateIgnoresClientMetadata(t *testing.T) {
	bodyWithoutMetadata := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello world"}]}`)
	bodyWithMetadata := []byte(`{"model":"claude-sonnet-4-6","metadata":{"input_tokens":999999},"messages":[{"role":"user","content":"hello world"}]}`)
	withoutMetadata := estimateKiroInputTokens(bodyWithoutMetadata)
	withMetadata := estimateKiroInputTokens(bodyWithMetadata)
	if withMetadata == 999999 {
		t.Fatal("client metadata.input_tokens must not be trusted")
	}
	if withMetadata <= 0 || withoutMetadata <= 0 || withMetadata > withoutMetadata*2 {
		t.Fatalf("unexpected estimates without=%d with=%d", withoutMetadata, withMetadata)
	}
}

func TestKiroTokenCountersMatchReferenceRules(t *testing.T) {
	if got := anthropictokenizer.CountTokens("abc def"); got != 1 {
		t.Fatalf("english tokens = %d, want 1", got)
	}
	if got := anthropictokenizer.CountTokens("你好世界"); got != 1 {
		t.Fatalf("cjk tokens = %d, want 1", got)
	}
	if kiroTokensPerTool != 150 {
		t.Fatalf("tool tokens = %d, want 150", kiroTokensPerTool)
	}
	if got := countKiroMessageContentTokens(map[string]any{"thinking": "abc def"}); got != 1 {
		t.Fatalf("thinking tokens = %d, want 1", got)
	}
	if got := countKiroMessageContentTokens(map[string]any{"input": map[string]any{"path": "/tmp/a.txt"}}); got <= 0 {
		t.Fatalf("tool input tokens should be positive, got %d", got)
	}
	if got := countKiroMessageContentTokens(map[string]any{"content": []any{map[string]any{"text": "abc"}, map[string]any{"text": "你好"}}}); got != 2 {
		t.Fatalf("tool result content tokens = %d, want 2", got)
	}
}

func resetKiroCacheTracker() {
	globalKiroCacheTracker = &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}
}

type fakeKiroGatewayCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func newFakeKiroGatewayCache() *fakeKiroGatewayCache {
	return &fakeKiroGatewayCache{entries: make(map[string]time.Time)}
}

func (c *fakeKiroGatewayCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, nil
}

func (c *fakeKiroGatewayCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (c *fakeKiroGatewayCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *fakeKiroGatewayCache) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (c *fakeKiroGatewayCache) GetKiroCacheFingerprints(_ context.Context, stableKey string, fingerprints []string) (map[string]bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	out := make(map[string]bool, len(fingerprints))
	for _, fingerprint := range fingerprints {
		key := stableKey + "|" + fingerprint
		if expiresAt, ok := c.entries[key]; ok && expiresAt.After(now) {
			out[fingerprint] = true
			continue
		}
		delete(c.entries, key)
	}
	return out, nil
}

func (c *fakeKiroGatewayCache) UpsertKiroCacheFingerprints(_ context.Context, stableKey string, fingerprintTTLs map[string]time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for fingerprint, ttl := range fingerprintTTLs {
		if ttl <= 0 {
			continue
		}
		key := stableKey + "|" + fingerprint
		expiresAt := now.Add(ttl)
		if existing, ok := c.entries[key]; ok && existing.After(expiresAt) {
			continue
		}
		c.entries[key] = expiresAt
	}
	return nil
}

func kiroCacheGroup(ratio float64) *Group {
	return &Group{ID: 12, Platform: PlatformKiro, KiroCacheEmulationEnabled: true, KiroCacheEmulationRatio: ratio}
}

func kiroCacheAccount(id int64, refreshToken string, accessToken string) *Account {
	return &Account{ID: id, Platform: PlatformKiro, Type: AccountTypeOAuth, Credentials: map[string]any{
		"client_id":     "client-id",
		"refresh_token": refreshToken,
		"access_token":  accessToken,
	}}
}

func kiroCacheRequestBody(label string, oneHour bool) []byte {
	ttl := ""
	if oneHour {
		ttl = `,"ttl":"1h"`
	}
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q,"cache_control":{"type":"ephemeral"%s}}]}]}`, strings.Repeat("cacheable prompt chunk "+label+" ", 512), ttl))
}

func kiroCacheRequestBodyWithoutControl(label string) []byte {
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q}]}]}`, strings.Repeat("cacheable prompt chunk "+label+" ", 512)))
}

func kiroCacheMultiMessageBody(prefixLabel, tailLabel string) []byte {
	prefix := strings.Repeat("cacheable prompt chunk "+prefixLabel+" ", 512)
	tail := strings.Repeat("conversation growth chunk "+tailLabel+" ", 160)
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q,"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":%q}]}]}`, prefix, tail))
}

func kiroCacheMultiMessageBodyWithoutControl(prefixLabel, tailLabel string) []byte {
	prefix := strings.Repeat("cacheable prompt chunk "+prefixLabel+" ", 512)
	tail := strings.Repeat("conversation growth chunk "+tailLabel+" ", 160)
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":%q}]},{"role":"user","content":[{"type":"text","text":%q}]}]}`, prefix, tail))
}

func kiroCacheManyMessageBody(prefixLabel string, stableTailCount, newTailCount int) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"claude-sonnet-4-6","messages":[`)
	prefix := strings.Repeat("cacheable prompt chunk "+prefixLabel+" ", 512)
	b.WriteString(fmt.Sprintf(`{"role":"user","content":[{"type":"text","text":%q,"cache_control":{"type":"ephemeral"}}]}`, prefix))
	for i := 0; i < stableTailCount; i++ {
		b.WriteString(",")
		text := strings.Repeat(fmt.Sprintf("stable tail %02d ", i), 64)
		b.WriteString(fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}]}`, text))
	}
	for i := 0; i < newTailCount; i++ {
		b.WriteString(",")
		text := strings.Repeat(fmt.Sprintf("new tail %02d ", i), 64)
		b.WriteString(fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}]}`, text))
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func kiroCacheManyMessageBodyWithoutControl(prefixLabel string, stableTailCount, newTailCount int) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"claude-sonnet-4-6","messages":[`)
	prefix := strings.Repeat("cacheable prompt chunk "+prefixLabel+" ", 512)
	b.WriteString(fmt.Sprintf(`{"role":"user","content":[{"type":"text","text":%q}]}`, prefix))
	for i := 0; i < stableTailCount; i++ {
		b.WriteString(",")
		text := strings.Repeat(fmt.Sprintf("stable tail %02d ", i), 64)
		b.WriteString(fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}]}`, text))
	}
	for i := 0; i < newTailCount; i++ {
		b.WriteString(",")
		text := strings.Repeat(fmt.Sprintf("new tail %02d ", i), 64)
		b.WriteString(fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}]}`, text))
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func kiroCacheToolContextBodyWithoutControl(prefixLabel, toolID, tailLabel string) []byte {
	prefix := strings.Repeat("cacheable prompt chunk "+prefixLabel+" ", 512)
	toolResult := strings.Repeat("tool result chunk "+prefixLabel+" ", 128)
	tail := strings.Repeat("conversation growth chunk "+tailLabel+" ", 128)
	return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[`+
		`{"role":"user","content":[{"type":"text","text":%q}]},`+
		`{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"read_file","input":{"path":"/tmp/a.txt"}}]},`+
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":[{"type":"text","text":%q}]}]},`+
		`{"role":"assistant","content":[{"type":"text","text":%q}]}`+
		`]}`, prefix, toolID, toolID, toolResult, tail))
}
