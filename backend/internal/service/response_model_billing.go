package service

import (
	"context"
	"log/slog"
	"strings"
)

const responseModelBillingCostEpsilon = 1e-12

// responseModelBillingDeclaration 返回可用于计费的上游响应模型；返回空字符串表示
// 必须沿用基线计费模型。两条计费主干（Anthropic 系 / OpenAI 系）共用本准入判断。
//
// 渠道把 billing_model_source 设为 response_model，等于把"按哪个模型计价"的一部分
// 决定权交给上游，因此准入条件必须收紧：
//   - 只在渠道显式开启该模式时生效，其余模式一律不看响应模型；
//   - 一次请求内出现过互相冲突的模型声明时不采纳（无法确定上游究竟服务了哪个模型）；
//   - 图片 / 视频 / 网页搜索 / 语音 / 搜索附加费这类按次按量计费的请求不采纳：它们按张、
//     按秒、按次定价，与本模式的 token 定价准入检查不是同一套价格表，混用会让一个只验过
//     token 价的模型名去决定媒体单价。新增按次计费形态时必须同步扩这个入参。
//
// 调用方还必须额外满足两条：模型能被价格表确定性识别（见
// hasIdentifiedResponseModelPricing / hasIdentifiedOpenAIResponsePricing），以及通过
// responseModelBillingAdoptable 的成本准入。
func responseModelBillingDeclaration(source, responseModel string, conflict, mediaBilled bool) string {
	if source != BillingModelSourceResponse || conflict || mediaBilled {
		return ""
	}
	return strings.TrimSpace(responseModel)
}

// responseModelBillingAdoptable 判定按响应模型重算出的成本能否取代基线成本。
// 三条不变式，任一不满足都必须沿用基线（即开启本模式前的既有行为）：
//
//  1. 不得更贵——上游声明永远不能抬高用户费用；epsilon 吸收两次计算之间的浮点末位误差。
//  2. 不得把一笔本应计费的请求归零。价格表里存在把 token 价显式写成 0 的条目
//     （TokenPricingAbsent 只在 input/output 价**都缺失**时才为真，显式 0 算"有价"因而
//     能通过确定性识别那道门），放任归零等于让上游自报一个免费模型名就能白嫖。
//     基线本身就是 0 时不受影响，采纳与否都不改变金额。
//  3. 不得把计费从管理员显式配置的渠道定价切到全局价格表。渠道定价查表只做精确键与
//     前缀通配、**不剥日期后缀**，而全局价格表的确定性识别**会剥** 8 位日期后缀；上游
//     普遍自报带日期的模型 ID（如 claude-opus-4-5-20251101），若允许跨源比较，渠道加价
//     会被这类自报名字静默绕过。管理员若确实想让降级目标享受折扣，为它显式配一条渠道
//     定价即可——那是一次可审计的显式授权。
func responseModelBillingAdoptable(baseline, response *CostBreakdown, baselineChannelPriced, responseChannelPriced bool) bool {
	if baseline == nil || response == nil {
		return false
	}
	if response.TotalCost > baseline.TotalCost+responseModelBillingCostEpsilon {
		return false
	}
	if response.TotalCost <= 0 && baseline.TotalCost > 0 {
		return false
	}
	return !baselineChannelPriced || responseChannelPriced
}

// logResponseModelBillingApplied 记录一次实际生效的响应模型计费切换。
// 本模式下的少收由上游声明驱动，必须留下可审计痕迹；计费基准未变时不记录，避免刷屏。
func logResponseModelBillingApplied(component string, account *Account, requestID, baselineModel, responseModel string, baselineCost, responseCost *CostBreakdown) {
	baselineModel = strings.TrimSpace(baselineModel)
	responseModel = strings.TrimSpace(responseModel)
	if strings.EqualFold(baselineModel, responseModel) {
		return
	}
	attrs := []any{
		"component", component,
		"request_id", strings.TrimSpace(requestID),
		"baseline_model", baselineModel,
		"response_model", responseModel,
	}
	if baselineCost != nil && responseCost != nil {
		attrs = append(attrs, "baseline_cost", baselineCost.TotalCost, "billed_cost", responseCost.TotalCost)
	}
	if account != nil {
		attrs = append(attrs, "platform", account.Platform, "account_id", account.ID)
	}
	slog.Info("billing.response_model_applied", attrs...)
}

func (s *GatewayService) hasIdentifiedResponseModelPricing(ctx context.Context, model string, apiKey *APIKey) (bool, bool) {
	if strings.TrimSpace(model) == "" {
		return false, false
	}
	if s.resolveChannelPricing(ctx, model, apiKey) != nil {
		return true, true
	}
	return s.billingService != nil && s.billingService.HasIdentifiedTokenPricing(model), false
}

func (s *OpenAIGatewayService) hasIdentifiedOpenAIResponsePricing(ctx context.Context, model string, apiKey *APIKey) (bool, bool) {
	if strings.TrimSpace(model) == "" {
		return false, false
	}
	if s.resolveOpenAIChannelPricing(ctx, model, apiKey) != nil {
		return true, true
	}
	return s.billingService != nil && s.billingService.HasIdentifiedTokenPricing(model), false
}
