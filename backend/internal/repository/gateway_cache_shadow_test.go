package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheRecordsKiroCacheShadowStreamAndMetrics(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &gatewayCache{rdb: rdb}
	createdAt := time.Date(2026, 7, 11, 2, 30, 0, 0, time.UTC)
	sample := &service.KiroCacheShadowSample{
		SchemaVersion:     "v1",
		CreatedAt:         createdAt,
		RequestID:         "req-shadow",
		GroupID:           19,
		Model:             "claude-opus-4-8",
		UAForm:            "agent-sdk",
		ContextBucket:     "180k_220k",
		CurrentStateWarm:  true,
		ProtocolStateWarm: true,
		Actual: service.KiroCacheShadowCandidate{
			Usage:         service.KiroCacheShadowUsage{InputTokens: 10, CacheReadInputTokens: 90},
			InputSideCost: 0.1,
			CacheHit:      true,
		},
		CurrentRatio09: service.KiroCacheShadowCandidate{
			Usage:         service.KiroCacheShadowUsage{InputTokens: 20, CacheReadInputTokens: 80},
			InputSideCost: 0.2,
			CacheHit:      true,
		},
		CurrentRatio1: service.KiroCacheShadowCandidate{
			Usage:         service.KiroCacheShadowUsage{CacheReadInputTokens: 100},
			InputSideCost: 0.05,
			CacheHit:      true,
		},
		ProtocolV2: service.KiroCacheShadowCandidate{
			Usage:         service.KiroCacheShadowUsage{InputTokens: 10, CacheReadInputTokens: 90},
			InputSideCost: 0.1,
			CacheHit:      true,
		},
	}

	require.NoError(t, cache.RecordKiroCacheShadowSample(context.Background(), sample))
	require.Equal(t, int64(1), rdb.XLen(context.Background(), kiroCacheShadowStreamKey).Val())
	metricsKey := "kiro_cache_shadow_metrics:v1:20260711:group_19:claude-opus-4-8:agent-sdk:180k_220k"
	metrics := rdb.HGetAll(context.Background(), metricsKey).Val()
	require.Equal(t, "1", metrics["requests"])
	require.Equal(t, "100", metrics["context_tokens"])
	require.Equal(t, "10", metrics["current_ratio_0_9_abs_input_error"])
	require.Equal(t, "0", metrics["protocol_v2_abs_input_error"])
	require.Equal(t, "1", metrics["current_warm_requests"])
	require.Equal(t, "100", metrics["protocol_warm_context_tokens"])
	require.Equal(t, "10", metrics["current_warm_current_ratio_0_9_abs_input_error"])
	require.NotEmpty(t, metrics["actual_input_side_cost"])
	require.Greater(t, rdb.TTL(context.Background(), kiroCacheShadowStreamKey).Val(), 0*time.Second)
}
