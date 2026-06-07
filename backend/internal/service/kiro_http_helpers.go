package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/google/uuid"
)

const (
	kiroAWSJSONContentType              = "application/x-amz-json-1.0"
	kiroJSONContentType                 = "application/json"
	kiroGenerateAssistantResponseTarget = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"
	kiroGenerateAssistantResponsePath   = "/generateAssistantResponse"
)

func buildKiroAccountKey(account *Account) string {
	if account == nil {
		return ""
	}
	return kiropkg.BuildAccountKey(
		account.GetCredential("client_id"),
		account.GetCredential("client_id_hash"),
		account.GetCredential("refresh_token"),
		account.GetCredential("profile_arn"),
		account.ID,
	)
}

func buildKiroMachineID(account *Account) string {
	if account == nil {
		return kiropkg.BuildMachineID("", "", "account:nil")
	}
	for _, key := range []string{"machine_id", "machineId"} {
		if machineID, ok := kiropkg.NormalizeMachineID(account.GetCredential(key)); ok {
			return machineID
		}
	}
	fallbackKey := buildKiroMachineIDFallbackKey(account)
	if account.Type == AccountTypeAPIKey {
		return kiropkg.BuildMachineID("", firstKiroCredential(account, "kiro_api_key", "kiroApiKey", "api_key"), fallbackKey)
	}
	return kiropkg.BuildMachineID(account.GetCredential("refresh_token"), "", fallbackKey)
}

func firstKiroCredential(account *Account, keys ...string) string {
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

func buildKiroMachineIDFallbackKey(account *Account) string {
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

func buildKiroRequestID(resp *http.Response) string {
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

func isKiroSuspendedBody(respBody []byte) bool {
	body := string(respBody)
	return strings.Contains(body, "SUSPENDED") || strings.Contains(body, "TEMPORARILY_SUSPENDED")
}

func isKiroTokenErrorBody(respBody []byte) bool {
	lower := strings.ToLower(string(respBody))
	return strings.Contains(lower, "token") ||
		strings.Contains(lower, "expired") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "unauthorized")
}

func kiroProxyURL(account *Account) string {
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		return account.Proxy.URL()
	}
	return ""
}

func kiroAPIRegion(account *Account) string {
	if account == nil {
		return kiroDefaultRegion
	}
	region := strings.TrimSpace(account.GetCredential("api_region"))
	if region == "" {
		region = kiroDefaultRegion
	}
	return region
}

func isKiroCLIWireMode(account *Account) bool {
	if account == nil {
		return false
	}
	for _, key := range []string{"kiro_wire_mode", "wire_mode"} {
		switch strings.ToLower(strings.TrimSpace(account.GetCredential(key))) {
		case "cli", "kiro_cli", "kiro-cli":
			return true
		}
	}
	if strings.TrimSpace(account.GetCredential("profile_arn")) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(account.GetCredential("kiro_endpoint_mode"))) {
	case "runtime", "kiro_runtime":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(account.GetCredential("preferred_endpoint"))) {
	case "runtime", "kiro_runtime":
		return true
	}
	return false
}

func isKiroRuntimeEndpointMode(account *Account) bool {
	if account == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(account.GetCredential("kiro_endpoint_mode"))) {
	case "runtime", "kiro_runtime":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(account.GetCredential("preferred_endpoint"))) {
	case "runtime", "kiro_runtime":
		return true
	}
	return isKiroCLIWireMode(account)
}

func kiroRuntimeAPIRegion(account *Account) string {
	if account == nil {
		return kiroDefaultRegion
	}
	if region := strings.TrimSpace(account.GetCredential("api_region")); region != "" {
		return region
	}
	if region := kiroRegionFromProfileArn(account.GetCredential("profile_arn")); region != "" {
		return region
	}
	return kiroDefaultRegion
}

func applyKiroConditionalHeaders(req *http.Request, account *Account) {
	if req == nil || account == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_method")), "external_idp") {
		req.Header.Set("TokenType", "EXTERNAL_IDP")
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("provider")), "Internal") {
		req.Header.Set("redirect-for-internal", "true")
	}
}

func resolveKiroPayloadProfileArn(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential("profile_arn"))
}

func newKiroJSONRequest(ctx context.Context, endpointURL string, payload []byte, token, accountKey, machineID, amzTarget string, account *Account) (*http.Request, error) {
	return newKiroJSONRequestWithAttempt(ctx, endpointURL, payload, token, accountKey, machineID, amzTarget, account, 1, 3)
}

func newKiroJSONRequestWithAttempt(ctx context.Context, endpointURL string, payload []byte, token, accountKey, machineID, amzTarget string, account *Account, attempt, maxAttempts int) (*http.Request, error) {
	return newKiroJSONRequestWithAttemptAndDefaultTarget(ctx, endpointURL, payload, token, accountKey, machineID, amzTarget, account, attempt, maxAttempts, true)
}

func newKiroJSONRequestWithExplicitTarget(ctx context.Context, endpointURL string, payload []byte, token, accountKey, machineID, amzTarget string, account *Account, attempt, maxAttempts int) (*http.Request, error) {
	return newKiroJSONRequestWithAttemptAndDefaultTarget(ctx, endpointURL, payload, token, accountKey, machineID, amzTarget, account, attempt, maxAttempts, false)
}

func newKiroJSONRequestWithAttemptAndDefaultTarget(ctx context.Context, endpointURL string, payload []byte, token, accountKey, machineID, amzTarget string, account *Account, attempt, maxAttempts int, defaultGenerateTarget bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(amzTarget)
	if defaultGenerateTarget && target == "" && isKiroGenerateAssistantResponseURL(req.URL) {
		target = kiroGenerateAssistantResponseTarget
	}

	if target != "" {
		req.Header.Set("Content-Type", kiroAWSJSONContentType)
		req.Header.Set("X-Amz-Target", target)
	} else {
		req.Header.Set("Content-Type", kiroJSONContentType)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", kiropkg.BuildRuntimeUserAgent(accountKey, machineID))
	req.Header.Set("X-Amz-User-Agent", kiropkg.BuildRuntimeAmzUserAgent(accountKey, machineID))
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	if isKiroCLIWireMode(account) {
		req.Header.Set("x-amzn-codewhisperer-optout", "false")
	} else {
		req.Header.Set("x-amzn-codewhisperer-optout", "true")
	}
	if attempt <= 0 {
		attempt = 1
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	req.Header.Set("Amz-Sdk-Request", fmt.Sprintf("attempt=%d; max=%d", attempt, maxAttempts))
	req.Header.Set("Amz-Sdk-Invocation-Id", uuid.NewString())
	if account != nil {
		profileArn := strings.TrimSpace(account.GetCredential("profile_arn"))
		if profileArn != "" {
			req.Header.Set("x-amzn-kiro-profile-arn", profileArn)
		}
	}
	applyKiroConditionalHeaders(req, account)
	return req, nil
}

func isKiroGenerateAssistantResponseURL(u *url.URL) bool {
	return u != nil && strings.HasSuffix(strings.TrimRight(u.Path, "/"), kiroGenerateAssistantResponsePath)
}
