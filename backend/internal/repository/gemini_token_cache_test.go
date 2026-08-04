//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGeminiTokenCache_DeleteAccessToken_RedisError(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	cache := NewGeminiTokenCache(rdb)
	err := cache.DeleteAccessToken(context.Background(), "broken")
	require.Error(t, err)
}

func TestGeminiTokenCache_OwnerLeaseCannotDeleteSuccessor(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := NewGeminiTokenCache(rdb).(*geminiTokenCache)
	ctx := context.Background()

	acquired, err := cache.AcquireRefreshLease(ctx, "grok:account:42", "owner-1", time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
	server.FastForward(2 * time.Second)
	acquired, err = cache.AcquireRefreshLease(ctx, "grok:account:42", "owner-2", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)

	require.NoError(t, cache.ReleaseRefreshLease(ctx, "grok:account:42", "owner-1"))
	value, err := rdb.Get(ctx, oauthRefreshLockKeyPrefix+"grok:account:42").Result()
	require.NoError(t, err)
	require.Equal(t, "owner-2", value)

	require.NoError(t, cache.ReleaseRefreshLease(ctx, "grok:account:42", "owner-2"))
	require.Equal(t, int64(0), rdb.Exists(ctx, oauthRefreshLockKeyPrefix+"grok:account:42").Val())
}
