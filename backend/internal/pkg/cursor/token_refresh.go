package cursor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	oauthTokenURL = BaseURLAPI + EndpointToken
	// exchangeTokenURL 是 Cursor CLI 登录体系使用的刷新端点：Bearer 上
	// refresh_token，空 JSON body。
	exchangeTokenURL = BaseURLAPI + "/auth/exchange_user_api_key"
)

type tokenRefreshRequest struct {
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	RefreshToken string `json:"refresh_token"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// TokenRefreshResult is a Cursor OAuth token rotation.
type TokenRefreshResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// ExpiresAt is when the new access token should be treated as stale.
func (r TokenRefreshResult) ExpiresAt(now time.Time) time.Time {
	if r.ExpiresIn > 0 {
		return now.Add(time.Duration(r.ExpiresIn) * time.Second)
	}
	if exp := AccessTokenExpiry(r.AccessToken); exp != nil {
		return *exp
	}
	return now.Add(time.Hour)
}

// RefreshSession exchanges a refresh token for a new Cursor session.
func RefreshSession(ctx context.Context, httpClient *http.Client, refreshToken string) (*TokenRefreshResult, error) {
	return refreshTokenImpl(ctx, httpClient, refreshToken)
}

// RefreshSessionViaProxy exchanges a refresh token through the given
// HTTP CONNECT / SOCKS5 proxy, using the shared transport pool.
func RefreshSessionViaProxy(ctx context.Context, refreshToken, proxyURL string) (*TokenRefreshResult, error) {
	httpClient, err := UnaryHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	return refreshTokenImpl(ctx, httpClient, refreshToken)
}

// refreshTokenImpl 依次尝试两个刷新端点。凭证有两种来源：浏览器登录拿到的
// CLI 体系 token 走 /auth/exchange_user_api_key，从 IDE storage.json 手工导入的
// 走 /oauth/token。先试前者，它明确失败时再退到后者，这样两种来源都能续期。
func refreshTokenImpl(ctx context.Context, httpClient *http.Client, refreshToken string) (*TokenRefreshResult, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("cursor: missing refresh token")
	}

	if httpClient == nil {
		var err error
		httpClient, err = UnaryHTTPClient("")
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, cursorUnaryTimeout)
	defer cancel()

	result, exchangeErr := refreshViaExchange(ctx, httpClient, refreshToken)
	if exchangeErr == nil {
		return result, nil
	}
	result, oauthErr := refreshViaOAuthToken(ctx, httpClient, refreshToken)
	if oauthErr == nil {
		return result, nil
	}
	return nil, fmt.Errorf("cursor: token refresh failed on both endpoints: %v; %v", exchangeErr, oauthErr)
}

// refreshViaExchange 走 CLI 登录体系：Authorization 带 refresh_token，body 为空对象。
func refreshViaExchange(ctx context.Context, httpClient *http.Client, refreshToken string) (*TokenRefreshResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeTokenURL, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("build exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+refreshToken)

	body, err := doRefreshRequest(httpClient, req, "exchange")
	if err != nil {
		return nil, err
	}
	// 该端点返回 camelCase 的 accessToken/refreshToken。
	var parsed TokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse exchange response: %w", err)
	}
	accessToken := strings.TrimSpace(parsed.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("empty access token in exchange response")
	}
	rotated := strings.TrimSpace(parsed.RefreshToken)
	if rotated == "" {
		// 该端点不轮换 refresh token，沿用原值。
		rotated = refreshToken
	}
	return &TokenRefreshResult{AccessToken: accessToken, RefreshToken: rotated}, nil
}

// refreshViaOAuthToken 走 IDE 体系的标准 OAuth refresh_token 授权。
func refreshViaOAuthToken(ctx context.Context, httpClient *http.Client, refreshToken string) (*TokenRefreshResult, error) {
	payload, err := json.Marshal(tokenRefreshRequest{
		GrantType:    "refresh_token",
		ClientID:     DefaultAuthClientID,
		RefreshToken: refreshToken,
	})
	if err != nil {
		return nil, fmt.Errorf("encode refresh request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	body, err := doRefreshRequest(httpClient, req, "oauth")
	if err != nil {
		return nil, err
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}
	if strings.TrimSpace(tr.AccessToken) == "" {
		return nil, fmt.Errorf("empty access token in refresh response")
	}
	return &TokenRefreshResult{
		AccessToken:  strings.TrimSpace(tr.AccessToken),
		RefreshToken: strings.TrimSpace(tr.RefreshToken),
		ExpiresIn:    tr.ExpiresIn,
	}, nil
}

func doRefreshRequest(httpClient *http.Client, req *http.Request, label string) ([]byte, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", label, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", label, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s status %d: %s", label, resp.StatusCode, truncateForError(body))
	}
	return body, nil
}

// AccessTokenExpiry reads exp from an unverified Cursor JWT.
// Tokens may be stored as "userId::jwt".
func AccessTokenExpiry(token string) *time.Time {
	token = strings.TrimSpace(token)
	if i := strings.Index(token, "::"); i >= 0 && i < len(token)-2 {
		token = strings.TrimSpace(token[i+2:])
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return nil
	}
	var claims struct {
		Exp json.Number `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == "" {
		return nil
	}
	sec, err := claims.Exp.Int64()
	if err != nil {
		f, ferr := claims.Exp.Float64()
		if ferr != nil {
			return nil
		}
		sec = int64(f)
	}
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0).UTC()
	return &t
}

func decodeJWTSegment(seg string) ([]byte, error) {
	if payload, err := base64.RawURLEncoding.DecodeString(seg); err == nil {
		return payload, nil
	}
	return base64.URLEncoding.DecodeString(seg)
}
