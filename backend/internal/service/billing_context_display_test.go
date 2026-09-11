//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// solDisplayCatalogJSON 镜像远程目录的 Sol 条目形态：above_272k 绝对价折算成 272K 阶梯。
const solDisplayCatalogJSON = `{"gpt-5.6-sol": {"litellm_provider": "openai", "mode": "chat",
	"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05, "cache_read_input_token_cost": 5e-07,
	"input_cost_per_token_above_272k_tokens": 1e-05,
	"output_cost_per_token_above_272k_tokens": 4.5e-05,
	"cache_read_input_token_cost_above_272k_tokens": 1e-06}}`

// solChannelRow 镜像生产渠道行：只配基础价、不配区间（阶梯来自目录）。
func solChannelRow() []ChannelModelPricing {
	return []ChannelModelPricing{{
		Platform: PlatformOpenAI, Models: []string{"gpt-5.6-sol"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(4e-6), OutputPrice: testPtrFloat64(20e-6),
		CacheReadPrice: testPtrFloat64(0.4e-6), CacheWritePrice: testPtrFloat64(5e-6),
	}}
}

// 渠道行没配区间、目录带 272K 阶梯：分组开启时给出逐档绝对单价（渠道基础价 × 目录倍率），
// 缓存读/写与输入同倍率——这正是「可用分组」页原先看不到的部分。
func TestResolveDisplayContextIntervals_ChannelBaseWithCatalogLadder(t *testing.T) {
	bs, resolver := newTokenCostTestEnv(t, PlatformOpenAI, solChannelRow(), mustCatalogFromJSON(solDisplayCatalogJSON))

	intervals := bs.ResolveDisplayContextIntervals(context.Background(), resolver, "gpt-5.6-sol", PlatformOpenAI, enabledGroup(PlatformOpenAI))
	require.Len(t, intervals, 2)

	base, long := intervals[0], intervals[1]
	require.Equal(t, 0, base.MinTokens)
	require.NotNil(t, base.MaxTokens)
	require.Equal(t, 272000, *base.MaxTokens)
	require.Equal(t, "≤272K", base.TierLabel)
	require.InDelta(t, 4e-6, *base.InputPrice, 1e-12)
	require.InDelta(t, 20e-6, *base.OutputPrice, 1e-12)
	require.InDelta(t, 5e-6, *base.CacheWritePrice, 1e-12)
	require.InDelta(t, 0.4e-6, *base.CacheReadPrice, 1e-12)

	require.Equal(t, 272000, long.MinTokens)
	require.Nil(t, long.MaxTokens)
	require.Equal(t, ">272K", long.TierLabel)
	require.InDelta(t, 8e-6, *long.InputPrice, 1e-12)
	require.InDelta(t, 30e-6, *long.OutputPrice, 1e-12)
	require.InDelta(t, 10e-6, *long.CacheWritePrice, 1e-12)
	require.InDelta(t, 0.8e-6, *long.CacheReadPrice, 1e-12)
	require.Equal(t, 1, long.SortOrder)
}

// 分组关闭长上下文阶梯 → 实收只有基础档 → 不返回阶梯，调用方沿用平价展示。
func TestResolveDisplayContextIntervals_DisabledGroupHasNoTiers(t *testing.T) {
	bs, resolver := newTokenCostTestEnv(t, PlatformOpenAI, solChannelRow(), mustCatalogFromJSON(solDisplayCatalogJSON))

	require.Nil(t, bs.ResolveDisplayContextIntervals(context.Background(), resolver, "gpt-5.6-sol", PlatformOpenAI, disabledGroup(PlatformOpenAI)))
}

// 非 token 计费、无定价来源、依赖缺失：一律 nil，不得报错或 panic。
func TestResolveDisplayContextIntervals_NonTokenOrUnknownIsNil(t *testing.T) {
	imageRow := []ChannelModelPricing{{
		Platform: PlatformOpenAI, Models: []string{"gpt-image-2"}, BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.04),
	}}
	bs, resolver := newTokenCostTestEnv(t, PlatformOpenAI, imageRow, mustCatalogFromJSON(solDisplayCatalogJSON))

	require.Nil(t, bs.ResolveDisplayContextIntervals(context.Background(), resolver, "gpt-image-2", PlatformOpenAI, enabledGroup(PlatformOpenAI)))
	require.Nil(t, bs.ResolveDisplayContextIntervals(context.Background(), resolver, "unknown-model-xyz", PlatformOpenAI, enabledGroup(PlatformOpenAI)))
	require.Nil(t, bs.ResolveDisplayContextIntervals(context.Background(), nil, "gpt-5.6-sol", PlatformOpenAI, enabledGroup(PlatformOpenAI)))
	var nilSvc *BillingService
	require.Nil(t, nilSvc.ResolveDisplayContextIntervals(context.Background(), resolver, "gpt-5.6-sol", PlatformOpenAI, enabledGroup(PlatformOpenAI)))
}
