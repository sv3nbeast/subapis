package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// gpt55OverrideCatalogJSON 镜像真实目录形态：长上下文以 above_272k 绝对价字段表达。
const gpt55OverrideCatalogJSON = `{
	"gpt-5.5": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 5e-06, "input_cost_per_token_priority": 1.25e-05,
		"output_cost_per_token": 3e-05, "output_cost_per_token_priority": 7.5e-05,
		"cache_read_input_token_cost": 5e-07,
		"input_cost_per_token_above_272k_tokens": 1e-05,
		"output_cost_per_token_above_272k_tokens": 4.5e-05,
		"cache_read_input_token_cost_above_272k_tokens": 1e-06},
	"gpt-5.4": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 2.5e-06, "output_cost_per_token": 1.5e-05,
		"cache_read_input_token_cost": 2.5e-07,
		"input_cost_per_token_above_272k_tokens": 5e-06,
		"output_cost_per_token_above_272k_tokens": 2.25e-05}
}`

func newPricingServiceWithOverride(t *testing.T, overrideJSON string) *PricingService {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte(overrideJSON), 0644))
	svc := &PricingService{cfg: &config.Config{}}
	svc.cfg.Pricing.OverrideFile = path
	return svc
}

// override 的旗舰用例：显式 threshold=0 压住 above 折算，把目录条目的阶梯关成标准价。
func TestPricingOverride_ExplicitZeroThresholdDisablesCatalogLadder(t *testing.T) {
	svc := newPricingServiceWithOverride(t, `{"gpt-5.5": {"long_context_input_token_threshold": 0}}`)
	data, err := svc.parsePricingData([]byte(gpt55OverrideCatalogJSON))
	require.NoError(t, err)

	patched := data["gpt-5.5"]
	require.NotNil(t, patched)
	require.Zero(t, patched.LongContextInputTokenThreshold)
	require.Zero(t, patched.LongContextInputCostMultiplier)
	require.InDelta(t, 5e-6, patched.InputCostPerToken, 1e-12, "补丁不得影响基础价")
	require.InDelta(t, 3e-5, patched.OutputCostPerToken, 1e-12)
	require.Equal(t, 272000, data["gpt-5.4"].LongContextInputTokenThreshold, "未覆盖的模型保持目录阶梯")

	svc.pricingData = data
	billing := NewBillingService(&config.Config{}, svc)
	tokens := UsageTokens{InputTokens: 300000, OutputTokens: 1000, CacheReadTokens: 10000}
	cost, err := billing.CalculateCost("gpt-5.5", tokens, 1)
	require.NoError(t, err)
	require.False(t, cost.LongContextBillingApplied)
	require.InDelta(t, 300000*5e-6, cost.InputCost, 1e-10)
	require.InDelta(t, 1000*3e-5, cost.OutputCost, 1e-10)
	require.InDelta(t, 10000*5e-7, cost.CacheReadCost, 1e-10)
}

func TestPricingOverride_FieldLevelMergeKeepsOtherFields(t *testing.T) {
	svc := newPricingServiceWithOverride(t, `{"gpt-5.4": {"input_cost_per_token": 3e-06}}`)
	data, err := svc.parsePricingData([]byte(gpt55OverrideCatalogJSON))
	require.NoError(t, err)

	patched := data["gpt-5.4"]
	require.InDelta(t, 3e-6, patched.InputCostPerToken, 1e-12)
	require.InDelta(t, 1.5e-5, patched.OutputCostPerToken, 1e-12, "未覆盖字段保持目录值")
	require.Equal(t, "openai", patched.LiteLLMProvider)
	require.Equal(t, 272000, patched.LongContextInputTokenThreshold, "above 折算仍生效")
	// 折算发生在合并之后：above 价不变、基础价被补丁改小，倍率随之变化。
	require.InDelta(t, 5.0/3.0, patched.LongContextInputCostMultiplier, 1e-9)
}

func TestPricingOverride_NullFieldValueRemovesField(t *testing.T) {
	svc := newPricingServiceWithOverride(t, `{"gpt-5.5": {
		"input_cost_per_token_above_272k_tokens": null,
		"output_cost_per_token_above_272k_tokens": null,
		"cache_read_input_token_cost_above_272k_tokens": null}}`)
	data, err := svc.parsePricingData([]byte(gpt55OverrideCatalogJSON))
	require.NoError(t, err)
	require.Zero(t, data["gpt-5.5"].LongContextInputTokenThreshold, "above 字段删除后不再折算阶梯")
	require.InDelta(t, 5e-6, data["gpt-5.5"].InputCostPerToken, 1e-12)
}

// 完整加载管线：纯补丁不得抢在回退合并前建条目（否则回退完整条目被跳过、
// 其余分项价变 0 少收）；目录/回退都没有的模型作为独立条目并入。
func TestPricingOverride_LoadPipelineAddsNewModelAndPatchesFallbackOnly(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	require.NoError(t, os.WriteFile(catalogPath, []byte(`{
		"remote-model": {"litellm_provider": "test", "mode": "chat",
			"input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06}
	}`), 0644))
	fallbackPath := filepath.Join(dir, "fallback.json")
	require.NoError(t, os.WriteFile(fallbackPath, []byte(`{
		"fallback-only-model": {"litellm_provider": "test", "mode": "chat",
			"input_cost_per_token": 4e-06, "output_cost_per_token": 8e-06,
			"cache_read_input_token_cost": 4e-07}
	}`), 0644))
	overridePath := filepath.Join(dir, "overrides.json")
	require.NoError(t, os.WriteFile(overridePath, []byte(`{
		"fallback-only-model": {"input_cost_per_token": 9e-06},
		"override-new-model": {"litellm_provider": "test", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 1e-05}
	}`), 0644))

	svc := &PricingService{cfg: &config.Config{}}
	svc.cfg.Pricing.FallbackFile = fallbackPath
	svc.cfg.Pricing.OverrideFile = overridePath
	require.NoError(t, svc.loadPricingData(catalogPath))

	patched := svc.pricingData["fallback-only-model"]
	require.NotNil(t, patched)
	require.InDelta(t, 9e-6, patched.InputCostPerToken, 1e-12)
	require.InDelta(t, 8e-6, patched.OutputCostPerToken, 1e-12, "回退条目的其余字段必须保留")
	require.InDelta(t, 4e-7, patched.CacheReadInputTokenCost, 1e-12)

	added := svc.pricingData["override-new-model"]
	require.NotNil(t, added)
	require.InDelta(t, 5e-6, added.InputCostPerToken, 1e-12)
	require.InDelta(t, 1e-5, added.OutputCostPerToken, 1e-12)

	require.InDelta(t, 1e-6, svc.pricingData["remote-model"].InputCostPerToken, 1e-12)
}

// 拼错模型名（或纯补丁落在不存在的模型上）会被有效性过滤丢弃，必须有哨兵 WARN。
func TestPricingOverride_IneffectiveEntryWarns(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	require.NoError(t, os.WriteFile(catalogPath, []byte(`{
		"remote-model": {"litellm_provider": "test", "mode": "chat", "input_cost_per_token": 1e-06}
	}`), 0644))
	overridePath := filepath.Join(dir, "overrides.json")
	require.NoError(t, os.WriteFile(overridePath, []byte(`{
		"typo-model": {"long_context_input_token_threshold": 0}
	}`), 0644))

	svc := &PricingService{cfg: &config.Config{}}
	svc.cfg.Pricing.OverrideFile = overridePath
	require.NoError(t, svc.loadPricingData(catalogPath))

	require.NotContains(t, svc.pricingData, "typo-model")
	require.True(t, logSink.ContainsMessageAtLevel("override had no effect for 1 model(s): typo-model", "warn"))
}

func TestPricingOverride_NonObjectEntryKeepsCatalogEntry(t *testing.T) {
	svc := newPricingServiceWithOverride(t, `{"gpt-5.5": "oops"}`)
	data, err := svc.parsePricingData([]byte(gpt55OverrideCatalogJSON))
	require.NoError(t, err)
	require.Equal(t, 272000, data["gpt-5.5"].LongContextInputTokenThreshold, "非法补丁忽略，目录条目原样保留")
	require.InDelta(t, 5e-6, data["gpt-5.5"].InputCostPerToken, 1e-12)
}

func TestPricingOverride_MissingOrInvalidFileIsIgnored(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		svc := &PricingService{cfg: &config.Config{}}
		svc.cfg.Pricing.OverrideFile = filepath.Join(t.TempDir(), "absent.json")
		data, err := svc.parsePricingData([]byte(gpt55OverrideCatalogJSON))
		require.NoError(t, err)
		require.Equal(t, 272000, data["gpt-5.5"].LongContextInputTokenThreshold)
	})

	t.Run("invalid json", func(t *testing.T) {
		svc := newPricingServiceWithOverride(t, `{invalid`)
		data, err := svc.parsePricingData([]byte(gpt55OverrideCatalogJSON))
		require.NoError(t, err)
		require.Equal(t, 272000, data["gpt-5.5"].LongContextInputTokenThreshold)
	})
}

// 对真实出厂目录快照关闭 gpt-5.5 阶梯：计费视角阈值归零、基础价不变，
// 其他模型（gpt-5.4）的目录阶梯不受影响。
func TestPricingOverride_DisablesGPT55LadderOnDefaultCatalog(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	svc := newPricingServiceWithOverride(t, `{
		"gpt-5.5": {"long_context_input_token_threshold": 0},
		"gpt-5.5-2026-04-23": {"long_context_input_token_threshold": 0}
	}`)
	data, err := svc.parsePricingData(body)
	require.NoError(t, err)
	svc.pricingData = data
	billing := NewBillingService(&config.Config{}, svc)

	for _, model := range []string{"gpt-5.5", "gpt-5.5-2026-04-23"} {
		pricing, err := billing.GetModelPricing(model)
		require.NoError(t, err)
		require.Zero(t, pricing.LongContextInputThreshold, model)
		require.InDelta(t, 5e-6, pricing.InputPricePerToken, 1e-12, model)
	}

	pricing, err := billing.GetModelPricing("gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, 272000, pricing.LongContextInputThreshold, "其他模型的目录阶梯不受影响")
}

// deployModelPricingOverridesPath 是随仓库发布的生产 override 补丁；部署时拷到
// /app/data/model_pricing_overrides.json 并由 config.yaml 的 pricing.override_file 指向。
const deployModelPricingOverridesPath = "../../../deploy/model_pricing_overrides.json"

// staleRemoteSolCatalogJSON 镜像远程目录中滞后的 Sol 条目：$5/$30 基础价 + above_272k 绝对价。
const staleRemoteSolCatalogJSON = `{
	"gpt-5.6-sol": {"litellm_provider": "openai", "mode": "chat", "supports_service_tier": true,
		"input_cost_per_token": 5e-06, "input_cost_per_token_priority": 1e-05,
		"output_cost_per_token": 3e-05, "output_cost_per_token_priority": 6e-05,
		"cache_read_input_token_cost": 5e-07, "cache_read_input_token_cost_priority": 1e-06,
		"cache_creation_input_token_cost": 6.25e-06, "cache_creation_input_token_cost_priority": 1.25e-05,
		"input_cost_per_token_above_272k_tokens": 1e-05, "input_cost_per_token_above_272k_tokens_priority": 2e-05,
		"output_cost_per_token_above_272k_tokens": 4.5e-05, "output_cost_per_token_above_272k_tokens_priority": 9e-05,
		"cache_read_input_token_cost_above_272k_tokens": 1e-06, "cache_read_input_token_cost_above_272k_tokens_priority": 2e-06,
		"cache_creation_input_token_cost_above_272k_tokens": 1.25e-05, "cache_creation_input_token_cost_above_272k_tokens_priority": 2.5e-05}
}`

// 生产 override 补丁必须把滞后的远程 Sol 条目压到官方现行价（2026-11-21 前 $4/$20，
// Fast 2x，缓存读 $0.40、缓存写 $5），且 272K 阶梯（输入/缓存 2x、输出 1.5x）保持不变。
func TestPricingOverride_DeployFilePinsSolOfficialPricingOverStaleRemote(t *testing.T) {
	svc := &PricingService{cfg: &config.Config{}}
	svc.cfg.Pricing.OverrideFile = deployModelPricingOverridesPath
	data, err := svc.parsePricingData([]byte(staleRemoteSolCatalogJSON))
	require.NoError(t, err)

	sol := data["gpt-5.6-sol"]
	require.NotNil(t, sol)
	require.InDelta(t, 4e-6, sol.InputCostPerToken, 1e-12)
	require.InDelta(t, 8e-6, sol.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 2e-5, sol.OutputCostPerToken, 1e-12)
	require.InDelta(t, 4e-5, sol.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 4e-7, sol.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 8e-7, sol.CacheReadInputTokenCostPriority, 1e-12)
	require.InDelta(t, 5e-6, sol.CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 1e-5, sol.CacheCreationInputTokenCostPriority, 1e-12)
	require.Equal(t, 272000, sol.LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, sol.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, sol.LongContextOutputCostMultiplier, 1e-12)

	svc.pricingData = data
	billing := NewBillingService(&config.Config{}, svc)

	standard, err := billing.CalculateCost("gpt-5.6-sol", UsageTokens{InputTokens: 1000, CacheReadTokens: 1000, CacheCreationTokens: 1000, OutputTokens: 1000}, 1)
	require.NoError(t, err)
	require.False(t, standard.LongContextBillingApplied)
	require.InDelta(t, 1000*4e-6, standard.InputCost, 1e-12)
	require.InDelta(t, 1000*0.4e-6, standard.CacheReadCost, 1e-12)
	require.InDelta(t, 1000*5e-6, standard.CacheCreationCost, 1e-12)
	require.InDelta(t, 1000*20e-6, standard.OutputCost, 1e-12)

	longCtx, err := billing.CalculateCost("gpt-5.6-sol", UsageTokens{InputTokens: 300000, CacheReadTokens: 10000, CacheCreationTokens: 1000, OutputTokens: 1000}, 1)
	require.NoError(t, err)
	require.True(t, longCtx.LongContextBillingApplied)
	require.InDelta(t, 300000*8e-6, longCtx.InputCost, 1e-9, "长上下文输入 $8")
	require.InDelta(t, 10000*0.8e-6, longCtx.CacheReadCost, 1e-9, "长上下文缓存读 $0.80")
	require.InDelta(t, 1000*10e-6, longCtx.CacheCreationCost, 1e-9, "长上下文缓存写 $10")
	require.InDelta(t, 1000*30e-6, longCtx.OutputCost, 1e-9, "长上下文输出 $30")

	fast, err := billing.CalculateCostWithServiceTier("gpt-5.6-sol", UsageTokens{InputTokens: 1000, CacheReadTokens: 1000, OutputTokens: 1000}, 1, "priority")
	require.NoError(t, err)
	require.InDelta(t, 1000*8e-6, fast.InputCost, 1e-12, "Fast 输入 $8")
	require.InDelta(t, 1000*0.8e-6, fast.CacheReadCost, 1e-12, "Fast 缓存读 $0.80")
	require.InDelta(t, 1000*40e-6, fast.OutputCost, 1e-12, "Fast 输出 $40")
}
