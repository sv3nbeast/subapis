package kiro

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/google/uuid"
)

const (
	socialAuthPortalURL = "https://app.kiro.dev"
	socialAuthEndpoint  = "https://prod.us-east-1.auth.desktop.kiro.dev"
	defaultIDCRegion    = "us-east-1"
	BuilderIDStartURL   = "https://view.awsapps.com/start"
	sessionTTL          = 10 * time.Minute
	sessionCleanupEvery = 32
	sessionCleanupMin   = 32
)

var (
	socialAuthEndpointURL = socialAuthEndpoint
	oidcEndpointOverride  = ""
)

type SocialProvider string

const (
	SocialProviderGoogle SocialProvider = "Google"
	SocialProviderGitHub SocialProvider = "Github"
)

const (
	ProviderGoogle     = "Google"
	ProviderGithub     = "Github"
	ProviderBuilderId  = "BuilderId"
	ProviderEnterprise = "Enterprise"
)

func IsValidKiroProvider(p string) bool {
	switch strings.TrimSpace(p) {
	case ProviderGoogle, ProviderGithub, ProviderBuilderId, ProviderEnterprise:
		return true
	default:
		return false
	}
}

func resolveIDCProvider(startURL string) string {
	if strings.TrimSpace(startURL) == "" || strings.TrimSpace(startURL) == BuilderIDStartURL {
		return ProviderBuilderId
	}
	return ProviderEnterprise
}

func resolveIDCRefreshProvider(startURL string, provider ...string) string {
	switch strings.TrimSpace(firstNonEmpty(provider...)) {
	case ProviderBuilderId:
		return ProviderBuilderId
	case ProviderEnterprise:
		return ProviderEnterprise
	default:
		return resolveIDCProvider(startURL)
	}
}

func normalizeKiroExpiresAt(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("expiresAt is empty")
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	}
	for i, layout := range layouts {
		var (
			t   time.Time
			err error
		)
		if i >= 2 {
			t, err = time.ParseInLocation(layout, value, time.UTC)
		} else {
			t, err = time.Parse(layout, value)
		}
		if err == nil {
			return t.Local().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("invalid expiresAt format: %q", raw)
}

type AuthSession struct {
	State        string
	CodeVerifier string
	DeviceCode   string
	ProxyURL     string
	CreatedAt    time.Time
	AuthType     string
	Provider     string
	RedirectURI  string
	ClientID     string
	ClientSecret string
	Region       string
	StartURL     string
}

type SessionStore struct {
	mu       sync.RWMutex
	data     map[string]*AuthSession
	setCount uint64
}

func NewSessionStore() *SessionStore {
	return &SessionStore{data: make(map[string]*AuthSession)}
}

func (s *SessionStore) Get(id string) (*AuthSession, bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if ok && sessionExpired(session, now) {
		delete(s.data, id)
		return nil, false
	}
	return session, ok
}

func (s *SessionStore) Set(id string, session *AuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCount++
	if len(s.data) >= sessionCleanupMin && s.setCount%sessionCleanupEvery == 0 {
		s.pruneExpiredLocked(time.Now())
	}
	s.data[id] = session
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
}

func (s *SessionStore) pruneExpiredLocked(now time.Time) {
	for id, session := range s.data {
		if sessionExpired(session, now) {
			delete(s.data, id)
		}
	}
}

func sessionExpired(session *AuthSession, now time.Time) bool {
	if session == nil {
		return true
	}
	if session.CreatedAt.IsZero() {
		return true
	}
	return now.After(session.CreatedAt.Add(sessionTTL))
}

type TokenData struct {
	AccessToken   string `json:"accessToken"`
	RefreshToken  string `json:"refreshToken"`
	ProfileArn    string `json:"profileArn,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
	AuthMethod    string `json:"authMethod,omitempty"`
	Provider      string `json:"provider,omitempty"`
	ClientID      string `json:"clientId,omitempty"`
	ClientSecret  string `json:"clientSecret,omitempty"`
	ClientIDHash  string `json:"clientIdHash,omitempty"`
	Email         string `json:"email,omitempty"`
	StartURL      string `json:"startUrl,omitempty"`
	Region        string `json:"region,omitempty"`
	IssuerURL     string `json:"issuerUrl,omitempty"`
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`
	Scopes        string `json:"scopes,omitempty"`
}

func (t *TokenData) UnmarshalJSON(data []byte) error {
	type tokenDataFields struct {
		AccessToken        string `json:"accessToken"`
		AccessTokenSnake   string `json:"access_token"`
		RefreshToken       string `json:"refreshToken"`
		RefreshTokenSnake  string `json:"refresh_token"`
		ProfileArn         string `json:"profileArn"`
		ProfileArnSnake    string `json:"profile_arn"`
		ExpiresAt          string `json:"expiresAt"`
		ExpiresAtSnake     string `json:"expires_at"`
		Expired            string `json:"expired"`
		AuthMethod         string `json:"authMethod"`
		AuthMethodSnake    string `json:"auth_method"`
		Provider           string `json:"provider"`
		ClientID           string `json:"clientId"`
		ClientIDSnake      string `json:"client_id"`
		ClientSecret       string `json:"clientSecret"`
		ClientSecretSnake  string `json:"client_secret"`
		ClientIDHash       string `json:"clientIdHash"`
		ClientIDHashSnake  string `json:"client_id_hash"`
		Email              string `json:"email"`
		StartURL           string `json:"startUrl"`
		StartURLSnake      string `json:"start_url"`
		Region             string `json:"region"`
		IssuerURL          string `json:"issuerUrl"`
		IssuerURLSnake     string `json:"issuer_url"`
		TokenEndpoint      string `json:"tokenEndpoint"`
		TokenEndpointSnake string `json:"token_endpoint"`
		Scopes             string `json:"scopes"`
	}
	var fields tokenDataFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	t.AccessToken = firstNonEmpty(fields.AccessToken, fields.AccessTokenSnake)
	t.RefreshToken = firstNonEmpty(fields.RefreshToken, fields.RefreshTokenSnake)
	t.ProfileArn = firstNonEmpty(fields.ProfileArn, fields.ProfileArnSnake)
	t.ExpiresAt = firstNonEmpty(fields.ExpiresAt, fields.ExpiresAtSnake, fields.Expired)
	t.AuthMethod = firstNonEmpty(fields.AuthMethod, fields.AuthMethodSnake)
	t.Provider = fields.Provider
	t.ClientID = firstNonEmpty(fields.ClientID, fields.ClientIDSnake)
	t.ClientSecret = firstNonEmpty(fields.ClientSecret, fields.ClientSecretSnake)
	t.ClientIDHash = firstNonEmpty(fields.ClientIDHash, fields.ClientIDHashSnake)
	t.Email = fields.Email
	t.StartURL = firstNonEmpty(fields.StartURL, fields.StartURLSnake)
	t.Region = fields.Region
	t.IssuerURL = firstNonEmpty(fields.IssuerURL, fields.IssuerURLSnake)
	t.TokenEndpoint = firstNonEmpty(fields.TokenEndpoint, fields.TokenEndpointSnake)
	t.Scopes = fields.Scopes
	return nil
}

type socialTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	ExpiresIn    int    `json:"expiresIn"`
}

func (r *socialTokenResponse) UnmarshalJSON(data []byte) error {
	type tokenResponseFields struct {
		AccessToken       string `json:"accessToken"`
		AccessTokenSnake  string `json:"access_token"`
		RefreshToken      string `json:"refreshToken"`
		RefreshTokenSnake string `json:"refresh_token"`
		ProfileArn        string `json:"profileArn"`
		ProfileArnSnake   string `json:"profile_arn"`
		ExpiresIn         int    `json:"expiresIn"`
		ExpiresInSnake    int    `json:"expires_in"`
	}
	var fields tokenResponseFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	r.AccessToken = firstNonEmpty(fields.AccessToken, fields.AccessTokenSnake)
	r.RefreshToken = firstNonEmpty(fields.RefreshToken, fields.RefreshTokenSnake)
	r.ProfileArn = firstNonEmpty(fields.ProfileArn, fields.ProfileArnSnake)
	r.ExpiresIn = firstPositive(fields.ExpiresIn, fields.ExpiresInSnake)
	return nil
}

type registerClientResponse struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type deviceAuthorizationResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

type createTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	ExpiresIn    int    `json:"expiresIn"`
}

func (r *createTokenResponse) UnmarshalJSON(data []byte) error {
	type tokenResponseFields struct {
		AccessToken       string `json:"accessToken"`
		AccessTokenSnake  string `json:"access_token"`
		RefreshToken      string `json:"refreshToken"`
		RefreshTokenSnake string `json:"refresh_token"`
		ProfileArn        string `json:"profileArn"`
		ProfileArnSnake   string `json:"profile_arn"`
		ExpiresIn         int    `json:"expiresIn"`
		ExpiresInSnake    int    `json:"expires_in"`
	}
	var fields tokenResponseFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	r.AccessToken = firstNonEmpty(fields.AccessToken, fields.AccessTokenSnake)
	r.RefreshToken = firstNonEmpty(fields.RefreshToken, fields.RefreshTokenSnake)
	r.ProfileArn = firstNonEmpty(fields.ProfileArn, fields.ProfileArnSnake)
	r.ExpiresIn = firstPositive(fields.ExpiresIn, fields.ExpiresInSnake)
	return nil
}

type userInfoResponse struct {
	Email string `json:"email"`
}

type deviceRegistration struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

func (r *deviceRegistration) UnmarshalJSON(data []byte) error {
	type deviceRegistrationFields struct {
		ClientID          string `json:"clientId"`
		ClientIDSnake     string `json:"client_id"`
		ClientSecret      string `json:"clientSecret"`
		ClientSecretSnake string `json:"client_secret"`
	}
	var fields deviceRegistrationFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	r.ClientID = firstNonEmpty(fields.ClientID, fields.ClientIDSnake)
	r.ClientSecret = firstNonEmpty(fields.ClientSecret, fields.ClientSecretSnake)
	return nil
}

type RefreshTokenInvalidError struct {
	StatusCode int
	Body       string
}

type AuthCodeExpiredError struct {
	ExpiresAt time.Time
}

func (e *AuthCodeExpiredError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("kiro authorization code expired at %s; regenerate the authorization URL and paste the final callback immediately", e.ExpiresAt.Format(time.RFC3339))
}

func (e *RefreshTokenInvalidError) Error() string {
	if e == nil {
		return ""
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return "kiro refresh token invalid (invalid_grant)"
	}
	return fmt.Sprintf("kiro refresh token invalid (invalid_grant, status %d): %s", e.StatusCode, body)
}

func GenerateSessionID() string {
	return uuid.NewString()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func GenerateState() (string, error) {
	return randomURLSafe(16)
}

func GenerateCodeVerifier() (string, error) {
	return randomURLSafe(32)
}

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func GenerateCodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func ValidateAuthCodeNotExpired(code string, now time.Time) error {
	expiresAt, ok := ParseAuthCodeExpiry(code)
	if !ok {
		return nil
	}
	if !now.Before(expiresAt) {
		return &AuthCodeExpiredError{ExpiresAt: expiresAt}
	}
	return nil
}

func ResolveAuthCodeForTokenExchange(code string) string {
	plaintext, ok := ParseAuthCodePlaintext(code)
	if ok && strings.TrimSpace(plaintext) != "" {
		return plaintext
	}
	return strings.TrimSpace(code)
}

func ParseAuthCodeExpiry(code string) (time.Time, bool) {
	parsed, ok := parseAuthCodePayload(code)
	if !ok || parsed.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(parsed.Exp, 0), true
}

func ParseAuthCodePlaintext(code string) (string, bool) {
	parsed, ok := parseAuthCodePayload(code)
	if !ok || strings.TrimSpace(parsed.Plaintext) == "" {
		return "", false
	}
	return strings.TrimSpace(parsed.Plaintext), true
}

func parseAuthCodePayload(code string) (struct {
	Plaintext string `json:"plaintext"`
	Exp       int64  `json:"exp"`
}, bool) {
	var parsed struct {
		Plaintext string `json:"plaintext"`
		Exp       int64  `json:"exp"`
	}
	parts := strings.Split(strings.TrimSpace(code), ".")
	if len(parts) < 2 {
		return parsed, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return parsed, false
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return parsed, false
	}
	return parsed, true
}

func BuildSocialSignInURL(redirectURI, codeChallenge, state string) string {
	params := url.Values{}
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("redirect_uri", redirectURI)
	params.Set("redirect_from", "KiroIDE")
	return fmt.Sprintf("%s/signin?%s", socialAuthPortalURL, params.Encode())
}

func BuildSocialTokenRedirectURI(baseRedirectURI, callbackPath, loginOption string) string {
	redirectURI := strings.TrimRight(strings.TrimSpace(baseRedirectURI), "/")
	if redirectURI == "" {
		return ""
	}
	path := strings.TrimSpace(callbackPath)
	if path == "" {
		path = "/oauth/callback"
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	fullRedirectURI := redirectURI + path
	if option := strings.TrimSpace(loginOption); option != "" {
		return fullRedirectURI + "?login_option=" + url.QueryEscape(option)
	}
	return fullRedirectURI
}

func CreateSocialToken(ctx context.Context, proxyURL, code, codeVerifier, redirectURI string) (*TokenData, error) {
	payload := map[string]string{
		"code":          code,
		"code_verifier": codeVerifier,
		"redirect_uri":  redirectURI,
	}
	var resp socialTokenResponse
	if err := doJSON(ctx, proxyURL, http.MethodPost, socialAuthEndpointURL+"/oauth/token", payload, &resp, BuildLoginHeaders(shortSHA(codeVerifier), BuildMachineID("", "", "codeVerifier:"+codeVerifier))); err != nil {
		return nil, err
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return &TokenData{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ProfileArn:   resp.ProfileArn,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "social",
		Region:       defaultIDCRegion,
	}, nil
}

func RefreshSocialToken(ctx context.Context, proxyURL, refreshToken, provider string) (*TokenData, error) {
	payload := map[string]string{
		"refreshToken": refreshToken,
	}
	var resp socialTokenResponse
	accountKey := BuildAccountKey("", "", refreshToken, "", 0)
	if err := doJSON(ctx, proxyURL, http.MethodPost, socialAuthEndpointURL+"/refreshToken", payload, &resp, BuildLoginHeaders(accountKey, BuildMachineID(refreshToken, "", accountKey))); err != nil {
		return nil, err
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return &TokenData{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ProfileArn:   resp.ProfileArn,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "social",
		Provider:     provider,
		Region:       defaultIDCRegion,
	}, nil
}

func RegisterIDCClient(ctx context.Context, proxyURL, redirectURI, issuerURL, region string) (*registerClientResponse, error) {
	if region == "" {
		region = defaultIDCRegion
	}
	payload := map[string]any{
		"clientName":   "Kiro IDE",
		"clientType":   "public",
		"scopes":       []string{"codewhisperer:completions", "codewhisperer:analysis", "codewhisperer:conversations", "codewhisperer:transformations", "codewhisperer:taskassist"},
		"grantTypes":   []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"},
		"redirectUris": []string{redirectURI},
		"issuerUrl":    issuerURL,
	}
	var resp registerClientResponse
	headers := oidcHeaders("", BuildMachineID("", "", "register-idc-client"))
	if err := doJSON(ctx, proxyURL, http.MethodPost, getOIDCEndpoint(region)+"/client/register", payload, &resp, headers); err != nil {
		return nil, err
	}
	return &resp, nil
}

func StartIDCDeviceAuthorization(ctx context.Context, proxyURL, clientID, clientSecret, startURL, region string) (*deviceAuthorizationResponse, error) {
	if region == "" {
		region = defaultIDCRegion
	}
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"startUrl":     startURL,
	}
	var resp deviceAuthorizationResponse
	accountKey := BuildAccountKey(clientID, "", "", "", 0)
	headers := oidcHeaders(accountKey, BuildMachineID("", "", "clientID:"+clientID))
	if err := doJSON(ctx, proxyURL, http.MethodPost, getOIDCEndpoint(region)+"/device_authorization", payload, &resp, headers); err != nil {
		return nil, err
	}
	return &resp, nil
}

func BuildIDCAuthURL(clientID, redirectURI, state, codeChallenge, region string) string {
	if region == "" {
		region = defaultIDCRegion
	}
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scopes", strings.Join([]string{
		"codewhisperer:completions",
		"codewhisperer:analysis",
		"codewhisperer:conversations",
		"codewhisperer:transformations",
		"codewhisperer:taskassist",
	}, " "))
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	return fmt.Sprintf("%s/authorize?%s", getOIDCEndpoint(region), params.Encode())
}

func ExchangeIDCDeviceCode(ctx context.Context, proxyURL, clientID, clientSecret, deviceCode, region, startURL string) (*TokenData, error) {
	if region == "" {
		region = defaultIDCRegion
	}
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"deviceCode":   strings.TrimSpace(deviceCode),
		"grantType":    "urn:ietf:params:oauth:grant-type:device_code",
	}
	var resp createTokenResponse
	accountKey := BuildAccountKey(clientID, "", "", "", 0)
	headers := oidcHeaders(accountKey, BuildMachineID("", "", "clientID:"+clientID))
	if err := doJSON(ctx, proxyURL, http.MethodPost, getOIDCEndpoint(region)+"/token", payload, &resp, headers); err != nil {
		return nil, err
	}
	return buildIDCTokenData(ctx, proxyURL, &resp, clientID, clientSecret, region, startURL)
}

func ExchangeIDCAuthCode(ctx context.Context, proxyURL, clientID, clientSecret, code, codeVerifier, redirectURI, region, startURL string) (*TokenData, error) {
	if region == "" {
		region = defaultIDCRegion
	}
	exchangeCode := ResolveAuthCodeForTokenExchange(code)
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"code":         exchangeCode,
		"codeVerifier": codeVerifier,
		"redirectUri":  redirectURI,
		"grantType":    "authorization_code",
	}
	var resp createTokenResponse
	accountKey := BuildAccountKey(clientID, "", "", "", 0)
	headers := oidcHeaders(accountKey, BuildMachineID("", "", "clientID:"+clientID))
	if err := doJSON(ctx, proxyURL, http.MethodPost, getOIDCEndpoint(region)+"/token", payload, &resp, headers); err != nil {
		return nil, err
	}
	return buildIDCTokenData(ctx, proxyURL, &resp, clientID, clientSecret, region, startURL)
}

func buildIDCTokenData(ctx context.Context, proxyURL string, resp *createTokenResponse, clientID, clientSecret, region, startURL string) (*TokenData, error) {
	if resp == nil {
		return nil, fmt.Errorf("kiro idc token response is empty")
	}
	if strings.TrimSpace(resp.AccessToken) == "" {
		return nil, fmt.Errorf("kiro idc token response missing access token")
	}
	if strings.TrimSpace(resp.RefreshToken) == "" {
		return nil, fmt.Errorf("kiro idc token response missing refresh token")
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	token := &TokenData{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ProfileArn:   resp.ProfileArn,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "idc",
		Provider:     resolveIDCProvider(startURL),
		ClientID:     clientID,
		ClientSecret: clientSecret,
		StartURL:     startURL,
		Region:       region,
	}
	token.Email = FetchOIDCUserEmail(ctx, proxyURL, token.AccessToken, region)
	return token, nil
}

func RefreshIDCToken(ctx context.Context, proxyURL, clientID, clientSecret, refreshToken, region, startURL string, provider ...string) (*TokenData, error) {
	if region == "" {
		region = defaultIDCRegion
	}
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
	}
	var resp createTokenResponse
	accountKey := BuildAccountKey(clientID, "", refreshToken, "", 0)
	headers := oidcHeaders(accountKey, BuildMachineID(refreshToken, "", accountKey))
	if err := doJSON(ctx, proxyURL, http.MethodPost, getOIDCEndpoint(region)+"/token", payload, &resp, headers); err != nil {
		return nil, err
	}
	if strings.TrimSpace(resp.AccessToken) == "" {
		return nil, fmt.Errorf("kiro idc token response missing access token")
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	token := &TokenData{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ProfileArn:   resp.ProfileArn,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:   "idc",
		Provider:     resolveIDCRefreshProvider(startURL, provider...),
		ClientID:     clientID,
		ClientSecret: clientSecret,
		StartURL:     startURL,
		Region:       region,
	}
	token.Email = FetchOIDCUserEmail(ctx, proxyURL, token.AccessToken, region)
	return token, nil
}

func RefreshExternalIDPToken(ctx context.Context, proxyURL, refreshToken, clientID, clientSecret, tokenEndpoint, scopes, region, profileArn, issuerURL string) (*TokenData, error) {
	tokenEndpoint = ResolveExternalIDPTokenEndpoint(tokenEndpoint, issuerURL)
	if tokenEndpoint == "" {
		return nil, fmt.Errorf("kiro external_idp refresh requires token_endpoint")
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, fmt.Errorf("kiro external_idp refresh requires client_id")
	}
	if region == "" {
		region = defaultIDCRegion
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", clientID)
	form.Set("refresh_token", strings.TrimSpace(refreshToken))
	if strings.TrimSpace(clientSecret) != "" {
		form.Set("client_secret", strings.TrimSpace(clientSecret))
	}
	if strings.TrimSpace(scopes) != "" {
		form.Set("scope", strings.TrimSpace(scopes))
	}
	var resp createTokenResponse
	if err := doForm(ctx, proxyURL, http.MethodPost, tokenEndpoint, form, &resp, map[string]string{
		"Accept": "application/json",
	}); err != nil {
		return nil, err
	}
	if strings.TrimSpace(resp.AccessToken) == "" {
		return nil, fmt.Errorf("kiro external_idp token response missing access token")
	}
	expiresIn := resp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	nextRefreshToken := strings.TrimSpace(resp.RefreshToken)
	if nextRefreshToken == "" {
		nextRefreshToken = strings.TrimSpace(refreshToken)
	}
	nextProfileArn := strings.TrimSpace(resp.ProfileArn)
	if nextProfileArn == "" {
		nextProfileArn = strings.TrimSpace(profileArn)
	}
	return &TokenData{
		AccessToken:   resp.AccessToken,
		RefreshToken:  nextRefreshToken,
		ProfileArn:    nextProfileArn,
		ExpiresAt:     time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AuthMethod:    "external_idp",
		Provider:      ProviderEnterprise,
		ClientID:      clientID,
		ClientSecret:  strings.TrimSpace(clientSecret),
		Region:        region,
		IssuerURL:     strings.TrimSpace(issuerURL),
		TokenEndpoint: tokenEndpoint,
		Scopes:        strings.TrimSpace(scopes),
	}, nil
}

func ResolveExternalIDPTokenEndpoint(tokenEndpoint, issuerURL string) string {
	if endpoint := strings.TrimSpace(tokenEndpoint); endpoint != "" {
		return endpoint
	}
	issuer := strings.TrimRight(strings.TrimSpace(issuerURL), "/")
	if issuer == "" {
		return ""
	}
	lower := strings.ToLower(issuer)
	if strings.HasSuffix(lower, "/token") && strings.Contains(lower, "/oauth2/") {
		return issuer
	}
	const azureV2Suffix = "/v2.0"
	if strings.HasSuffix(lower, azureV2Suffix) {
		return issuer[:len(issuer)-len(azureV2Suffix)] + "/oauth2/v2.0/token"
	}
	if strings.Contains(lower, "login.microsoftonline.com/") {
		return issuer + "/oauth2/v2.0/token"
	}
	return ""
}

func FetchOIDCUserEmail(ctx context.Context, proxyURL, accessToken, region string) string {
	if strings.TrimSpace(accessToken) == "" {
		return ""
	}
	var resp userInfoResponse
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	if err := doJSON(ctx, proxyURL, http.MethodGet, getOIDCEndpoint(region)+"/userinfo", nil, &resp, headers); err != nil {
		return ""
	}
	return strings.TrimSpace(resp.Email)
}

func ParseImportedToken(tokenJSON string, deviceRegistrationJSON string) (*TokenData, error) {
	var token TokenData
	if err := json.Unmarshal([]byte(tokenJSON), &token); err != nil {
		return nil, fmt.Errorf("failed to parse kiro token: %w", err)
	}
	token.AuthMethod = normalizeImportedAuthMethod(token.AuthMethod)
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, fmt.Errorf("access token is empty")
	}
	if token.ClientIDHash != "" && (token.ClientID == "" || token.ClientSecret == "") && strings.TrimSpace(deviceRegistrationJSON) != "" {
		var reg deviceRegistration
		if err := json.Unmarshal([]byte(deviceRegistrationJSON), &reg); err != nil {
			return nil, fmt.Errorf("failed to parse device registration: %w", err)
		}
		if reg.ClientID != "" {
			token.ClientID = reg.ClientID
		}
		if reg.ClientSecret != "" {
			token.ClientSecret = reg.ClientSecret
		}
	}
	if token.AuthMethod == "" && strings.TrimSpace(token.ClientID) != "" && strings.TrimSpace(token.ClientSecret) != "" {
		token.AuthMethod = "idc"
	}
	if token.AuthMethod == "" && strings.TrimSpace(token.ClientID) != "" && strings.TrimSpace(token.TokenEndpoint) != "" {
		token.AuthMethod = "external_idp"
	}
	token.Provider = strings.TrimSpace(token.Provider)
	if token.AuthMethod == "external_idp" && token.Provider == "" {
		token.Provider = ProviderEnterprise
	}
	if token.AuthMethod == "external_idp" {
		if strings.TrimSpace(token.StartURL) == "" && strings.TrimSpace(token.IssuerURL) != "" {
			token.StartURL = strings.TrimSpace(token.IssuerURL)
		}
		token.TokenEndpoint = ResolveExternalIDPTokenEndpoint(token.TokenEndpoint, firstNonEmpty(token.IssuerURL, token.StartURL))
	}
	if strings.EqualFold(token.Provider, "AWS") && token.AuthMethod == "idc" {
		token.Provider = resolveIDCProvider(token.StartURL)
	}
	if !IsValidKiroProvider(token.Provider) {
		return nil, fmt.Errorf("unsupported or missing kiro provider: %q (must be one of Google/Github/BuilderId/Enterprise)", token.Provider)
	}
	if (token.AuthMethod == "idc" || token.AuthMethod == "external_idp") && strings.TrimSpace(token.Region) == "" {
		token.Region = defaultIDCRegion
	}
	if strings.TrimSpace(token.ExpiresAt) != "" {
		normalized, err := normalizeKiroExpiresAt(token.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("failed to parse kiro token expiresAt: %w", err)
		}
		token.ExpiresAt = normalized
	}
	return &token, nil
}

func normalizeImportedAuthMethod(authMethod string) string {
	switch strings.ToLower(strings.TrimSpace(authMethod)) {
	case "idc", "iam_identity_center":
		return "idc"
	case "external_idp", "external-idp", "externalidp":
		return "external_idp"
	case "social":
		return "social"
	default:
		return strings.ToLower(strings.TrimSpace(authMethod))
	}
}

func getOIDCEndpoint(region string) string {
	if strings.TrimSpace(oidcEndpointOverride) != "" {
		return strings.TrimRight(strings.TrimSpace(oidcEndpointOverride), "/")
	}
	if region == "" {
		region = defaultIDCRegion
	}
	return fmt.Sprintf("https://oidc.%s.amazonaws.com", region)
}

func oidcHeaders(accountKey, machineID string) map[string]string {
	headers := BuildOIDCHeaders(accountKey, machineID)
	if headers["amz-sdk-invocation-id"] == "" {
		headers["amz-sdk-invocation-id"] = uuid.NewString()
	}
	if headers["amz-sdk-request"] == "" {
		headers["amz-sdk-request"] = "attempt=1; max=4"
	}
	return headers
}

func doJSON(ctx context.Context, proxyURL, method, rawURL string, payload any, out any, extraHeaders map[string]string) error {
	client, err := newHTTPClient(proxyURL)
	if err != nil {
		return err
	}

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return err
	}

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyText := strings.TrimSpace(string(respBody))
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(bodyText), "invalid_grant") {
			return &RefreshTokenInvalidError{StatusCode: resp.StatusCode, Body: bodyText}
		}
		return fmt.Errorf("upstream request failed (status %d): %s", resp.StatusCode, bodyText)
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}

func doForm(ctx context.Context, proxyURL, method, rawURL string, values url.Values, out any, extraHeaders map[string]string) error {
	client, err := newHTTPClient(proxyURL)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyText := strings.TrimSpace(string(respBody))
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(bodyText), "invalid_grant") {
			return &RefreshTokenInvalidError{StatusCode: resp.StatusCode, Body: bodyText}
		}
		return fmt.Errorf("upstream request failed (status %d): %s", resp.StatusCode, bodyText)
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}

func newHTTPClient(rawProxyURL string) (*http.Client, error) {
	_, parsed, err := proxyurl.Parse(rawProxyURL)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{}
	if parsed != nil {
		if err := proxyutil.ConfigureTransportProxy(transport, parsed); err != nil {
			return nil, err
		}
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}, nil
}
