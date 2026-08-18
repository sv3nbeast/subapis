package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCachePreSemanticRetryMarkerIsSharedAndBounded(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &gatewayCache{rdb: rdb}
	ctx := context.Background()

	require.NoError(t, cache.MarkPreSemanticFailure(ctx, "fingerprint-a", "gateway-retry:v1:logical-a", 2*time.Minute))
	// SETNX preserves the first logical request when multiple failed attempts
	// race before the next client retry arrives.
	require.NoError(t, cache.MarkPreSemanticFailure(ctx, "fingerprint-a", "gateway-retry:v1:logical-b", 2*time.Minute))

	logicalID, ok, err := cache.GetPreSemanticFailure(ctx, "fingerprint-a")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "gateway-retry:v1:logical-a", logicalID)

	logicalID, ok, err = cache.GetPreSemanticFailure(ctx, "fingerprint-a")
	require.NoError(t, err)
	require.True(t, ok, "repeated retries share the marker until the successful settlement clears it")
	require.Equal(t, "gateway-retry:v1:logical-a", logicalID)
	require.NoError(t, cache.ClearPreSemanticFailure(ctx, "fingerprint-a"))
	_, ok, err = cache.GetPreSemanticFailure(ctx, "fingerprint-a")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, cache.MarkPreSemanticFailure(ctx, "fingerprint-expiring", "gateway-retry:v1:logical-expiring", time.Minute))
	require.NotEmpty(t, rdb.Keys(ctx, gatewayRetryBillingPrefix+"*").Val())
	mr.FastForward(61 * time.Second)
	_, ok, err = cache.GetPreSemanticFailure(ctx, "fingerprint-expiring")
	require.NoError(t, err)
	require.False(t, ok, "expired retry markers must not link a later deliberate request")
}

func TestGatewayCachePreSemanticRetryMarkerConcurrentSetNXKeepsOneLogicalID(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &gatewayCache{rdb: rdb}
	ctx := context.Background()

	const attempts = 32
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = cache.MarkPreSemanticFailure(ctx, "concurrent-fingerprint", fmt.Sprintf("gateway-retry:v1:logical-%d", i), time.Minute)
		}(i)
	}
	wg.Wait()

	logicalID, ok, err := cache.GetPreSemanticFailure(ctx, "concurrent-fingerprint")
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, logicalID, "gateway-retry:v1:logical-")
	for i := 0; i < attempts; i++ {
		got, gotOK, gotErr := cache.GetPreSemanticFailure(ctx, "concurrent-fingerprint")
		require.NoError(t, gotErr)
		require.True(t, gotOK)
		require.Equal(t, logicalID, got)
	}
}
