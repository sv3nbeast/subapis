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
	kiroJSONContentType                 = "application/json"
	kiroGenerateAssistantResponseTarget = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"
	kiroGenerateAssistantResponsePath   = "/generateAssistantResponse"
	kiroKRSEndpointURL                  = "https://runtime.us-east-1.kiro.dev/generateAssistantResponse"
	kiroBuilderIDProfileARN             = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
	kiroSocialProfileARN                = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
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

func kiroIsPlaceholderProfileARN(arn string) bool {
	return strings.TrimSpace(arn) == kiroBuilderIDProfileARN
}

func kiroDefaultProfileARN(account *Account) string {
	if account != nil && strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_method")), "social") {
		return kiroSocialProfileARN
	}
	return kiroBuilderIDProfileARN
}

func kiroResolveProfileArnForKRS(account *Account) string {
	if account == nil {
		return ""
	}
	if arn := strings.TrimSpace(account.GetCredential("profile_arn")); arn != "" {
		return arn
	}
	return kiroDefaultProfileARN(account)
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

func buildKiroClientRequestID(resp *http.Response) string {
	if requestID := buildKiroRequestID(resp); requestID != "" {
		return requestID
	}
	return kiropkg.NewClaudeRequestID()
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
	regions := kiroAPIRegionCandidates(account)
	if len(regions) == 0 {
		return kiroDefaultRegion
	}
	return regions[0]
}

// kiroAPIRegionCandidates returns regions in descending confidence order.
// api_region is an explicit administrator override. A resolved profile ARN is
// the next-best source because CodeWhisperer profiles are regional. IDC token
// responses may omit profileArn, so the OIDC region must be tried before the
// historical us-east-1 default during profile discovery.
func kiroAPIRegionCandidates(account *Account) []string {
	regions := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(region string) {
		region = strings.TrimSpace(region)
		if region == "" {
			return
		}
		key := strings.ToLower(region)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		regions = append(regions, region)
	}

	if account != nil {
		add(account.GetCredential("api_region"))
		add(kiroRegionFromProfileArn(account.GetCredential("profile_arn")))
		add(account.GetCredential("region"))
	}
	add(kiroDefaultRegion)
	return regions
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

func hasExplicitKiroEndpointPreference(account *Account) bool {
	if account == nil {
		return false
	}
	for _, key := range []string{"kiro_wire_mode", "wire_mode"} {
		switch strings.ToLower(strings.TrimSpace(account.GetCredential(key))) {
		case "cli", "kiro_cli", "kiro-cli":
			return true
		}
	}
	switch strings.ToLower(strings.TrimSpace(account.GetCredential("kiro_endpoint_mode"))) {
	case "runtime", "kiro_runtime":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(account.GetCredential("preferred_endpoint"))) {
	case "runtime", "kiro_runtime", "kiro", "kiroide", "kiro_ide", "ide", "codewhisperer", "cw", "amazonq", "amazon_q", "q":
		return true
	}
	return false
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
	if account.Type == AccountTypeAPIKey {
		req.Header["TokenType"] = []string{"API_KEY"}
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_method")), "external_idp") {
		req.Header.Set("TokenType", "EXTERNAL_IDP")
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("provider")), "Internal") {
		req.Header.Set("redirect-for-internal", "true")
	}
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

	req.Header.Set("Content-Type", kiroJSONContentType)
	if target != "" {
		req.Header.Set("X-Amz-Target", target)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer "+token)
	if isKiroRuntimeRequestURL(req.URL) {
		req.Header.Set("User-Agent", kiropkg.BuildKiroIDERuntimeUserAgent(accountKey, machineID))
	} else {
		req.Header.Set("User-Agent", kiropkg.BuildRuntimeUserAgent(accountKey, machineID))
	}
	req.Header.Set("X-Amz-User-Agent", kiropkg.BuildRuntimeAmzUserAgent(accountKey, machineID))
	req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	if req.URL != nil && req.URL.Host != "" {
		req.Host = req.URL.Host
	}
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
	if account != nil && isKiroRuntimeRequestURL(req.URL) {
		profileArn := kiroResolveProfileArnForKRS(account)
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

func isKiroRuntimeRequestURL(u *url.URL) bool {
	return u != nil && strings.HasPrefix(strings.ToLower(u.Host), "runtime.") && strings.HasSuffix(strings.ToLower(u.Host), ".kiro.dev")
}
