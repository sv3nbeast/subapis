package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const stickySessionPrefix = "sticky_session:"
const kiroCacheFingerprintPrefix = "kiro_cache_emulation:"

type gatewayCache struct {
	rdb *redis.Client
}

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
