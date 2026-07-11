package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type kiroCacheShadowStoreStub struct {
	samples chan *KiroCacheShadowSample
}

func newKiroCacheShadowStoreStub() *kiroCacheShadowStoreStub {
	return &kiroCacheShadowStoreStub{samples: make(chan *KiroCacheShadowSample, 1)}
}

func (s *kiroCacheShadowStoreStub) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, nil
}

func (s *kiroCacheShadowStoreStub) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (s *kiroCacheShadowStoreStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *kiroCacheShadowStoreStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (s *kiroCacheShadowStoreStub) RecordKiroCacheShadowSample(_ context.Context, sample *KiroCacheShadowSample) error {
	s.samples <- sample
	return nil
}

func resetKiroCacheShadowTrackers() {
	globalKiroCacheShadowCurrentTracker = &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}
	globalKiroCacheShadowProtocolTracker = &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}
}

func TestKiroCacheShadowRecordsGroundTruthAndThreeCandidates(t *testing.T) {
	t.Setenv("SUB2API_KIRO_CACHE_SHADOW_ENABLED", "true")
	resetKiroCacheShadowTrackers()
	store := newKiroCacheShadowStoreStub()
	svc := &GatewayService{cache: store}
	groupID := int64(19)
	body := kiroShadowBody(4)
	ctx := SetClaudeCodeUserAgent(context.Background(), "claude-cli/2.1.170 (external, claude-desktop-3p, agent-sdk/0.3.170)")
	eval := svc.beginKiroCacheShadow(ctx, &Account{ID: 88, Platform: PlatformAnthropic, Type: AccountTypeOAuth}, &ParsedRequest{GroupID: &groupID}, body, "claude-opus-4-8")
	require.NotNil(t, eval)

	svc.finishKiroCacheShadow(eval, "req-shadow", ClaudeUsage{
		InputTokens:              10,
		CacheReadInputTokens:     900,
		CacheCreationInputTokens: 90,
		CacheCreation5mTokens:    90,
	})

	select {
	case sample := <-store.samples:
		require.Equal(t, "req-shadow", sample.RequestID)
		require.Equal(t, int64(88), sample.AccountID)
		require.Equal(t, int64(19), sample.GroupID)
		require.Equal(t, 1000, sample.Actual.Usage.totalInputTokens())
		require.Equal(t, 1000, sample.CurrentRatio09.Usage.totalInputTokens())
		require.Equal(t, 1000, sample.CurrentRatio1.Usage.totalInputTokens())
		require.Equal(t, 1000, sample.ProtocolV2.Usage.totalInputTokens())
		require.Greater(t, sample.CurrentRatio09.Usage.InputTokens, sample.CurrentRatio1.Usage.InputTokens)
		require.Greater(t, sample.ExplicitBreakpoints, 0)
	case <-time.After(time.Second):
		t.Fatal("shadow sample was not recorded")
	}
}

func TestKiroCacheShadowSkipsNonAnthropicAccounts(t *testing.T) {
	store := newKiroCacheShadowStoreStub()
	svc := &GatewayService{cache: store}
	eval := svc.beginKiroCacheShadow(context.Background(), &Account{ID: 1, Platform: PlatformKiro, Type: AccountTypeOAuth}, nil, kiroShadowBody(1), "claude-opus-4-8")
	require.Nil(t, eval)
}

func TestKiroCacheProtocolLookbackMatchesWithin20BlocksAndMissesBeyond(t *testing.T) {
	tracker := &kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}
	cacheKey := uint64(7)
	seed, ok := buildKiroCacheProfile(kiroShadowBody(1), "claude-sonnet-4-6", 0)
	require.True(t, ok)
	updateKiroShadowProtocolTracker(tracker, cacheKey, seed)

	within, ok := buildKiroCacheProfile(kiroShadowBody(20), "claude-sonnet-4-6", 0)
	require.True(t, ok)
	usage, match, explicit := buildKiroShadowProtocolUsage(tracker, cacheKey, within)
	require.Equal(t, 1, explicit)
	require.NotNil(t, match)
	require.Equal(t, 19, match.blockDepth)
	require.Greater(t, usage.CacheReadInputTokens, 0)

	beyond, ok := buildKiroCacheProfile(kiroShadowBody(22), "claude-sonnet-4-6", 0)
	require.True(t, ok)
	usage, match, _ = buildKiroShadowProtocolUsage(tracker, cacheKey, beyond)
	require.Nil(t, match)
	require.Zero(t, usage.CacheReadInputTokens)
	require.Greater(t, usage.CacheCreationInputTokens, 0)
}

func TestKiroCacheProtocolCreationBreakdownUsesBreakpointIntervals(t *testing.T) {
	body := kiroShadowMixedTTLBody()
	profile, ok := buildKiroCacheProfile(body, "claude-sonnet-4-6", 0)
	require.True(t, ok)
	usage, match, explicit := buildKiroShadowProtocolUsage(&kiroCacheTracker{entries: make(map[uint64]map[[32]byte]kiroCacheEntry)}, 9, profile)
	require.Nil(t, match)
	require.Equal(t, 2, explicit)
	require.Greater(t, usage.CacheCreation1hInputTokens, 0)
	require.Greater(t, usage.CacheCreation5mInputTokens, 0)
	require.Equal(t, usage.CacheCreationInputTokens, usage.CacheCreation1hInputTokens+usage.CacheCreation5mInputTokens)
}

func BenchmarkKiroCacheShadowBeginLargeContext(b *testing.B) {
	store := newKiroCacheShadowStoreStub()
	svc := &GatewayService{cache: store}
	groupID := int64(19)
	body, _ := json.Marshal(map[string]any{
		"model": "claude-opus-4-8",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type":          "text",
				"text":          strings.Repeat("large cache shadow context ", 60_000),
				"cache_control": map[string]any{"type": "ephemeral", "ttl": "5m"},
			}},
		}},
	})
	account := &Account{ID: 99, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	parsed := &ParsedRequest{GroupID: &groupID}
	ctx := SetClaudeCodeUserAgent(context.Background(), "claude-cli/2.1.170 (external, claude-desktop-3p, agent-sdk/0.3.170)")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if eval := svc.beginKiroCacheShadow(ctx, account, parsed, body, "claude-opus-4-8"); eval == nil {
			b.Fatal("shadow evaluation unexpectedly skipped")
		}
	}
}

func kiroShadowBody(blockCount int) []byte {
	if blockCount < 1 {
		blockCount = 1
	}
	content := make([]map[string]any, 0, blockCount)
	content = append(content, map[string]any{"type": "text", "text": strings.Repeat("stable-prefix ", 8000)})
	for i := 1; i < blockCount; i++ {
		content = append(content, map[string]any{"type": "text", "text": fmt.Sprintf("tail-%02d", i)})
	}
	content[len(content)-1]["cache_control"] = map[string]any{"type": "ephemeral", "ttl": "5m"}
	body, _ := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6",
		"messages": []any{map[string]any{
			"role":    "user",
			"content": content,
		}},
	})
	return body
}

func kiroShadowMixedTTLBody() []byte {
	long := strings.Repeat("cacheable-segment ", 4000)
	body, _ := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": long, "cache_control": map[string]any{"type": "ephemeral", "ttl": "1h"}},
				map[string]any{"type": "text", "text": long},
				map[string]any{"type": "text", "text": long, "cache_control": map[string]any{"type": "ephemeral", "ttl": "5m"}},
			},
		}},
	})
	return body
}
