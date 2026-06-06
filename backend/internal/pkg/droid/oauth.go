package droid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

const (
	WorkOSDeviceAuthorizeURL = "https://api.workos.com/user_management/authorize/device"
	WorkOSTokenURL           = "https://api.workos.com/user_management/authenticate"
	WorkOSClientID           = "client_01HNM792M5G5G1A2THWPXKFMXB"

	sessionTTL          = 10 * time.Minute
	sessionCleanupEvery = 32
	sessionCleanupMin   = 32
	defaultPollInterval = 5
	requestTimeout      = 15 * time.Second
)

type DeviceAuthError struct {
	Message    string
	Code       string
	RetryAfter int
}

func (e *DeviceAuthError) Error() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Message)
}

type DeviceAuthSession struct {
	State      string
	DeviceCode string
	ProxyURL   string
	CreatedAt  time.Time
}

type SessionStore struct {
	mu       sync.RWMutex
	data     map[string]*DeviceAuthSession
	setCount uint64
}

func NewSessionStore() *SessionStore {
	return &SessionStore{data: make(map[string]*DeviceAuthSession)}
}

func (s *SessionStore) Get(id string) (*DeviceAuthSession, bool) {
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

func (s *SessionStore) Set(id string, session *DeviceAuthSession) {
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

func sessionExpired(session *DeviceAuthSession, now time.Time) bool {
	if session == nil || session.CreatedAt.IsZero() {
		return true
	}
	return now.After(session.CreatedAt.Add(sessionTTL))
}

type DeviceAuthorizationResult struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int
	Interval                int
}

type TokenData struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	ExpiresAt    string
	TokenType    string
}

func StartDeviceAuthorization(ctx context.Context, proxyURL string) (*DeviceAuthorizationResult, error) {
	values := url.Values{}
	values.Set("client_id", WorkOSClientID)

	var payload struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := doFormRequest(ctx, proxyURL, WorkOSDeviceAuthorizeURL, values, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.DeviceCode) == "" || strings.TrimSpace(payload.VerificationURI) == "" {
		return nil, fmt.Errorf("workos response missing device_code or verification_uri")
	}
	result := &DeviceAuthorizationResult{
		DeviceCode:              payload.DeviceCode,
		UserCode:                payload.UserCode,
		VerificationURI:         payload.VerificationURI,
		VerificationURIComplete: payload.VerificationURIComplete,
		ExpiresIn:               payload.ExpiresIn,
		Interval:                payload.Interval,
	}
	if result.VerificationURIComplete == "" {
		result.VerificationURIComplete = result.VerificationURI
	}
	if result.ExpiresIn <= 0 {
		result.ExpiresIn = 300
	}
	if result.Interval <= 0 {
		result.Interval = defaultPollInterval
	}
	return result, nil
}

func PollDeviceAuthorization(ctx context.Context, proxyURL, deviceCode string) (*TokenData, error) {
	if strings.TrimSpace(deviceCode) == "" {
		return nil, &DeviceAuthError{Message: "missing device code", Code: "missing_device_code"}
	}

	values := url.Values{}
	values.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	values.Set("device_code", deviceCode)
	values.Set("client_id", WorkOSClientID)

	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		ExpiresAt    string `json:"expires_at"`
		TokenType    string `json:"token_type"`
	}
	if err := doFormRequest(ctx, proxyURL, WorkOSTokenURL, values, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, &DeviceAuthError{Message: "workos response missing access_token", Code: "missing_access_token"}
	}
	return &TokenData{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		ExpiresIn:    payload.ExpiresIn,
		ExpiresAt:    payload.ExpiresAt,
		TokenType:    payload.TokenType,
	}, nil
}

func RefreshToken(ctx context.Context, proxyURL, refreshToken string) (*TokenData, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, &DeviceAuthError{Message: "droid refresh_token is empty; reauthorize Droid account", Code: "missing_refresh_token"}
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", WorkOSClientID)

	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		ExpiresAt    string `json:"expires_at"`
		TokenType    string `json:"token_type"`
	}
	if err := doFormRequest(ctx, proxyURL, WorkOSTokenURL, values, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, &DeviceAuthError{Message: "workos response missing access_token", Code: "missing_access_token"}
	}
	if strings.TrimSpace(payload.RefreshToken) == "" {
		payload.RefreshToken = refreshToken
	}
	return &TokenData{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		ExpiresIn:    payload.ExpiresIn,
		ExpiresAt:    payload.ExpiresAt,
		TokenType:    payload.TokenType,
	}, nil
}

func doFormRequest(ctx context.Context, rawProxyURL, endpoint string, values url.Values, out any) error {
	client, err := newHTTPClient(rawProxyURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseDeviceAuthError(resp, body)
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func parseDeviceAuthError(resp *http.Response, body []byte) error {
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		Interval         int    `json:"interval"`
	}
	_ = json.Unmarshal(body, &payload)

	code := strings.TrimSpace(payload.Error)
	message := strings.TrimSpace(payload.ErrorDescription)
	if message == "" {
		message = code
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}

	retryAfter := payload.Interval
	if retryAfter <= 0 && resp != nil {
		if value := strings.TrimSpace(resp.Header.Get("Retry-After")); value != "" {
			var parsed int
			_, _ = fmt.Sscanf(value, "%d", &parsed)
			if parsed > 0 {
				retryAfter = parsed
			}
		}
	}

	return &DeviceAuthError{
		Message:    message,
		Code:       code,
		RetryAfter: retryAfter,
	}
}

func newHTTPClient(rawProxyURL string) (*http.Client, error) {
	_, parsed, err := proxyurl.Parse(rawProxyURL)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{}
	if parsed != nil {
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{
		Timeout:   requestTimeout,
		Transport: transport,
	}, nil
}
