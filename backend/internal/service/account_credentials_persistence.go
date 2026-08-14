package service

import (
	"context"
	"fmt"
	"log/slog"
)

type accountCredentialsUpdater interface {
	UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error
}

func persistAccountCredentials(ctx context.Context, repo AccountRepository, account *Account, credentials map[string]any) (err error) {
	// A few lightweight repository adapters embed AccountRepository to implement
	// only the state methods they need. Guard the legacy Update fallback so a nil
	// embedded interface cannot turn an upstream auth error into a process panic.
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("persist account credentials: %v", recovered)
		}
	}()
	if repo == nil || account == nil {
		return nil
	}

	// 安全不变量:spark 影子账号恒不持凭据(凭据透传母账号)。这是凭据写入的唯一汇聚点
	// (token 刷新 / 订阅补全 / CRS 创建后刷新等全部经此),在此对影子早返 no-op 是
	// defense-in-depth——即便某条上游路径漏判,也不会把凭据落到影子行(外审第6轮 P1)。
	if account.IsCredentialShadow() {
		slog.Warn("skip persisting credentials to spark shadow account",
			"account_id", account.ID, "parent_id", *account.ParentAccountID)
		return nil
	}
	if account.HasAnthropicStableCanaryManagedFields() && !AnthropicStableCanaryRefreshAuthorized(ctx, account.ID) {
		return ErrAnthropicStableCanaryOutboundBlocked
	}
	if account.HasAnthropicStableIdentityManagedFields() && !AnthropicStableCanaryRefreshAuthorized(ctx, account.ID) {
		return ErrAnthropicStableIdentityOutboundBlocked
	}

	account.Credentials = shallowCopyMap(credentials)
	if updater, ok := any(repo).(accountCredentialsUpdater); ok {
		return updater.UpdateCredentials(ctx, account.ID, account.Credentials)
	}
	return repo.Update(ctx, account)
}

func cloneCredentials(in map[string]any) map[string]any {
	return shallowCopyMap(in)
}

// sparkShadowAllowedCredentialKeys 是 spark 影子账号唯一可写的凭据键集合(仅模型映射)。
// 校验(isAllowed)与 sanitize 共用此单一来源,避免两处独立硬编码列表漂移。
var sparkShadowAllowedCredentialKeys = map[string]struct{}{
	"model_mapping":         {},
	"compact_model_mapping": {},
}

func isAllowedSparkShadowCredentialsUpdate(credentials map[string]any) bool {
	if credentials == nil {
		return true
	}
	for key := range credentials {
		if _, ok := sparkShadowAllowedCredentialKeys[key]; !ok {
			return false
		}
	}
	return true
}

func sanitizeSparkShadowCredentials(credentials map[string]any) map[string]any {
	if len(credentials) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(sparkShadowAllowedCredentialKeys))
	for key := range sparkShadowAllowedCredentialKeys {
		if value, ok := credentials[key]; ok && value != nil {
			out[key] = value
		}
	}
	return out
}
