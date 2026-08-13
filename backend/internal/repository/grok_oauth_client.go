package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	sharedhttp "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/imroc/req/v3"
)

type grokOAuthClient struct {
	tokenURL string
}

const (
	accountsBaseURL      = "https://accounts.x.ai"
	loginRPCEndpoint     = accountsBaseURL + "/api/rpc"
	turnstileWebsiteURL  = accountsBaseURL
	turnstileWebsiteKey  = "0x4AAAAAAAhr9JGVDZbrZOo0"
	yesCaptchaCreateTask = "https://api.yescaptcha.com/createTask"
	yesCaptchaGetResult  = "https://api.yescaptcha.com/getTaskResult"
)

func NewGrokOAuthClient() service.GrokOAuthClient {
	// Fail closed: never fall back to an unvalidated EffectiveTokenURL (env can
	// point at an attacker host and steal code/refresh tokens).
	tokenURL, err := xai.ValidatedTokenURL()
	if err != nil || strings.TrimSpace(tokenURL) == "" {
		// Official allowlisted endpoint only — never EffectiveTokenURL() (raw env).
		tokenURL = xai.DefaultTokenURL
	}
	return &grokOAuthClient{tokenURL: tokenURL}
}

func (c *grokOAuthClient) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string) (*xai.TokenResponse, error) {
	client, err := createGrokReqClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = xai.EffectiveClientID()
	}

	formData := url.Values{}
	formData.Set("grant_type", "authorization_code")
	formData.Set("client_id", clientID)
	formData.Set("code", code)
	formData.Set("redirect_uri", xai.EffectiveRedirectURI(redirectURI))
	formData.Set("code_verifier", codeVerifier)

	var tokenResp xai.TokenResponse
	resp, err := client.R().
		SetContext(ctx).
		SetHeader("User-Agent", "sub2api-grok-oauth/1.0").
		SetHeader(xai.CLIClientVersionHdr, xai.CLIClientVersion).
		SetFormDataFromValues(formData).
		SetSuccessResult(&tokenResp).
		Post(c.tokenURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}
	if !resp.IsSuccessState() {
		return nil, grokOAuthStatusError("GROK_OAUTH_TOKEN_EXCHANGE_FAILED", "token exchange failed", resp)
	}
	return &tokenResp, nil
}

func (c *grokOAuthClient) StartDeviceAuthorization(ctx context.Context, proxyURL, clientID, scope string) (*xai.DeviceAuthorizationResponse, error) {
	client, err := createGrokReqClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_DEVICE_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}
	deviceURL, err := xai.ValidatedDeviceURL()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "GROK_DEVICE_URL_INVALID", "invalid device authorization URL: %v", err)
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = xai.EffectiveClientID()
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = xai.EffectiveScope()
	}

	formData := url.Values{
		"client_id": {clientID},
		"scope":     {scope},
		"referrer":  {"grok-build"},
	}
	var result xai.DeviceAuthorizationResponse
	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader(xai.CLIClientVersionHdr, xai.CLIClientVersion).
		SetHeader(xai.DeviceSurfaceHeader, xai.DeviceSurfaceUI).
		SetFormDataFromValues(formData).
		SetSuccessResult(&result).
		Post(deviceURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_DEVICE_REQUEST_FAILED", "request failed: %v", err)
	}
	if !resp.IsSuccessState() {
		return nil, grokOAuthStatusError("GROK_DEVICE_START_FAILED", "device authorization start failed", resp)
	}
	if strings.TrimSpace(result.DeviceCode) == "" || !validGrokDeviceUserCode(result.UserCode) || strings.TrimSpace(result.VerificationURI) == "" {
		return nil, infraerrors.New(http.StatusBadGateway, "GROK_DEVICE_RESPONSE_INVALID", "xAI device authorization response is incomplete")
	}
	if _, err := xai.ValidateOAuthEndpointURL(result.VerificationURI); err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_DEVICE_VERIFICATION_URL_INVALID", "xAI returned an invalid verification URL: %v", err)
	}
	if strings.TrimSpace(result.VerificationURIComplete) != "" {
		if _, err := xai.ValidateOAuthEndpointURL(result.VerificationURIComplete); err != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_DEVICE_VERIFICATION_URL_INVALID", "xAI returned an invalid complete verification URL: %v", err)
		}
	}
	if result.Interval <= 0 {
		result.Interval = 5
	}
	if result.ExpiresIn <= 0 {
		result.ExpiresIn = int(xai.SessionTTL.Seconds())
	}
	return &result, nil
}

func (c *grokOAuthClient) PollDeviceAuthorization(ctx context.Context, deviceCode, proxyURL, clientID string) (*xai.TokenResponse, error) {
	client, err := createGrokReqClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_DEVICE_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = xai.EffectiveClientID()
	}
	formData := url.Values{
		"grant_type":  {xai.DeviceGrantType},
		"client_id":   {clientID},
		"device_code": {strings.TrimSpace(deviceCode)},
	}
	var tokenResp xai.TokenResponse
	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader(xai.CLIClientVersionHdr, xai.CLIClientVersion).
		SetHeader(xai.DeviceSurfaceHeader, xai.DeviceSurfaceUI).
		SetFormDataFromValues(formData).
		SetSuccessResult(&tokenResp).
		Post(c.tokenURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_DEVICE_REQUEST_FAILED", "request failed: %v", err)
	}
	if !resp.IsSuccessState() {
		var payload struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal([]byte(resp.String()), &payload)
		code := strings.TrimSpace(payload.Error)
		if code == "authorization_pending" || code == "slow_down" || code == "access_denied" || code == "expired_token" {
			return nil, &xai.DeviceAuthorizationError{
				StatusCode:  resp.StatusCode,
				Code:        code,
				Description: payload.ErrorDescription,
				RetryAfter:  parseGrokOAuthRetryAfter(resp.Header.Get("Retry-After")),
			}
		}
		return nil, grokOAuthStatusError("GROK_DEVICE_POLL_FAILED", "device authorization polling failed", resp)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, infraerrors.New(http.StatusBadGateway, "GROK_DEVICE_TOKEN_INVALID", "xAI device token response did not contain an access token")
	}
	return &tokenResp, nil
}

func (c *grokOAuthClient) RefreshToken(ctx context.Context, refreshToken, proxyURL, clientID, principalType, principalID string) (*xai.TokenResponse, error) {
	client, err := createGrokReqClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = xai.EffectiveClientID()
	}

	formData := url.Values{}
	formData.Set("grant_type", "refresh_token")
	formData.Set("client_id", clientID)
	formData.Set("refresh_token", refreshToken)
	if principalType = strings.TrimSpace(principalType); principalType != "" {
		formData.Set("principal_type", principalType)
	}
	if principalID = strings.TrimSpace(principalID); principalID != "" {
		formData.Set("principal_id", principalID)
	}

	var tokenResp xai.TokenResponse
	resp, err := client.R().
		SetContext(ctx).
		SetHeader("User-Agent", "sub2api-grok-oauth/1.0").
		SetFormDataFromValues(formData).
		SetSuccessResult(&tokenResp).
		Post(c.tokenURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}
	if !resp.IsSuccessState() {
		return nil, grokOAuthStatusError("GROK_OAUTH_TOKEN_REFRESH_FAILED", "token refresh failed", resp)
	}
	return &tokenResp, nil
}

// LoginWithPassword authenticates against accounts.x.ai and returns an ephemeral SSO cookie.
// Password and SSO must never be written to account credentials or logs.
func (c *grokOAuthClient) LoginWithPassword(ctx context.Context, email, password, proxyURL string) (*service.GrokPasswordLoginResult, error) {
	turnstileToken, err := solveTurnstile(ctx)
	if err != nil {
		return nil, err
	}
	httpClient, err := createGrokHTTPClient(proxyURL, true)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}
	cookieSetterURL, err := createGrokPasswordSession(ctx, httpClient, strings.TrimSpace(email), password, turnstileToken)
	if err != nil {
		return nil, err
	}
	ssoToken, err := extractGrokSSOToken(ctx, httpClient, cookieSetterURL)
	if err != nil {
		return nil, err
	}
	return &service.GrokPasswordLoginResult{
		Email:    strings.TrimSpace(email),
		SSOToken: ssoToken,
	}, nil
}

func (c *grokOAuthClient) ConvertSSOToBuild(ctx context.Context, ssoToken, proxyURL string) (*xai.TokenResponse, error) {
	client, err := createGrokSSOHTTPClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "GROK_SSO_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, xai.SSOConversionTimeout)
	defer cancel()
	tokenResp, err := xai.ConvertSSOToBuild(requestCtx, ssoToken, &xai.SSODeviceOptions{HTTPClient: client})
	if err != nil {
		return nil, grokSSOConversionError(err)
	}
	return tokenResp, nil
}

func createGrokReqClient(proxyURL string) (*req.Client, error) {
	return getSharedReqClient(reqClientOptions{
		ProxyURL: proxyURL,
		Timeout:  60 * time.Second,
	})
}

func createGrokSSOHTTPClient(proxyURL string) (*http.Client, error) {
	client, err := sharedhttp.GetClient(sharedhttp.Options{
		ProxyURL:              proxyURL,
		Timeout:               xai.SSOConversionTimeout,
		ResponseHeaderTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone, nil
}

func grokSSOConversionError(err error) error {
	if errors.Is(err, xai.ErrSSOUnauthorized) {
		return infraerrors.New(http.StatusUnauthorized, "GROK_SSO_UNAUTHORIZED", "Grok Web SSO cookie is invalid or expired")
	}
	if errors.Is(err, xai.ErrSSOAuthorizationDenied) {
		return infraerrors.New(http.StatusForbidden, "GROK_SSO_AUTHORIZATION_DENIED", "xAI device authorization was denied or expired")
	}
	var statusErr xai.SSOHTTPError
	if errors.As(err, &statusErr) {
		statusCode := http.StatusBadGateway
		if statusErr.Status == http.StatusForbidden {
			statusCode = http.StatusForbidden
		}
		return infraerrors.Newf(statusCode, "GROK_SSO_UPSTREAM_FAILED", "xAI SSO conversion failed: %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return infraerrors.Newf(http.StatusGatewayTimeout, "GROK_SSO_TIMEOUT", "xAI SSO conversion timed out: %v", err)
	}
	return infraerrors.Newf(http.StatusBadGateway, "GROK_SSO_CONVERSION_FAILED", "xAI SSO conversion failed: %v", err)
}

func grokOAuthStatusError(code, message string, resp *req.Response) error {
	statusCode := http.StatusBadGateway
	errorCode := code
	upstreamStatus := 0
	body := ""
	if resp != nil {
		upstreamStatus = resp.StatusCode
		body = logredact.RedactText(resp.String())
		if resp.StatusCode == http.StatusForbidden && grokOAuthHasExplicitEntitlementDenial(body) {
			statusCode = http.StatusForbidden
			errorCode = "GROK_OAUTH_ENTITLEMENT_DENIED"
		}
	}
	return infraerrors.Newf(statusCode, errorCode, "%s: status %d, body: %s", message, upstreamStatus, body)
}

func grokOAuthHasExplicitEntitlementDenial(body string) bool {
	lower := strings.ToLower(body)
	// Billing exhaustion is recoverable. xAI may include a generic
	// access_denied code alongside the quota message, so it must win over the
	// entitlement marker during token refresh.
	for _, phrase := range []string{
		"spending limit",
		"run out of credits",
		"out of credits",
		"credits exhausted",
		"included free usage",
	} {
		if strings.Contains(lower, phrase) {
			return false
		}
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "").Replace(lower)
	for _, field := range []string{"error", "code", "reason"} {
		for _, value := range []string{"access_denied", "entitlement_denied", "subscription_required", "no_active_subscription"} {
			if strings.Contains(compact, `"`+field+`":"`+value+`"`) {
				return true
			}
		}
	}
	return strings.Contains(lower, "entitlement denied") ||
		strings.Contains(lower, "subscription required") ||
		strings.Contains(lower, "no active grok subscription")
}

func createGrokHTTPClient(proxyURL string, noRedirect bool) (*http.Client, error) {
	transport := &http.Transport{}
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	client := &http.Client{Timeout: 120 * time.Second, Transport: transport}
	if noRedirect {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client, nil
}

func solveTurnstile(ctx context.Context) (string, error) {
	clientKey := strings.TrimSpace(os.Getenv("YESCAPTCHA_CLIENT_KEY"))
	if clientKey == "" {
		clientKey = strings.TrimSpace(os.Getenv("YESCAPTCHA_API_KEY"))
	}
	if clientKey == "" {
		return "", infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_CAPTCHA_KEY_REQUIRED", "yescaptcha client key is required for Grok password authorization")
	}
	createBody, err := json.Marshal(map[string]any{
		"clientKey": clientKey,
		"task": map[string]any{
			"type":       "TurnstileTaskProxyless",
			"websiteURL": turnstileWebsiteURL,
			"websiteKey": turnstileWebsiteKey,
		},
	})
	if err != nil {
		return "", infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_CAPTCHA_FAILED", "encode captcha create request failed: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, yesCaptchaCreateTask, bytes.NewReader(createBody))
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "build captcha create request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "create captcha task failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var createResp struct {
		ErrorID          int    `json:"errorId"`
		TaskID           string `json:"taskId"`
		ErrorDescription string `json:"errorDescription"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "decode captcha create response failed: %v", err)
	}
	if createResp.ErrorID != 0 || strings.TrimSpace(createResp.TaskID) == "" {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "captcha create failed: %s", createResp.ErrorDescription)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
		body, err := json.Marshal(map[string]any{"clientKey": clientKey, "taskId": createResp.TaskID})
		if err != nil {
			return "", infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_CAPTCHA_FAILED", "encode captcha poll request failed: %v", err)
		}
		pollReq, err := http.NewRequestWithContext(ctx, http.MethodPost, yesCaptchaGetResult, bytes.NewReader(body))
		if err != nil {
			continue
		}
		pollReq.Header.Set("Content-Type", "application/json")
		pollRespRaw, err := http.DefaultClient.Do(pollReq)
		if err != nil {
			continue
		}
		var pollResp struct {
			ErrorID          int    `json:"errorId"`
			Status           string `json:"status"`
			ErrorDescription string `json:"errorDescription"`
			Solution         struct {
				Token string `json:"token"`
			} `json:"solution"`
		}
		err = json.NewDecoder(pollRespRaw.Body).Decode(&pollResp)
		_ = pollRespRaw.Body.Close()
		if err != nil {
			continue
		}
		if pollResp.ErrorID != 0 {
			return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_CAPTCHA_FAILED", "captcha poll failed: %s", pollResp.ErrorDescription)
		}
		if pollResp.Status == "ready" && strings.TrimSpace(pollResp.Solution.Token) != "" {
			return pollResp.Solution.Token, nil
		}
	}
	return "", infraerrors.New(http.StatusGatewayTimeout, "GROK_OAUTH_CAPTCHA_TIMEOUT", "captcha solve timed out")
}

func createGrokPasswordSession(ctx context.Context, client *http.Client, email, password, turnstileToken string) (string, error) {
	payload, err := json.Marshal(map[string]any{"rpc": "createSession", "req": map[string]any{"createSessionRequest": map[string]any{"credentials": map[string]any{"case": "emailAndPassword", "value": map[string]any{"email": email, "clearTextPassword": password}}}}, "turnstileToken": turnstileToken})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginRPCEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", accountsBaseURL)
	req.Header.Set("Referer", accountsBaseURL+"/sign-in?redirect=grok-com&email=true")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login returned status %d: %s", resp.StatusCode, logredact.RedactText(string(body)))
	}
	var result struct {
		CookieSetterURL string `json:"cookieSetterUrl"`
		Error           string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "decode password login response failed: %v", err)
	}
	if strings.TrimSpace(result.Error) != "" {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login error: %s", logredact.RedactText(result.Error))
	}
	if strings.TrimSpace(result.CookieSetterURL) == "" {
		return "", infraerrors.New(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login did not return cookieSetterUrl")
	}
	return result.CookieSetterURL, nil
}

func extractGrokSSOToken(ctx context.Context, client *http.Client, cookieSetterURL string) (string, error) {
	safeURL, err := validateGrokCookieSetterURL(cookieSetterURL)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "invalid cookie setter url: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, safeURL.String(), nil)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "build cookie setter request: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", accountsBaseURL+"/")
	resp, err := client.Do(req)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "follow cookie setter url failed: %v", err)
	}
	defer resp.Body.Close()
	for _, cookie := range resp.Header.Values("Set-Cookie") {
		if token, ok := strings.CutPrefix(cookie, "sso="); ok {
			if idx := strings.Index(token, ";"); idx >= 0 {
				token = token[:idx]
			}
			if strings.TrimSpace(token) != "" {
				return strings.TrimSpace(token), nil
			}
		}
	}
	return "", infraerrors.Newf(http.StatusBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "no sso cookie found in response (status=%d)", resp.StatusCode)
}

func validateGrokCookieSetterURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "accounts.x.ai") || parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, fmt.Errorf("url must use https://accounts.x.ai without authority overrides")
	}
	return parsed, nil
}

func validGrokDeviceUserCode(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, char := range value {
		if !((char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
			return false
		}
	}
	return true
}

func parseGrokOAuthRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(time.Now()) {
		return time.Until(retryAt)
	}
	return 0
}

var _ service.GrokOAuthClient = (*grokOAuthClient)(nil)
