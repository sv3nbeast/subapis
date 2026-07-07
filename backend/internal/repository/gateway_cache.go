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

var kiroCacheFingerprintUpsertScript = redis.NewScript(`
local ttl = tonumber(ARGV[1])
if ttl == nil or ttl <= 0 then
  return 0
end
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('PSETEX', KEYS[1], ttl, '1')
  return 1
end
local current = redis.call('PTTL', KEYS[1])
if current < 0 or current < ttl then
  redis.call('PEXPIRE', KEYS[1], ttl)
end
return 1
`)

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
	for fingerprint, ttl := range fingerprintTTLs {
		if fingerprint == "" || ttl <= 0 {
			continue
		}
		_ = kiroCacheFingerprintUpsertScript.Run(ctx, pipe, []string{buildKiroCacheFingerprintKey(stableKey, fingerprint)}, ttl.Milliseconds())
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return err
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
