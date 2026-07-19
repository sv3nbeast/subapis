package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type blockingSubscriptionPopulateCache struct {
	BillingCache
}

func (c *blockingSubscriptionPopulateCache) GetSubscriptionCache(context.Context, int64, int64) (*SubscriptionCacheData, error) {
	return nil, errors.New("cache miss")
}

func (c *blockingSubscriptionPopulateCache) SetSubscriptionCache(ctx context.Context, _ int64, _ int64, _ *SubscriptionCacheData) error {
	<-ctx.Done()
	return ctx.Err()
}

type subscriptionStatusRepoStub struct {
	UserSubscriptionRepository
	sub *UserSubscription
}

func (r *subscriptionStatusRepoStub) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	return r.sub, nil
}

func TestGetSubscriptionStatusBoundsSynchronousCachePopulate(t *testing.T) {
	now := time.Now()
	svc := NewBillingCacheService(
		&blockingSubscriptionPopulateCache{},
		nil,
		&subscriptionStatusRepoStub{sub: &UserSubscription{
			Status:    SubscriptionStatusActive,
			ExpiresAt: now.Add(time.Hour),
			UpdatedAt: now,
		}},
		nil,
		nil,
		nil,
		&config.Config{},
	)
	t.Cleanup(svc.Stop)

	started := time.Now()
	got, err := svc.GetSubscriptionStatus(context.Background(), 1, 2)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Less(t, time.Since(started), 250*time.Millisecond)
}
