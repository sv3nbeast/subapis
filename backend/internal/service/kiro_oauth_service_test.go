//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

func TestKiroIDCAuthRedirectURIUsesLoopbackIP(t *testing.T) {
	require.Equal(t, "http://127.0.0.1:9876/oauth/callback", kiroIDCRedirectURI)
}

func TestKiroSocialAuthRedirectURIUsesLoopbackIP(t *testing.T) {
	require.Equal(t, "http://localhost:49153", kiroSocialRedirectURI)
}

func TestBuildKiroSocialExchangeRedirectURIUsesProviderDefault(t *testing.T) {
	require.Equal(
		t,
		"http://localhost:49153/oauth/callback?login_option=github",
		buildKiroSocialExchangeRedirectURI("http://localhost:49153", "Github", "", ""),
	)
}

func TestBuildKiroSocialExchangeRedirectURIPreservesParsedCallbackData(t *testing.T) {
	require.Equal(
		t,
		"http://localhost:49153/signin/callback?login_option=google",
		buildKiroSocialExchangeRedirectURI("http://localhost:49153", "Github", "/signin/callback", "google"),
	)
}

func TestKiroOAuthService_ExchangeCodeRejectsExpiredSession(t *testing.T) {
	svc := NewKiroOAuthService(nil)
	svc.sessionStore.Set("expired-session", &kiropkg.AuthSession{
		State:     "expected-state",
		CreatedAt: time.Now().Add(-11 * time.Minute),
	})

	_, err := svc.ExchangeCode(context.Background(), &KiroExchangeCodeInput{
		SessionID: "expired-session",
		State:     "expected-state",
		Code:      "auth-code",
	})
	require.EqualError(t, err, "session not found or expired")
}

func TestKiroOAuthService_ExchangeCodeRejectsAWSIDCContinuationWithoutCode(t *testing.T) {
	svc := NewKiroOAuthService(nil)
	svc.sessionStore.Set("social-session", &kiropkg.AuthSession{
		State:        "expected-state",
		CodeVerifier: "code-verifier",
		CreatedAt:    time.Now(),
		AuthType:     "social",
		Provider:     string(kiropkg.SocialProviderGoogle),
		RedirectURI:  kiroSocialRedirectURI,
	})

	_, err := svc.ExchangeCode(context.Background(), &KiroExchangeCodeInput{
		SessionID:    "social-session",
		State:        "expected-state",
		LoginOption:  "awsidc",
		CallbackPath: "/signin/callback",
	})
	require.EqualError(t, err, "kiro AWS IDC continuation callback received; continue IDC authorization before exchanging token")
}

func TestKiroOAuthService_ExchangeCodeRejectsEmptyCode(t *testing.T) {
	svc := NewKiroOAuthService(nil)
	svc.sessionStore.Set("social-session", &kiropkg.AuthSession{
		State:        "expected-state",
		CodeVerifier: "code-verifier",
		CreatedAt:    time.Now(),
		AuthType:     "social",
		Provider:     string(kiropkg.SocialProviderGoogle),
		RedirectURI:  kiroSocialRedirectURI,
	})

	_, err := svc.ExchangeCode(context.Background(), &KiroExchangeCodeInput{
		SessionID: "social-session",
		State:     "expected-state",
	})
	require.EqualError(t, err, "authorization code is empty")
}

func TestKiroOAuthService_GenerateIDCAuthURLUsesDeviceAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client/register":
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Contains(t, payload["grantTypes"], "urn:ietf:params:oauth:grant-type:device_code")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"clientId":"client-id","clientSecret":"client-secret"}`))
		case "/device_authorization":
			var payload map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, "client-id", payload["clientId"])
			require.Equal(t, "https://d-example.awsapps.com/start", payload["startUrl"])
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"deviceCode":"device-code","userCode":"ABCD-EFGH","verificationUri":"https://device.example/","verificationUriComplete":"https://device.example/?user_code=ABCD-EFGH","expiresIn":600,"interval":5}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	previous := kiropkg.SetOIDCEndpointOverrideForTest(server.URL)
	t.Cleanup(func() { kiropkg.SetOIDCEndpointOverrideForTest(previous) })

	svc := NewKiroOAuthService(nil)
	result, err := svc.GenerateIDCAuthURL(context.Background(), &KiroGenerateIDCAuthURLInput{
		StartURL: "https://d-example.awsapps.com/start",
		Region:   "us-east-1",
	})
	require.NoError(t, err)
	require.Equal(t, "https://device.example/?user_code=ABCD-EFGH", result.AuthURL)
	require.Equal(t, "ABCD-EFGH", result.UserCode)
	require.Equal(t, 600, result.ExpiresIn)

	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)
	require.Equal(t, "device-code", session.DeviceCode)
}

func TestKiroOAuthService_ExchangeCodeUsesDeviceCodeWhenPresent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			var payload map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, "device-code", payload["deviceCode"])
			require.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", payload["grantType"])
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessToken":"access-token","refreshToken":"refresh-token","expiresIn":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"kiro@example.com"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	previous := kiropkg.SetOIDCEndpointOverrideForTest(server.URL)
	t.Cleanup(func() { kiropkg.SetOIDCEndpointOverrideForTest(previous) })

	svc := NewKiroOAuthService(nil)
	svc.sessionStore.Set("idc-session", &kiropkg.AuthSession{
		State:        "expected-state",
		DeviceCode:   "device-code",
		CreatedAt:    time.Now(),
		AuthType:     "idc",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		Region:       "us-east-1",
		StartURL:     "https://d-example.awsapps.com/start",
	})

	token, err := svc.ExchangeCode(context.Background(), &KiroExchangeCodeInput{
		SessionID: "idc-session",
		State:     "expected-state",
	})
	require.NoError(t, err)
	require.Equal(t, "access-token", token.AccessToken)
	require.Equal(t, "kiro@example.com", token.Email)
}
