package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestParseSubscriptionModelUsageCache(t *testing.T) {
	got := parseSubscriptionModelUsageCache(map[string]string{
		"status": "active",
		"model_usage:claude-fable-5:daily_usage_usd":                  "1.25",
		"model_usage:claude-fable-5:weekly_usage_usd":                 "2.5",
		"model_usage:anthropic.claude-fable-5-v1:0:monthly_usage_usd": "3.75",
	})
	require.InDelta(t, 1.25, got["claude-fable-5"].DailyUsageUSD, 1e-9)
	require.InDelta(t, 2.5, got["claude-fable-5"].WeeklyUsageUSD, 1e-9)
	require.InDelta(t, 3.75, got["anthropic.claude-fable-5-v1:0"].MonthlyUsageUSD, 1e-9)
}

func TestBillingCacheUpdateSubscriptionUsageForModelConcurrent(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &billingCache{rdb: rdb}
	ctx := context.Background()

	require.NoError(t, cache.SetSubscriptionCache(ctx, 1, 2, &service.SubscriptionCacheData{
		Status:     service.SubscriptionStatusActive,
		ExpiresAt:  time.Now().Add(time.Hour),
		ModelUsage: map[string]service.SubscriptionModelUsage{},
	}))

	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- cache.UpdateSubscriptionUsageForModel(ctx, 1, 2, 0.1, "claude-fable-5")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	got, err := cache.GetSubscriptionCache(ctx, 1, 2)
	require.NoError(t, err)
	require.InDelta(t, 2, got.DailyUsage, 1e-9)
	require.InDelta(t, 2, got.WeeklyUsage, 1e-9)
	require.InDelta(t, 2, got.MonthlyUsage, 1e-9)
	require.InDelta(t, 2, got.ModelUsage["claude-fable-5"].DailyUsageUSD, 1e-9)
	require.InDelta(t, 2, got.ModelUsage["claude-fable-5"].WeeklyUsageUSD, 1e-9)
	require.InDelta(t, 2, got.ModelUsage["claude-fable-5"].MonthlyUsageUSD, 1e-9)
}

func TestBillingCacheRejectsLegacySubscriptionSchema(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &billingCache{rdb: rdb}
	ctx := context.Background()

	require.NoError(t, rdb.HSet(ctx, billingSubKey(1, 2), map[string]any{
		subFieldStatus:    service.SubscriptionStatusActive,
		subFieldExpiresAt: time.Now().Add(time.Hour).Unix(),
	}).Err())
	_, err := cache.GetSubscriptionCache(ctx, 1, 2)
	require.Error(t, err)
}

func TestSetSubscriptionCacheDoesNotOverwriteUsageIncrement(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &billingCache{rdb: rdb}
	ctx := context.Background()
	stale := &service.SubscriptionCacheData{
		Status:       service.SubscriptionStatusActive,
		ExpiresAt:    time.Now().Add(time.Hour),
		DailyUsage:   1,
		WeeklyUsage:  1,
		MonthlyUsage: 1,
		ModelUsage: map[string]service.SubscriptionModelUsage{
			"claude-fable-5": {DailyUsageUSD: 1, WeeklyUsageUSD: 1, MonthlyUsageUSD: 1},
		},
	}

	require.NoError(t, cache.SetSubscriptionCache(ctx, 1, 2, stale))
	require.NoError(t, cache.UpdateSubscriptionUsageForModel(ctx, 1, 2, 0.5, "claude-fable-5"))
	require.NoError(t, cache.SetSubscriptionCache(ctx, 1, 2, stale))

	got, err := cache.GetSubscriptionCache(ctx, 1, 2)
	require.NoError(t, err)
	require.InDelta(t, 1.5, got.DailyUsage, 1e-9)
	require.InDelta(t, 1.5, got.ModelUsage["claude-fable-5"].DailyUsageUSD, 1e-9)
}

func TestSetSubscriptionCacheReplacesLegacySchema(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &billingCache{rdb: rdb}
	ctx := context.Background()
	key := billingSubKey(1, 2)

	require.NoError(t, rdb.HSet(ctx, key, subFieldStatus, "legacy").Err())
	require.NoError(t, cache.SetSubscriptionCache(ctx, 1, 2, &service.SubscriptionCacheData{
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: time.Now().Add(time.Hour),
	}))

	got, err := cache.GetSubscriptionCache(ctx, 1, 2)
	require.NoError(t, err)
	require.Equal(t, service.SubscriptionStatusActive, got.Status)
}
