package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheCommitKiroCacheFingerprintsReportsExisting(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &gatewayCache{rdb: rdb}
	entries := map[string]time.Duration{
		"fp-a": time.Minute,
		"fp-b": time.Minute,
	}

	first, err := cache.CommitKiroCacheFingerprints(context.Background(), "logical-session", entries)
	require.NoError(t, err)
	require.False(t, first["fp-a"])
	require.False(t, first["fp-b"])

	second, err := cache.CommitKiroCacheFingerprints(context.Background(), "logical-session", entries)
	require.NoError(t, err)
	require.True(t, second["fp-a"])
	require.True(t, second["fp-b"])
}

func TestGatewayCacheCommitKiroCacheFingerprintsConcurrentCreator(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &gatewayCache{rdb: rdb}

	const workers = 8
	start := make(chan struct{})
	results := make(chan bool, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			existing, err := cache.CommitKiroCacheFingerprints(context.Background(), "parallel-session", map[string]time.Duration{
				"fp-shared": time.Minute,
			})
			errs <- err
			results <- existing["fp-shared"]
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	creators := 0
	for existed := range results {
		if !existed {
			creators++
		}
	}
	require.Equal(t, 1, creators)
}

func TestGatewayCacheCommitKiroCacheFingerprintsPreservesLongestTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &gatewayCache{rdb: rdb}
	ctx := context.Background()
	stableKey := "ttl-session"
	fingerprint := "fp-ttl"
	key := buildKiroCacheFingerprintKey(stableKey, fingerprint)

	_, err := cache.CommitKiroCacheFingerprints(ctx, stableKey, map[string]time.Duration{fingerprint: 15 * time.Second})
	require.NoError(t, err)
	initialTTL, err := rdb.PTTL(ctx, key).Result()
	require.NoError(t, err)
	require.Greater(t, initialTTL, 10*time.Second)

	_, err = cache.CommitKiroCacheFingerprints(ctx, stableKey, map[string]time.Duration{fingerprint: 2 * time.Minute})
	require.NoError(t, err)
	extendedTTL, err := rdb.PTTL(ctx, key).Result()
	require.NoError(t, err)
	require.Greater(t, extendedTTL, time.Minute)

	_, err = cache.CommitKiroCacheFingerprints(ctx, stableKey, map[string]time.Duration{fingerprint: 30 * time.Second})
	require.NoError(t, err)
	afterShortCommit, err := rdb.PTTL(ctx, key).Result()
	require.NoError(t, err)
	require.Greater(t, afterShortCommit, time.Minute)

	persistentFingerprint := "fp-persistent"
	persistentKey := buildKiroCacheFingerprintKey(stableKey, persistentFingerprint)
	require.NoError(t, rdb.Set(ctx, persistentKey, "1", 0).Err())
	_, err = cache.CommitKiroCacheFingerprints(ctx, stableKey, map[string]time.Duration{persistentFingerprint: time.Minute})
	require.NoError(t, err)
	persistentTTL, err := rdb.PTTL(ctx, persistentKey).Result()
	require.NoError(t, err)
	require.Greater(t, persistentTTL, 50*time.Second)
}
