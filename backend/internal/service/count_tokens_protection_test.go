package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type countTokensProtectionRPMCache struct {
	UserRPMCache

	normalUserGroupCalls int32
	normalUserCalls      int32
	ctUserGroupCalls     int32
	ctUserCalls          int32

	ctUserGroupCounts []int
	ctUserCounts      []int
}

func (c *countTokensProtectionRPMCache) IncrementUserGroupRPM(context.Context, int64, int64) (int, error) {
	atomic.AddInt32(&c.normalUserGroupCalls, 1)
	return 1, nil
}

func (c *countTokensProtectionRPMCache) IncrementUserRPM(context.Context, int64) (int, error) {
	atomic.AddInt32(&c.normalUserCalls, 1)
	return 1, nil
}

func (c *countTokensProtectionRPMCache) IncrementCountTokensUserGroupRPM(context.Context, int64, int64) (int, error) {
	idx := int(atomic.AddInt32(&c.ctUserGroupCalls, 1)) - 1
	if idx < len(c.ctUserGroupCounts) {
		return c.ctUserGroupCounts[idx], nil
	}
	return 1, nil
}

func (c *countTokensProtectionRPMCache) IncrementCountTokensUserRPM(context.Context, int64) (int, error) {
	idx := int(atomic.AddInt32(&c.ctUserCalls, 1)) - 1
	if idx < len(c.ctUserCounts) {
		return c.ctUserCounts[idx], nil
	}
	return 1, nil
}

type countTokensProtectionRateRepo struct {
	UserGroupRateRepository

	override *int
	err      error
	calls    int32
}

func (r *countTokensProtectionRateRepo) GetRPMOverrideByUserAndGroup(context.Context, int64, int64) (*int, error) {
	atomic.AddInt32(&r.calls, 1)
	if r.err != nil {
		return nil, r.err
	}
	return r.override, nil
}

func TestCountTokensRPMUsesIsolatedCounters(t *testing.T) {
	cache := &countTokensProtectionRPMCache{
		ctUserGroupCounts: []int{1},
		ctUserCounts:      []int{1},
	}
	svc := &BillingCacheService{
		userRPMCache:      cache,
		userGroupRateRepo: &countTokensProtectionRateRepo{},
	}

	user := &User{ID: 1, RPMLimit: 10}
	group := &Group{ID: 10, RPMLimit: 10}

	require.NoError(t, svc.checkCountTokensRPM(context.Background(), user, group))
	require.EqualValues(t, 0, atomic.LoadInt32(&cache.normalUserGroupCalls))
	require.EqualValues(t, 0, atomic.LoadInt32(&cache.normalUserCalls))
	require.EqualValues(t, 1, atomic.LoadInt32(&cache.ctUserGroupCalls))
	require.EqualValues(t, 1, atomic.LoadInt32(&cache.ctUserCalls))
}

func TestCountTokensRPMReturnsDedicatedErrors(t *testing.T) {
	t.Run("group", func(t *testing.T) {
		cache := &countTokensProtectionRPMCache{ctUserGroupCounts: []int{6}}
		svc := &BillingCacheService{
			userRPMCache:      cache,
			userGroupRateRepo: &countTokensProtectionRateRepo{},
		}

		err := svc.checkCountTokensRPM(
			context.Background(),
			&User{ID: 1, RPMLimit: 10},
			&Group{ID: 10, RPMLimit: 5},
		)
		require.ErrorIs(t, err, ErrCountTokensGroupRPMExceeded)
		require.EqualValues(t, 0, atomic.LoadInt32(&cache.ctUserCalls))
	})

	t.Run("user", func(t *testing.T) {
		cache := &countTokensProtectionRPMCache{
			ctUserGroupCounts: []int{1},
			ctUserCounts:      []int{3},
		}
		svc := &BillingCacheService{
			userRPMCache:      cache,
			userGroupRateRepo: &countTokensProtectionRateRepo{},
		}

		err := svc.checkCountTokensRPM(
			context.Background(),
			&User{ID: 1, RPMLimit: 2},
			&Group{ID: 10, RPMLimit: 10},
		)
		require.ErrorIs(t, err, ErrCountTokensUserRPMExceeded)
		require.EqualValues(t, 1, atomic.LoadInt32(&cache.ctUserCalls))
	})
}

type countTokensProtectionConcurrencyCache struct {
	ConcurrencyCache

	acquireCalls     atomic.Int32
	releaseCalls     atomic.Int32
	acquireResult    bool
	acquireErr       error
	acquireMax       int
	acquireRequestID string
	releaseUserID    int64
	releaseRequestID string
}

func (c *countTokensProtectionConcurrencyCache) AcquireCountTokensUserSlot(_ context.Context, _ int64, maxConcurrency int, requestID string) (bool, error) {
	c.acquireCalls.Add(1)
	c.acquireMax = maxConcurrency
	c.acquireRequestID = requestID
	if c.acquireErr != nil {
		return false, c.acquireErr
	}
	return c.acquireResult, nil
}

func (c *countTokensProtectionConcurrencyCache) ReleaseCountTokensUserSlot(_ context.Context, userID int64, requestID string) error {
	c.releaseCalls.Add(1)
	c.releaseUserID = userID
	c.releaseRequestID = requestID
	return nil
}

func TestCountTokensConcurrencyUsesIsolatedSlot(t *testing.T) {
	require.Equal(t, 2, CalculateCountTokensMaxConcurrency(0))
	require.Equal(t, 2, CalculateCountTokensMaxConcurrency(4))
	require.Equal(t, 5, CalculateCountTokensMaxConcurrency(10))
	require.Equal(t, 5, CalculateCountTokensMaxConcurrency(20))

	cache := &countTokensProtectionConcurrencyCache{acquireResult: true}
	svc := NewConcurrencyService(cache)

	result, err := svc.AcquireCountTokensUserSlot(context.Background(), 42, 8)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.EqualValues(t, 1, cache.acquireCalls.Load())
	require.Equal(t, 4, cache.acquireMax)
	require.NotEmpty(t, cache.acquireRequestID)

	result.ReleaseFunc()
	require.Eventually(t, func() bool {
		return cache.releaseCalls.Load() == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, int64(42), cache.releaseUserID)
	require.Equal(t, cache.acquireRequestID, cache.releaseRequestID)
}

func TestCountTokensConcurrencyPropagatesAcquireError(t *testing.T) {
	cache := &countTokensProtectionConcurrencyCache{acquireErr: errors.New("redis down")}
	svc := NewConcurrencyService(cache)

	result, err := svc.AcquireCountTokensUserSlot(context.Background(), 42, 8)
	require.Error(t, err)
	require.Nil(t, result)
}
