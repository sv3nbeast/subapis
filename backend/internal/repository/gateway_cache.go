package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const stickySessionPrefix = "sticky_session:"
const kiroCacheFingerprintPrefix = "kiro_cache_emulation:"
const kiroCacheShadowStreamKey = "kiro_cache_shadow_samples:v1"
const kiroCacheShadowMetricsPrefix = "kiro_cache_shadow_metrics:v1:"
const kiroCacheShadowRetention = 7 * 24 * time.Hour
const kiroCacheShadowMaxSamples = 200000

type gatewayCache struct {
	rdb *redis.Client
}

var _ service.KiroCacheShadowStore = (*gatewayCache)(nil)

func NewGatewayCache(rdb *redis.Client) service.GatewayCache {
	return &gatewayCache{rdb: rdb}
}

// buildSessionKey 构建 session key，包含 groupID 实现分组隔离
// 格式: sticky_session:{groupID}:{sessionHash}
func buildSessionKey(groupID int64, sessionHash string) string {
	return fmt.Sprintf("%s%d:%s", stickySessionPrefix, groupID, sessionHash)
}

func (c *gatewayCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Get(ctx, key).Int64()
}

func (c *gatewayCache) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Set(ctx, key, accountID, ttl).Err()
}

func (c *gatewayCache) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Expire(ctx, key, ttl).Err()
}

// DeleteSessionAccountID 删除粘性会话与账号的绑定关系。
// 当检测到绑定的账号不可用（如状态错误、禁用、不可调度等）时调用，
// 以便下次请求能够重新选择可用账号。
//
// DeleteSessionAccountID removes the sticky session binding for the given session.
// Called when the bound account becomes unavailable (e.g., error status, disabled,
// or unschedulable), allowing subsequent requests to select a new available account.
func (c *gatewayCache) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Del(ctx, key).Err()
}

func buildKiroCacheFingerprintKey(stableKey string, fingerprint string) string {
	stableDigest := sha256.Sum256([]byte(stableKey))
	return fmt.Sprintf("%s%s:%s", kiroCacheFingerprintPrefix, hex.EncodeToString(stableDigest[:]), fingerprint)
}

func (c *gatewayCache) GetKiroCacheFingerprints(ctx context.Context, stableKey string, fingerprints []string) (map[string]bool, error) {
	out := make(map[string]bool, len(fingerprints))
	if c == nil || c.rdb == nil || stableKey == "" || len(fingerprints) == 0 {
		return out, nil
	}
	pipe := c.rdb.Pipeline()
	cmds := make(map[string]*redis.IntCmd, len(fingerprints))
	for _, fingerprint := range fingerprints {
		if fingerprint == "" {
			continue
		}
		cmds[fingerprint] = pipe.Exists(ctx, buildKiroCacheFingerprintKey(stableKey, fingerprint))
	}
	if len(cmds) == 0 {
		return out, nil
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}
	for fingerprint, cmd := range cmds {
		n, cmdErr := cmd.Result()
		if cmdErr != nil && cmdErr != redis.Nil {
			return nil, cmdErr
		}
		out[fingerprint] = n > 0
	}
	return out, nil
}

func (c *gatewayCache) UpsertKiroCacheFingerprints(ctx context.Context, stableKey string, fingerprintTTLs map[string]time.Duration) error {
	if c == nil || c.rdb == nil || stableKey == "" || len(fingerprintTTLs) == 0 {
		return nil
	}
	pipe := c.rdb.Pipeline()
	cmds := make([]redis.Cmder, 0, len(fingerprintTTLs)*3)
	for fingerprint, ttl := range fingerprintTTLs {
		if fingerprint == "" || ttl <= 0 {
			continue
		}
		ttlMillis := ttl.Milliseconds()
		if ttlMillis <= 0 {
			continue
		}
		key := buildKiroCacheFingerprintKey(stableKey, fingerprint)
		// 不再使用 Lua script：go-redis pipeline 中 EVALSHA 遇到 NOSCRIPT 不会像
		// 普通 client 调用一样可靠回退，生产曾出现大量 NOSCRIPT，导致 Kiro
		// cache fingerprint 持久化未真正落 Redis。这里用 Redis 原生命令实现同等语义：
		// 1) 新 key：SET ... NX PX ttl；
		// 2) 已存在且新 TTL 更长：PEXPIRE ... GT；
		// 3) 已存在但意外无 TTL：PEXPIRE ... NX。
		cmds = append(cmds, pipe.SetNX(ctx, key, "1", ttl))
		cmds = append(cmds, pipe.Do(ctx, "PEXPIRE", key, ttlMillis, "GT"))
		cmds = append(cmds, pipe.Do(ctx, "PEXPIRE", key, ttlMillis, "NX"))
	}
	if len(cmds) == 0 {
		return nil
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return err
	}
	for _, cmd := range cmds {
		if cmdErr := cmd.Err(); cmdErr != nil && cmdErr != redis.Nil {
			return cmdErr
		}
	}
	return nil
}

func (c *gatewayCache) RecordKiroCacheShadowSample(ctx context.Context, sample *service.KiroCacheShadowSample) error {
	if c == nil || c.rdb == nil || sample == nil {
		return nil
	}
	raw, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	createdAt := sample.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	metricsKey := fmt.Sprintf("%s%s:group_%d:%s:%s:%s",
		kiroCacheShadowMetricsPrefix,
		createdAt.Format("20060102"),
		sample.GroupID,
		kiroCacheShadowMetricKeyPart(sample.Model),
		kiroCacheShadowMetricKeyPart(sample.UAForm),
		kiroCacheShadowMetricKeyPart(sample.ContextBucket),
	)

	pipe := c.rdb.Pipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: kiroCacheShadowStreamKey,
		MaxLen: kiroCacheShadowMaxSamples,
		Approx: true,
		Values: map[string]any{"sample": string(raw)},
	})
	pipe.Expire(ctx, kiroCacheShadowStreamKey, kiroCacheShadowRetention)
	pipe.HIncrBy(ctx, metricsKey, "requests", 1)
	pipe.HIncrBy(ctx, metricsKey, "context_tokens", int64(sample.Actual.Usage.InputTokens+sample.Actual.Usage.CacheReadInputTokens+sample.Actual.Usage.CacheCreationInputTokens))
	if sample.CurrentStateWarm {
		pipe.HIncrBy(ctx, metricsKey, "current_warm_requests", 1)
		pipe.HIncrBy(ctx, metricsKey, "current_warm_context_tokens", int64(sample.Actual.Usage.InputTokens+sample.Actual.Usage.CacheReadInputTokens+sample.Actual.Usage.CacheCreationInputTokens))
		kiroCacheShadowAggregateCandidate(ctx, pipe, metricsKey, "current_warm_actual", sample.Actual, sample.Actual)
		kiroCacheShadowAggregateCandidate(ctx, pipe, metricsKey, "current_warm_current_ratio_0_9", sample.CurrentRatio09, sample.Actual)
		kiroCacheShadowAggregateCandidate(ctx, pipe, metricsKey, "current_warm_current_ratio_1", sample.CurrentRatio1, sample.Actual)
	}
	if sample.ProtocolStateWarm {
		pipe.HIncrBy(ctx, metricsKey, "protocol_warm_requests", 1)
		pipe.HIncrBy(ctx, metricsKey, "protocol_warm_context_tokens", int64(sample.Actual.Usage.InputTokens+sample.Actual.Usage.CacheReadInputTokens+sample.Actual.Usage.CacheCreationInputTokens))
		kiroCacheShadowAggregateCandidate(ctx, pipe, metricsKey, "protocol_warm_actual", sample.Actual, sample.Actual)
		kiroCacheShadowAggregateCandidate(ctx, pipe, metricsKey, "protocol_warm_protocol_v2", sample.ProtocolV2, sample.Actual)
	}
	kiroCacheShadowAggregateCandidate(ctx, pipe, metricsKey, "actual", sample.Actual, sample.Actual)
	kiroCacheShadowAggregateCandidate(ctx, pipe, metricsKey, "current_ratio_0_9", sample.CurrentRatio09, sample.Actual)
	kiroCacheShadowAggregateCandidate(ctx, pipe, metricsKey, "current_ratio_1", sample.CurrentRatio1, sample.Actual)
	kiroCacheShadowAggregateCandidate(ctx, pipe, metricsKey, "protocol_v2", sample.ProtocolV2, sample.Actual)
	pipe.Expire(ctx, metricsKey, kiroCacheShadowRetention)
	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return err
	}
	return nil
}

func kiroCacheShadowAggregateCandidate(ctx context.Context, pipe redis.Pipeliner, key, prefix string, candidate, actual service.KiroCacheShadowCandidate) {
	usage := candidate.Usage
	actualUsage := actual.Usage
	pipe.HIncrBy(ctx, key, prefix+"_input_tokens", int64(usage.InputTokens))
	pipe.HIncrBy(ctx, key, prefix+"_cache_read_tokens", int64(usage.CacheReadInputTokens))
	pipe.HIncrBy(ctx, key, prefix+"_cache_creation_tokens", int64(usage.CacheCreationInputTokens))
	pipe.HIncrBy(ctx, key, prefix+"_cache_creation_5m_tokens", int64(usage.CacheCreation5mInputTokens))
	pipe.HIncrBy(ctx, key, prefix+"_cache_creation_1h_tokens", int64(usage.CacheCreation1hInputTokens))
	pipe.HIncrByFloat(ctx, key, prefix+"_input_side_cost", candidate.InputSideCost)
	if candidate.CacheHit {
		pipe.HIncrBy(ctx, key, prefix+"_cache_hits", 1)
	}
	if prefix == "actual" || strings.HasSuffix(prefix, "_actual") {
		return
	}
	pipe.HIncrBy(ctx, key, prefix+"_abs_input_error", int64(absInt(usage.InputTokens-actualUsage.InputTokens)))
	pipe.HIncrBy(ctx, key, prefix+"_abs_cache_read_error", int64(absInt(usage.CacheReadInputTokens-actualUsage.CacheReadInputTokens)))
	pipe.HIncrBy(ctx, key, prefix+"_abs_cache_creation_error", int64(absInt(usage.CacheCreationInputTokens-actualUsage.CacheCreationInputTokens)))
	pipe.HIncrByFloat(ctx, key, prefix+"_abs_cost_error", math.Abs(candidate.InputSideCost-actual.InputSideCost))
}

func kiroCacheShadowMetricKeyPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(":", "_", " ", "_", "/", "_", "\\", "_")
	return replacer.Replace(value)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// Compile-time assertion: gatewayCache must implement CyberSessionBlockStore.
var _ service.CyberSessionBlockStore = (*gatewayCache)(nil)

const cyberSessionBlockPrefix = "cyber_session_block:"

// SetCyberSessionBlocked 把被 cyber_policy 命中的会话写入屏蔽表（TTL 自动过期）。
// 存储值 "1" 作为存在标记（IsCyberSessionBlocked 只检查 key 是否存在，不读值）。
func (c *gatewayCache) SetCyberSessionBlocked(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Set(ctx, cyberSessionBlockPrefix+key, "1", ttl).Err()
}

// IsCyberSessionBlocked 查询会话是否在屏蔽表中。
func (c *gatewayCache) IsCyberSessionBlocked(ctx context.Context, key string) (bool, error) {
	n, err := c.rdb.Exists(ctx, cyberSessionBlockPrefix+key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
