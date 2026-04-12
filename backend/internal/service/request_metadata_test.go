package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestRequestMetadataWriteAndRead_NoBridge(t *testing.T) {
	ctx := context.Background()
	state := NewModelCapacityRetryState(3 * time.Second)
	ctx = WithIsMaxTokensOneHaikuRequest(ctx, true, false)
	ctx = WithThinkingEnabled(ctx, true, false)
	ctx = WithPrefetchedStickySession(ctx, 123, 456, false)
	ctx = WithSingleAccountRetry(ctx, true, false)
	ctx = WithAccountSwitchCount(ctx, 2, false)
	ctx = WithAvoidEmailDomainSuffixes(ctx, []string{"Example.com", "example.com", "other.net"}, false)
	ctx = WithModelCapacityRetryState(ctx, state, false)

	isHaiku, ok := IsMaxTokensOneHaikuRequestFromContext(ctx)
	require.True(t, ok)
	require.True(t, isHaiku)

	thinking, ok := ThinkingEnabledFromContext(ctx)
	require.True(t, ok)
	require.True(t, thinking)

	accountID, ok := PrefetchedStickyAccountIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, int64(123), accountID)

	groupID, ok := PrefetchedStickyGroupIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, int64(456), groupID)

	singleRetry, ok := SingleAccountRetryFromContext(ctx)
	require.True(t, ok)
	require.True(t, singleRetry)

	switchCount, ok := AccountSwitchCountFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, 2, switchCount)

	avoidSuffixes := AvoidEmailDomainSuffixesFromContext(ctx)
	require.Equal(t, []string{"example.com", "other.net"}, avoidSuffixes)

	retryState := ModelCapacityRetryStateFromContext(ctx)
	require.Same(t, state, retryState)

	require.Nil(t, ctx.Value(ctxkey.IsMaxTokensOneHaikuRequest))
	require.Nil(t, ctx.Value(ctxkey.ThinkingEnabled))
	require.Nil(t, ctx.Value(ctxkey.PrefetchedStickyAccountID))
	require.Nil(t, ctx.Value(ctxkey.PrefetchedStickyGroupID))
	require.Nil(t, ctx.Value(ctxkey.SingleAccountRetry))
	require.Nil(t, ctx.Value(ctxkey.AccountSwitchCount))
}

func TestRequestMetadataWrite_BridgeLegacyKeys(t *testing.T) {
	ctx := context.Background()
	ctx = WithIsMaxTokensOneHaikuRequest(ctx, true, true)
	ctx = WithThinkingEnabled(ctx, true, true)
	ctx = WithPrefetchedStickySession(ctx, 123, 456, true)
	ctx = WithSingleAccountRetry(ctx, true, true)
	ctx = WithAccountSwitchCount(ctx, 2, true)

	require.Equal(t, true, ctx.Value(ctxkey.IsMaxTokensOneHaikuRequest))
	require.Equal(t, true, ctx.Value(ctxkey.ThinkingEnabled))
	require.Equal(t, int64(123), ctx.Value(ctxkey.PrefetchedStickyAccountID))
	require.Equal(t, int64(456), ctx.Value(ctxkey.PrefetchedStickyGroupID))
	require.Equal(t, true, ctx.Value(ctxkey.SingleAccountRetry))
	require.Equal(t, 2, ctx.Value(ctxkey.AccountSwitchCount))
}

func TestRequestMetadataRead_LegacyFallbackAndStats(t *testing.T) {
	beforeHaiku, beforeThinking, beforeAccount, beforeGroup, beforeSingleRetry, beforeSwitchCount := RequestMetadataFallbackStats()

	ctx := context.Background()
	ctx = context.WithValue(ctx, ctxkey.IsMaxTokensOneHaikuRequest, true)
	ctx = context.WithValue(ctx, ctxkey.ThinkingEnabled, true)
	ctx = context.WithValue(ctx, ctxkey.PrefetchedStickyAccountID, int64(321))
	ctx = context.WithValue(ctx, ctxkey.PrefetchedStickyGroupID, int64(654))
	ctx = context.WithValue(ctx, ctxkey.SingleAccountRetry, true)
	ctx = context.WithValue(ctx, ctxkey.AccountSwitchCount, int64(3))

	isHaiku, ok := IsMaxTokensOneHaikuRequestFromContext(ctx)
	require.True(t, ok)
	require.True(t, isHaiku)

	thinking, ok := ThinkingEnabledFromContext(ctx)
	require.True(t, ok)
	require.True(t, thinking)

	accountID, ok := PrefetchedStickyAccountIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, int64(321), accountID)

	groupID, ok := PrefetchedStickyGroupIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, int64(654), groupID)

	singleRetry, ok := SingleAccountRetryFromContext(ctx)
	require.True(t, ok)
	require.True(t, singleRetry)

	switchCount, ok := AccountSwitchCountFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, 3, switchCount)

	afterHaiku, afterThinking, afterAccount, afterGroup, afterSingleRetry, afterSwitchCount := RequestMetadataFallbackStats()
	require.Equal(t, beforeHaiku+1, afterHaiku)
	require.Equal(t, beforeThinking+1, afterThinking)
	require.Equal(t, beforeAccount+1, afterAccount)
	require.Equal(t, beforeGroup+1, afterGroup)
	require.Equal(t, beforeSingleRetry+1, afterSingleRetry)
	require.Equal(t, beforeSwitchCount+1, afterSwitchCount)
}

func TestRequestMetadataRead_PreferMetadataOverLegacy(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.ThinkingEnabled, false)
	ctx = WithThinkingEnabled(ctx, true, false)

	thinking, ok := ThinkingEnabledFromContext(ctx)
	require.True(t, ok)
	require.True(t, thinking)
	require.Equal(t, false, ctx.Value(ctxkey.ThinkingEnabled))
}

func TestModelCapacityRetryState_SpendAndRemaining(t *testing.T) {
	state := NewModelCapacityRetryState(3 * time.Second)
	require.True(t, state.CanSpend(1*time.Second))
	require.True(t, state.Spend(1*time.Second))
	require.Equal(t, 2*time.Second, state.Remaining())
	require.True(t, state.CanSpend(2*time.Second))
	require.True(t, state.Spend(2*time.Second))
	require.Equal(t, time.Duration(0), state.Remaining())
	require.False(t, state.CanSpend(1*time.Second))
	require.False(t, state.Spend(1*time.Second))
}
