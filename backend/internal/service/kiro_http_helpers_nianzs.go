// Source-faithful, namespaced integration of kiro_http_helpers.go from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.
// Only package identifiers and the kiro package import are rewritten so the
// legacy engine remains available for an immediate rollback.

package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/google/uuid"
)

// Kiro 默认 profileArn 常量（与 kiro.rs-admin 保持一致）。
// BuilderID 占位符：纯 BuilderID 账号没有真实 profile，上游 IDE 发送此占位符。
// Social 共享 ARN：Social 登录账号使用此共享 ARN。
const (
	nianzsKiroBuilderIDProfileARN = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
	nianzsKiroSocialProfileARN    = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
)

// nianzsKiroIsPlaceholderProfileARN 判断给定 ARN 是否为 BuilderID 占位符（非真实可用的 profile）。
func nianzsKiroIsPlaceholderProfileARN(arn string) bool {
	return arn == nianzsKiroBuilderIDProfileARN
}

// nianzsKiroIsSocialLogin 判断账号是否为 Social 登录方式。
func nianzsKiroIsSocialLogin(account *Account) bool {
	if account == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_method")), "social")
}

// nianzsKiroDefaultProfileARN 返回凭据缺少显式 profileArn 时应使用的默认 ARN：
// Social 登录 → nianzsKiroSocialProfileARN，其余（BuilderID 等）→ nianzsKiroBuilderIDProfileARN。
func nianzsKiroDefaultProfileARN(account *Account) string {
	if nianzsKiroIsSocialLogin(account) {
		return nianzsKiroSocialProfileARN
	}
	return nianzsKiroBuilderIDProfileARN
}

func nianzsBuildKiroAccountKey(account *Account) string {
	if account == nil {
		return ""
	}
	return nianzskiro.BuildAccountKey(
		account.GetCredential("client_id"),
		account.GetCredential("client_id_hash"),
		account.GetCredential("refresh_token"),
		account.GetCredential("profile_arn"),
		account.ID,
	)
}

func nianzsBuildKiroMachineID(account *Account) string {
	if account == nil {
		return nianzskiro.BuildMachineID("", "", "account:nil")
	}
	for _, key := range []string{"machine_id", "machineId"} {
		if machineID, ok := nianzskiro.NormalizeMachineID(account.GetCredential(key)); ok {
			return machineID
		}
	}
	fallbackKey := nianzsBuildKiroMachineIDFallbackKey(account)
	if account.Type == AccountTypeAPIKey {
		return nianzskiro.BuildMachineID("", nianzsFirstKiroCredential(account, "kiro_api_key", "kiroApiKey", "api_key"), fallbackKey)
	}
	return nianzskiro.BuildMachineID(account.GetCredential("refresh_token"), "", fallbackKey)
}

func nianzsFirstKiroCredential(account *Account, keys ...string) string {
	if account == nil {
		return ""
	}
	for _, key := range keys {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			return value
		}
	}
	return ""
}

func nianzsBuildKiroMachineIDFallbackKey(account *Account) string {
	if account == nil {
		return "account:nil"
	}
	if account.ID > 0 {
		return fmt.Sprintf("account:%d", account.ID)
	}
	for _, key := range []string{"client_id", "profile_arn"} {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			return key + ":" + value
		}
	}
	if name := strings.TrimSpace(account.Name); name != "" {
		return "name:" + name
	}
	return "account:unknown"
}

func nianzsBuildKiroRequestID(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	if requestID := strings.TrimSpace(resp.Header.Get("x-request-id")); requestID != "" {
		return requestID
	}
	if requestID := strings.TrimSpace(resp.Header.Get("x-amzn-requestid")); requestID != "" {
		return requestID
	}
	return strings.TrimSpace(resp.Header.Get("x-amz-request-id"))
}

func nianzsIsKiroSuspendedBody(respBody []byte) bool {
	body := string(respBody)
	return strings.Contains(body, "SUSPENDED") || strings.Contains(body, "TEMPORARILY_SUSPENDED")
}

func nianzsIsKiroTokenErrorBody(respBody []byte) bool {
	lower := strings.ToLower(string(respBody))
	return strings.Contains(lower, "token") ||
		strings.Contains(lower, "expired") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "unauthorized")
}

func nianzsKiroProxyURL(account *Account) string {
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		return account.Proxy.URL()
	}
	return ""
}

// nianzsIsKiroDirectModeAccount 判断账号是否走 Kiro 直连 AWS 模式。
// - OAuth 账号:直连 AWS(q.{region}.amazonaws.com 或 KRS),走 forwardKiroMessagesNianzs。
// - API Key 账号:
//   - base_url 为空 → 直连 AWS(ksk_ + tokentype: API_KEY),走 forwardKiroMessagesNianzs。
//   - base_url 非空 → 外部 Anthropic 兼容中转,返回 false,落回通用 buildUpstreamRequest
//     反代路径(请求 {base_url}/v1/messages,发 x-api-key),作为分组兜底/灾备账号。
func nianzsIsKiroDirectModeAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformKiro {
		return false
	}
	if account.Type == AccountTypeOAuth {
		return true
	}
	if account.Type == AccountTypeAPIKey {
		return strings.TrimSpace(account.GetCredential("base_url")) == ""
	}
	return false
}

func nianzsKiroAPIRegion(account *Account) string {
	if account == nil {
		return nianzsKiroDefaultRegion
	}
	region := strings.TrimSpace(account.GetCredential("api_region"))
	if region == "" {
		region = nianzsKiroDefaultRegion
	}
	return region
}

func nianzsApplyKiroConditionalHeaders(req *http.Request, account *Account) {
	if req == nil || account == nil {
		return
	}
	if account.Type == AccountTypeAPIKey {
		req.Header["TokenType"] = []string{"API_KEY"}
		return
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_method")), "external_idp") {
		req.Header.Set("TokenType", "EXTERNAL_IDP")
	}
}

func nianzsResolveKiroPayloadProfileArn(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential("profile_arn"))
}

// nianzsKiroResolveProfileArnForKRS 返回 KRS endpoint 所需的 profileArn。
// KRS endpoint（runtime.us-east-1.kiro.dev）强制要求 profileArn，
// 凭据无值时 fallback 到默认 ARN（Social → Social ARN，其余 → BuilderID 占位符）。
func nianzsKiroResolveProfileArnForKRS(account *Account) string {
	arn := nianzsResolveKiroPayloadProfileArn(account)
	if arn != "" {
		return arn
	}
	return nianzsKiroDefaultProfileARN(account)
}

func nianzsNewKiroJSONRequest(ctx context.Context, endpointURL string, payload []byte, token, accountKey, machineID, amzTarget string, account *Account) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer "+token)
	// KRS endpoint 用 Kiro IDE 实际 UA 格式 ("KiroIDE <version> <machineId>")；
	// AWS Q endpoint 继续用 AWS SDK 风格 UA。按 URL 区分以避免再加一个参数。
	if endpointURL == nianzsKiroKRSEndpointURL {
		req.Header.Set("User-Agent", nianzskiro.BuildKiroIDERuntimeUserAgent(accountKey, machineID))
	} else {
		req.Header.Set("User-Agent", nianzskiro.BuildRuntimeUserAgent(accountKey, machineID))
	}
	req.Header.Set("X-Amz-User-Agent", nianzskiro.BuildRuntimeAmzUserAgent(accountKey, machineID))
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
	req.Header.Set("Amz-Sdk-Invocation-Id", uuid.NewString())
	if amzTarget != "" {
		req.Header.Set("X-Amz-Target", amzTarget)
	}
	if account != nil {
		profileArn := nianzsResolveKiroPayloadProfileArn(account)
		if profileArn == "" && endpointURL == nianzsKiroKRSEndpointURL {
			profileArn = nianzsKiroResolveProfileArnForKRS(account)
		}
		if profileArn != "" {
			req.Header.Set("x-amzn-kiro-profile-arn", profileArn)
		}
	}
	nianzsApplyKiroConditionalHeaders(req, account)
	return req, nil
}
