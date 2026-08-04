package repository

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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

func NewGrokOAuthClient() service.GrokOAuthClient {
	return &grokOAuthClient{tokenURL: xai.EffectiveTokenURL()}
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
