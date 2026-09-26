//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Once the channel cache has been built successfully, a failing rebuild must
// keep serving that snapshot. Falling back to an empty cache would silently
// drop channel pricing, model mapping and restrict_models for all traffic until
// the data is fixed — which is what one NULL tier_label did on 2026-09-24.

type lastGoodRepoState struct {
	channels          []Channel
	listAllErr        error
	groupPlatformsErr error
	listAllCalls      int
}

func newLastGoodRepo(state *lastGoodRepoState) *mockChannelRepository {
	return &mockChannelRepository{
		listAllFn: func(_ context.Context) ([]Channel, error) {
			state.listAllCalls++
			if state.listAllErr != nil {
				return nil, state.listAllErr
			}
			return state.channels, nil
		},
		getGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			if state.groupPlatformsErr != nil {
				return nil, state.groupPlatformsErr
			}
			return map[int64]string{10: PlatformAnthropic}, nil
		},
	}
}

func lastGoodRestrictedChannel(inputPrice float64) Channel {
	return Channel{
		ID:             1,
		Status:         StatusActive,
		GroupIDs:       []int64{10},
		RestrictModels: true,
		ModelPricing: []ChannelModelPricing{
			{ID: 100, Platform: PlatformAnthropic, Models: []string{"claude-opus-4"}, InputPrice: testPtrFloat64(inputPrice)},
		},
		ModelMapping: map[string]map[string]string{
			PlatformAnthropic: {"opus-alias": "claude-opus-4"},
		},
	}
}

// requireLastGoodServed asserts that pricing, mapping, the channel and the
// model restriction all still come from the last good snapshot.
func requireLastGoodServed(t *testing.T, svc *ChannelService, inputPrice float64) {
	t.Helper()
	ctx := context.Background()

	pricing := svc.GetChannelModelPricing(ctx, 10, "claude-opus-4")
	require.NotNil(t, pricing, "channel pricing must survive a failed rebuild")
	require.InDelta(t, inputPrice, *pricing.InputPrice, 1e-12)

	mapping := svc.ResolveChannelMapping(ctx, 10, "opus-alias")
	require.True(t, mapping.Mapped)
	require.Equal(t, "claude-opus-4", mapping.MappedModel)

	ch, err := svc.GetChannelForGroup(ctx, 10)
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.Equal(t, int64(1), ch.ID)

	require.True(t, svc.IsModelRestricted(ctx, 10, "claude-sonnet-4"),
		"restrict_models must keep rejecting unpriced models")
	require.False(t, svc.IsModelRestricted(ctx, 10, "claude-opus-4"))
}

func TestChannelCache_InvalidationRebuildFailureServesLastGood(t *testing.T) {
	state := &lastGoodRepoState{channels: []Channel{lastGoodRestrictedChannel(15e-6)}}
	svc := newTestChannelService(newLastGoodRepo(state))
	requireLastGoodServed(t, svc, 15e-6)

	state.listAllErr = errors.New("sql: Scan error on column index 2: converting NULL to string is unsupported")
	// invalidateCache clears the in-process snapshot before rebuilding, so the
	// last good copy must outlive the clear.
	svc.InvalidateCache()
	requireLastGoodServed(t, svc, 15e-6)
}

func TestChannelCache_ExpiredRebuildFailureServesLastGood(t *testing.T) {
	state := &lastGoodRepoState{channels: []Channel{lastGoodRestrictedChannel(15e-6)}}
	svc := newTestChannelService(newLastGoodRepo(state))
	requireLastGoodServed(t, svc, 15e-6)
	callsBefore := state.listAllCalls

	state.listAllErr = errors.New("database down")
	expired := *svc.cache.Load().(*channelCache)
	expired.loadedAt = time.Now().Add(-channelCacheTTL - time.Second)
	svc.cache.Store(&expired)

	// The request that triggers the failing rebuild is served from the last
	// good snapshot rather than an error.
	ch, err := svc.GetChannelForGroup(context.Background(), 10)
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.Equal(t, callsBefore+1, state.listAllCalls)

	// Within the error TTL the fallback snapshot is reused without hitting the
	// database again.
	requireLastGoodServed(t, svc, 15e-6)
	require.Equal(t, callsBefore+1, state.listAllCalls)

	// The fallback carries the short error TTL so a fixed database is picked
	// up quickly.
	fallback := svc.cache.Load().(*channelCache)
	require.Less(t, channelCacheTTL-time.Since(fallback.loadedAt), channelErrorTTL+time.Second)
}

func TestChannelCache_GroupPlatformFailureServesLastGood(t *testing.T) {
	state := &lastGoodRepoState{channels: []Channel{lastGoodRestrictedChannel(15e-6)}}
	svc := newTestChannelService(newLastGoodRepo(state))
	requireLastGoodServed(t, svc, 15e-6)

	state.groupPlatformsErr = errors.New("group platforms failed")
	svc.InvalidateCache()
	requireLastGoodServed(t, svc, 15e-6)
}

func TestChannelCache_RecoveryReplacesLastGood(t *testing.T) {
	state := &lastGoodRepoState{channels: []Channel{lastGoodRestrictedChannel(15e-6)}}
	svc := newTestChannelService(newLastGoodRepo(state))
	requireLastGoodServed(t, svc, 15e-6)

	state.listAllErr = errors.New("database down")
	svc.InvalidateCache()
	requireLastGoodServed(t, svc, 15e-6)

	// The database comes back with new prices: the next rebuild replaces the
	// stale snapshot instead of pinning it.
	state.listAllErr = nil
	state.channels = []Channel{lastGoodRestrictedChannel(20e-6)}
	svc.InvalidateCache()
	requireLastGoodServed(t, svc, 20e-6)
}
