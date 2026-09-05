package repository

import (
	"context"
	"github.com/redis/go-redis/v9"
	"strconv"
	"testing"
	"time"
)

// Exact pre-index admission script from bdfc87822. This isolates the cost of
// bookkeeping; it is not a production/model TTFT benchmark.
var benchmarkPreIndexAcquireScript = redis.NewScript(`
		-- Redis 3.2-4.x compat: opt into effects replication so redis.call('TIME')
		-- replicates correctly. No-op on Redis 5.0+ (effects replication is default).
		redis.replicate_commands()
		local key = KEYS[1]
		local liveKey = KEYS[2]
		local maxConcurrency = tonumber(ARGV[1])
		local ttl = tonumber(ARGV[2])
		local requestID = ARGV[3]

		-- 使用 Redis 服务器时间，确保多实例时钟一致
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		local expireBefore = now - ttl

		-- 清理过期槽位
		redis.call('ZREMRANGEBYSCORE', tostring(key), '-inf', tostring(expireBefore))
		if liveKey then
			redis.call('ZREMRANGEBYSCORE', tostring(liveKey), '-inf', tostring(now - 60))
		end

		-- 检查是否已存在（支持重试场景刷新时间戳）
		local exists = redis.call('ZSCORE', tostring(key), tostring(requestID))
		if exists ~= false then
			redis.call('ZADD', tostring(key), tostring(now), tostring(requestID))
			redis.call('EXPIRE', tostring(key), tostring(ttl))
			return 1
		end

		-- 检查是否达到并发上限
		local count = redis.call('ZCARD', tostring(key))
		if liveKey then count = count + redis.call('ZCARD', tostring(liveKey)) end
		if count < maxConcurrency then
			redis.call('ZADD', tostring(key), tostring(now), tostring(requestID))
			redis.call('EXPIRE', tostring(key), tostring(ttl))
			return 1
		end

		return 0
`)

func BenchmarkConcurrencyIndexAdmission(b *testing.B) {
	rdb := newBenchmarkRedisClient(b)
	defer rdb.Close()
	ctx := context.Background()
	cache := NewConcurrencyCache(rdb, 15, 30).(*concurrencyCache)
	for _, indexed := range []bool{false, true} {
		name := "pre-index"
		if indexed {
			name = "indexed"
		}
		b.Run(name, func(b *testing.B) {
			accountID := time.Now().UnixNano()
			key := accountSlotKey(accountID)
			live := liveAccountSlotKey(accountID)
			b.Cleanup(func() {
				rdb.Del(ctx, key, live)
				rdb.ZRem(ctx, accountActiveIndexKey, strconv.FormatInt(accountID, 10))
			})
			if _, err := benchmarkPreIndexAcquireScript.Load(ctx, rdb).Result(); err != nil {
				b.Fatal(err)
			}
			if _, err := acquireScript.Load(ctx, rdb).Result(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				requestID := strconv.Itoa(i)
				if indexed {
					ok, err := cache.AcquireAccountSlot(ctx, accountID, b.N+1, requestID)
					if err != nil || !ok {
						b.Fatalf("indexed admission: %v %v", ok, err)
					}
				} else {
					result, err := benchmarkPreIndexAcquireScript.Run(ctx, rdb, []string{key, live}, b.N+1, cache.slotTTLSeconds, requestID).Int()
					if err != nil || result != 1 {
						b.Fatalf("baseline admission: %v %v", result, err)
					}
				}
			}
		})
	}
}
