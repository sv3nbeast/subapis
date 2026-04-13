package service

import (
	"context"
	"strings"
	"time"
)

func normalizeAntigravityModelName(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	normalized = strings.TrimPrefix(normalized, "models/")
	return normalized
}

// resolveAntigravityModelKey 根据请求的模型名解析限流 key
// 返回空字符串表示无法解析
func resolveAntigravityModelKey(requestedModel string) string {
	return normalizeAntigravityModelName(requestedModel)
}

func shouldAllowCreditsRateLimitBypass(ctx context.Context, account *Account, requestedModel string) bool {
	if account == nil || account.Platform != PlatformAntigravity {
		return false
	}
	if !account.IsOveragesEnabled() || account.isCreditsExhausted() {
		return false
	}
	modelKey := resolveCreditsOveragesModelKey(ctx, account, "", requestedModel)
	return !isClaudeModelFamily(modelKey) && !isClaudeModelFamily(requestedModel)
}

func isClaudeModelFamily(model string) bool {
	return strings.HasPrefix(normalizeAntigravityModelName(model), "claude-")
}

// IsSchedulableForModel 结合模型级限流判断是否可调度。
// 保持旧签名以兼容既有调用方；默认使用 context.Background()。
func (a *Account) IsSchedulableForModel(requestedModel string) bool {
	return a.IsSchedulableForModelWithContext(context.Background(), requestedModel)
}

func (a *Account) IsSchedulableForModelWithContext(ctx context.Context, requestedModel string) bool {
	if a == nil {
		return false
	}
	if !a.IsSchedulable() {
		return false
	}
	if a.isModelRateLimitedWithContext(ctx, requestedModel) {
		// 非 Claude 模型允许通过 AI Credits 绕过模型级限流；Claude 保持严格隔离。
		if shouldAllowCreditsRateLimitBypass(ctx, a, requestedModel) {
			return true
		}
		return false
	}
	if a.isModelCapacityCoolingDownWithContext(ctx, requestedModel) {
		return false
	}
	return true
}

// GetRateLimitRemainingTime 获取限流剩余时间（模型级限流）
// 返回 0 表示未限流或已过期
func (a *Account) GetRateLimitRemainingTime(requestedModel string) time.Duration {
	return a.GetRateLimitRemainingTimeWithContext(context.Background(), requestedModel)
}

// GetRateLimitRemainingTimeWithContext 获取限流剩余时间（模型级限流）
// 返回 0 表示未限流或已过期
func (a *Account) GetRateLimitRemainingTimeWithContext(ctx context.Context, requestedModel string) time.Duration {
	if a == nil {
		return 0
	}
	remaining := a.GetModelRateLimitRemainingTimeWithContext(ctx, requestedModel)
	if capacityRemaining := a.GetModelCapacityCooldownRemainingTimeWithContext(ctx, requestedModel); capacityRemaining > remaining {
		return capacityRemaining
	}
	return remaining
}
