package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const grokDefaultAccessTokenTTL = 6 * time.Hour

type GrokOAuthService struct {
	sessionStore *xai.SessionStore
	proxyRepo    ProxyRepository
	oauthClient  GrokOAuthClient
}

func NewGrokOAuthService(proxyRepo ProxyRepository, oauthClient GrokOAuthClient) *GrokOAuthService {
	return &GrokOAuthService{
		sessionStore: xai.NewSessionStore(),
		proxyRepo:    proxyRepo,
		oauthClient:  oauthClient,
	}
}

type GrokAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

func (s *GrokOAuthService) GenerateAuthURL(ctx context.Context, proxyID *int64, redirectURI string) (*GrokAuthURLResult, error) {
	state, err := xai.GenerateState()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_STATE_FAILED", "failed to generate state: %v", err)
	}
	nonce, err := xai.GenerateNonce()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_NONCE_FAILED", "failed to generate nonce: %v", err)
	}
	codeVerifier, err := xai.GenerateCodeVerifier()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_VERIFIER_FAILED", "failed to generate code verifier: %v", err)
	}
	sessionID, err := xai.GenerateSessionID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "GROK_OAUTH_SESSION_FAILED", "failed to generate session ID: %v", err)
	}

	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	redirectURI = xai.EffectiveRedirectURI(redirectURI)
	codeChallenge := xai.GenerateCodeChallenge(codeVerifier)

	authURL, err := xai.BuildAuthorizationURL(state, codeChallenge, redirectURI, nonce)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "GROK_OAUTH_INVALID_AUTHORIZE_URL", "%v", err)
	}

	s.sessionStore.Set(sessionID, &xai.OAuthSession{
		State:         state,
		CodeVerifier:  codeVerifier,
		CodeChallenge: codeChallenge,
		ClientID:      xai.EffectiveClientID(),
		Scope:         xai.EffectiveScope(),
		ProxyURL:      proxyURL,
		RedirectURI:   redirectURI,
		CreatedAt:     time.Now(),
	})

	return &GrokAuthURLResult{
		AuthURL:   authURL,
		SessionID: sessionID,
		State:     state,
	}, nil
}

type GrokExchangeCodeInput struct {
	SessionID   string
	Code        string
	State       string
	RedirectURI string
	ProxyID     *int64
}

type GrokTokenInfo struct {
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token,omitempty"`
	IDToken           string `json:"id_token,omitempty"`
	TokenType         string `json:"token_type,omitempty"`
	ExpiresIn         int64  `json:"expires_in"`
	ExpiresAt         int64  `json:"expires_at"`
	ClientID          string `json:"client_id,omitempty"`
	Scope             string `json:"scope,omitempty"`
	Email             string `json:"email,omitempty"`
	UserID            string `json:"user_id,omitempty"`
	TeamID            string `json:"team_id,omitempty"`
	IdentityKey       string `json:"identity_key,omitempty"`
	SubscriptionTier  string `json:"subscription_tier,omitempty"`
	EntitlementStatus string `json:"entitlement_status,omitempty"`
	BaseURL           string `json:"base_url,omitempty"`
}

func (s *GrokOAuthService) ExchangeCode(ctx context.Context, input *GrokExchangeCodeInput) (*GrokTokenInfo, error) {
	if input == nil {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_INVALID_INPUT", "input is required")
	}
	session, ok := s.sessionStore.Get(input.SessionID)
	if !ok {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_SESSION_NOT_FOUND", "session not found or expired")
	}
	defer s.sessionStore.Delete(input.SessionID)

	parsed := xai.ParseAuthorizationInput(input.Code)
	code := strings.TrimSpace(parsed.Code)
	if code == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_CODE_REQUIRED", "authorization code is required")
	}
	state := strings.TrimSpace(input.State)
	if state == "" {
		state = strings.TrimSpace(parsed.State)
	}
	if parsed.RequiresState && state == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_STATE_REQUIRED", "oauth state is required for callback URLs")
	}
	if state != "" && subtle.ConstantTimeCompare([]byte(state), []byte(session.State)) != 1 {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_INVALID_STATE", "invalid oauth state")
	}

	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		var err error
		proxyURL, err = s.proxyURL(ctx, input.ProxyID)
		if err != nil {
			return nil, err
		}
	}
	redirectURI := session.RedirectURI
	if strings.TrimSpace(input.RedirectURI) != "" {
		redirectURI = input.RedirectURI
	}

	tokenResp, err := s.oauthClient.ExchangeCode(ctx, code, session.CodeVerifier, redirectURI, proxyURL, session.ClientID)
	if err != nil {
		return nil, err
	}
	return s.tokenInfoFromResponse(tokenResp, session.ClientID, nil), nil
}

func (s *GrokOAuthService) RefreshToken(ctx context.Context, refreshToken, proxyURL, clientID string) (*GrokTokenInfo, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_NO_REFRESH_TOKEN", "refresh_token is required")
	}
	tokenResp, err := s.oauthClient.RefreshToken(ctx, refreshToken, proxyURL, clientID)
	if err != nil {
		return nil, err
	}
	tokenInfo := s.tokenInfoFromResponse(tokenResp, clientID, nil)
	if tokenInfo.RefreshToken == "" {
		tokenInfo.RefreshToken = refreshToken
	}
	return tokenInfo, nil
}

func (s *GrokOAuthService) ValidateRefreshToken(ctx context.Context, refreshToken string, proxyID *int64) (*GrokTokenInfo, error) {
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	return s.RefreshToken(ctx, refreshToken, proxyURL, xai.EffectiveClientID())
}

func (s *GrokOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*GrokTokenInfo, error) {
	if account == nil || account.Platform != PlatformGrok {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_INVALID_ACCOUNT", "account is not a Grok account")
	}
	if account.Type != AccountTypeOAuth {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_INVALID_ACCOUNT_TYPE", "account is not an OAuth account")
	}

	proxyURL, err := s.proxyURL(ctx, account.ProxyID)
	if err != nil {
		return nil, err
	}
	refreshToken := account.GetCredential("refresh_token")
	if strings.TrimSpace(refreshToken) == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_NO_REFRESH_TOKEN", "no refresh token available")
	}

	clientID := account.GetCredential("client_id")
	tokenInfo, err := s.RefreshToken(ctx, refreshToken, proxyURL, clientID)
	if err != nil {
		return nil, err
	}
	tokenInfo.SubscriptionTier = account.GetCredential("subscription_tier")
	tokenInfo.EntitlementStatus = account.GetCredential("entitlement_status")
	return tokenInfo, nil
}

func (s *GrokOAuthService) BuildAccountCredentials(tokenInfo *GrokTokenInfo) map[string]any {
	if tokenInfo == nil {
		return nil
	}
	expiresAt := time.Unix(tokenInfo.ExpiresAt, 0).UTC().Format(time.RFC3339)
	creds := map[string]any{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   expiresAt,
	}
	if tokenInfo.RefreshToken != "" {
		creds["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.TokenType != "" {
		creds["token_type"] = tokenInfo.TokenType
	}
	if tokenInfo.IDToken != "" {
		creds["id_token"] = tokenInfo.IDToken
	}
	if tokenInfo.ClientID != "" {
		creds["client_id"] = tokenInfo.ClientID
	}
	if tokenInfo.Scope != "" {
		creds["scope"] = tokenInfo.Scope
	}
	if tokenInfo.Email != "" {
		creds["email"] = tokenInfo.Email
	}
	if tokenInfo.UserID != "" {
		creds["user_id"] = tokenInfo.UserID
	}
	if tokenInfo.TeamID != "" {
		creds["team_id"] = tokenInfo.TeamID
	}
	if tokenInfo.IdentityKey != "" {
		creds["identity_key"] = tokenInfo.IdentityKey
	}
	if tokenInfo.SubscriptionTier != "" {
		creds["subscription_tier"] = tokenInfo.SubscriptionTier
	}
	if tokenInfo.EntitlementStatus != "" {
		creds["entitlement_status"] = tokenInfo.EntitlementStatus
	}
	baseURL := strings.TrimSpace(tokenInfo.BaseURL)
	if baseURL == "" {
		baseURL = inferGrokBaseURL(tokenInfo.AccessToken)
	}
	creds["base_url"] = baseURL
	return EnrichGrokOAuthCredentials(creds)
}

func (s *GrokOAuthService) Stop() {
	s.sessionStore.Stop()
}

func (s *GrokOAuthService) tokenInfoFromResponse(tokenResp *xai.TokenResponse, clientID string, existing map[string]any) *GrokTokenInfo {
	now := time.Now()
	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = int64(grokDefaultAccessTokenTTL.Seconds())
	}
	info := &GrokTokenInfo{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		IDToken:      tokenResp.IDToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    expiresIn,
		ExpiresAt:    now.Add(time.Duration(expiresIn) * time.Second).Unix(),
		ClientID:     strings.TrimSpace(clientID),
		Scope:        tokenResp.Scope,
		BaseURL:      inferGrokBaseURL(tokenResp.AccessToken),
	}
	if info.ClientID == "" {
		info.ClientID = xai.EffectiveClientID()
	}
	if info.TokenType == "" {
		info.TokenType = "Bearer"
	}
	claims := parseGrokJWTIdentity(tokenResp.IDToken, tokenResp.AccessToken)
	info.Email = claims.Email
	info.UserID = claims.UserID
	info.TeamID = claims.TeamID
	if info.Email == "" && existing != nil {
		if email, _ := existing["email"].(string); email != "" {
			info.Email = email
		}
	}
	identityCredentials := map[string]any{
		"client_id": info.ClientID,
		"email":     info.Email,
		"user_id":   info.UserID,
		"team_id":   info.TeamID,
	}
	info.IdentityKey = GrokOAuthIdentityKey(identityCredentials)
	return info
}

func (s *GrokOAuthService) proxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if s.proxyRepo == nil {
		return "", infraerrors.New(http.StatusBadRequest, "GROK_OAUTH_PROXY_NOT_AVAILABLE", "proxy repository is not available")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadRequest, "GROK_OAUTH_PROXY_NOT_FOUND", "proxy not found: %v", err)
	}
	if proxy == nil {
		return "", nil
	}
	return proxy.URL(), nil
}

type grokJWTIdentity struct {
	Email  string
	UserID string
	TeamID string
}

func parseGrokJWTIdentity(tokens ...string) grokJWTIdentity {
	var identity grokJWTIdentity
	for _, token := range tokens {
		parts := strings.Split(strings.TrimSpace(token), ".")
		if len(parts) < 2 {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var claims map[string]any
		if json.Unmarshal(payload, &claims) != nil {
			continue
		}
		if identity.Email == "" {
			identity.Email = strings.TrimSpace(grokStringClaim(claims, "email"))
		}
		if identity.UserID == "" {
			identity.UserID = strings.TrimSpace(firstNonEmpty(
				grokStringClaim(claims, "sub"),
				grokStringClaim(claims, "user_id"),
				grokStringClaim(claims, "principal_id"),
			))
		}
		if identity.TeamID == "" {
			identity.TeamID = strings.TrimSpace(firstNonEmpty(
				grokStringClaim(claims, "team_id"),
				grokStringClaim(claims, "organization_id"),
			))
		}
	}
	return identity
}

func grokStringClaim(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return value
}

// EnrichGrokOAuthCredentials derives stable non-secret identity fields from
// imported JWTs. It makes both authorization-code and manual refresh-token
// imports idempotent without relying on rotating token values.
func EnrichGrokOAuthCredentials(credentials map[string]any) map[string]any {
	enriched := make(map[string]any, len(credentials)+4)
	for key, value := range credentials {
		enriched[key] = value
	}
	claims := parseGrokJWTIdentity(
		grokCredentialString(enriched, "id_token"),
		grokCredentialString(enriched, "access_token"),
	)
	for key, value := range map[string]string{
		"email": claims.Email, "user_id": claims.UserID, "team_id": claims.TeamID,
	} {
		if strings.TrimSpace(grokCredentialString(enriched, key)) == "" && strings.TrimSpace(value) != "" {
			enriched[key] = strings.TrimSpace(value)
		}
	}
	if identityKey := GrokOAuthIdentityKey(enriched); identityKey != "" {
		enriched["identity_key"] = identityKey
	}
	return enriched
}

// GrokOAuthIdentityKey returns a stable hash of the OAuth principal, team and
// client. Token values are deliberately excluded because xAI rotates them.
func GrokOAuthIdentityKey(credentials map[string]any) string {
	if credentials == nil {
		return ""
	}
	principal := strings.TrimSpace(grokCredentialString(credentials, "user_id"))
	if principal == "" {
		principal = strings.ToLower(strings.TrimSpace(grokCredentialString(credentials, "email")))
	}
	if principal == "" {
		return ""
	}
	clientID := strings.TrimSpace(grokCredentialString(credentials, "client_id"))
	if clientID == "" {
		clientID = xai.DefaultClientID
	}
	teamID := strings.TrimSpace(grokCredentialString(credentials, "team_id"))
	digest := sha256.Sum256([]byte("grok-oauth-v1\x00" + clientID + "\x00" + principal + "\x00" + teamID))
	return hex.EncodeToString(digest[:])
}

func SameGrokOAuthIdentity(left, right map[string]any) bool {
	if left == nil || right == nil {
		return false
	}
	left = EnrichGrokOAuthCredentials(left)
	right = EnrichGrokOAuthCredentials(right)
	if leftKey, rightKey := grokCredentialString(left, "identity_key"), grokCredentialString(right, "identity_key"); leftKey != "" && leftKey == rightKey {
		return true
	}
	if !grokIdentityFieldCompatible(left, right, "client_id") || !grokIdentityFieldCompatible(left, right, "team_id") {
		return false
	}
	leftUser, rightUser := grokCredentialString(left, "user_id"), grokCredentialString(right, "user_id")
	if leftUser != "" && rightUser != "" {
		return leftUser == rightUser
	}
	leftEmail := strings.ToLower(grokCredentialString(left, "email"))
	rightEmail := strings.ToLower(grokCredentialString(right, "email"))
	return leftEmail != "" && leftEmail == rightEmail
}

func grokIdentityFieldCompatible(left, right map[string]any, key string) bool {
	leftValue, rightValue := grokCredentialString(left, key), grokCredentialString(right, key)
	return leftValue == "" || rightValue == "" || leftValue == rightValue
}

func grokCredentialString(credentials map[string]any, key string) string {
	value, _ := credentials[key].(string)
	return strings.TrimSpace(value)
}

// inferGrokBaseURL distinguishes xAI API OAuth tokens from Grok CLI tokens.
// API tokens currently include a tier claim, while free CLI tokens do not.
// Opaque or malformed tokens keep the historical API default.
func inferGrokBaseURL(accessToken string) string {
	parts := strings.Split(strings.TrimSpace(accessToken), ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return xai.DefaultBaseURL
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return xai.DefaultBaseURL
	}
	var header map[string]json.RawMessage
	if err := json.Unmarshal(headerBytes, &header); err != nil || len(header) == 0 {
		return xai.DefaultBaseURL
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return xai.DefaultBaseURL
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payloadBytes, &claims); err != nil || claims == nil {
		return xai.DefaultBaseURL
	}
	if _, hasTier := claims["tier"]; hasTier {
		return xai.DefaultBaseURL
	}
	return xai.DefaultCLIBaseURL
}

// PreserveGrokOAuthRoutingCredentials prevents reauthorization from deleting
// routing configuration that is not part of an OAuth token response.
func PreserveGrokOAuthRoutingCredentials(account *Account, incoming map[string]any) map[string]any {
	if account == nil || account.Platform != PlatformGrok {
		return incoming
	}

	merged := make(map[string]any, len(incoming)+2)
	for key, value := range incoming {
		merged[key] = value
	}
	for _, key := range []string{"base_url", "model_mapping"} {
		if _, exists := merged[key]; exists {
			continue
		}
		if value, exists := account.Credentials[key]; exists {
			merged[key] = value
		}
	}
	if _, exists := merged["base_url"]; !exists {
		accessToken, _ := merged["access_token"].(string)
		merged["base_url"] = inferGrokBaseURL(accessToken)
	}
	return merged
}
