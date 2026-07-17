package kiro

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildExternalIDPAuthURL(t *testing.T) {
	raw := BuildExternalIDPAuthURL(
		"https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize",
		"client-id",
		"http://localhost:3128/oauth/callback",
		"openid offline_access",
		"challenge",
		"state",
		"user@example.com",
	)
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	require.Equal(t, "client-id", parsed.Query().Get("client_id"))
	require.Equal(t, "code", parsed.Query().Get("response_type"))
	require.Equal(t, "query", parsed.Query().Get("response_mode"))
	require.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	require.Equal(t, "user@example.com", parsed.Query().Get("login_hint"))
}

func TestValidateExternalIDPEndpoint(t *testing.T) {
	require.NoError(t, validateExternalIDPEndpoint("https://login.microsoftonline.com/tenant/v2.0"))
	require.NoError(t, validateExternalIDPEndpoint("https://login.microsoftonline.us/tenant/oauth2/v2.0/token"))
	require.Error(t, validateExternalIDPEndpoint("http://login.microsoftonline.com/tenant"))
	require.Error(t, validateExternalIDPEndpoint("https://login.microsoftonline.com.attacker.example/tenant"))
	require.Error(t, validateExternalIDPEndpoint("https://127.0.0.1/token"))
}

func TestExchangeExternalIDPAuthCodeRequiresRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-only","expires_in":3600}`))
	}))
	defer server.Close()

	_, err := ExchangeExternalIDPAuthCode(
		context.Background(), "", server.URL, "client-id", "code", "verifier",
		"http://localhost:3128/oauth/callback", "openid offline_access", "https://login.microsoftonline.com/tenant/v2.0",
	)
	require.ErrorContains(t, err, "missing refresh token")
}

func TestNormalizeAuthMethod(t *testing.T) {
	require.Equal(t, AuthMethodIDC, NormalizeAuthMethod("IAM_IDENTITY_CENTER"))
	require.Equal(t, AuthMethodIDC, NormalizeAuthMethod("builder-id"))
	require.Equal(t, AuthMethodIDC, NormalizeAuthMethod("AWSIDC"))
	require.Equal(t, AuthMethodExternalIDP, NormalizeAuthMethod("External-IdP"))
	require.Equal(t, AuthMethodSocial, NormalizeAuthMethod(" SOCIAL "))
	require.Empty(t, NormalizeAuthMethod("unknown"))
}

func TestSessionStoreUsesSnapshots(t *testing.T) {
	store := NewSessionStore()
	original := &AuthSession{State: "initial", CreatedAt: time.Now()}
	store.Set("session", original)
	original.State = "mutated-after-set"

	first, ok := store.Get("session")
	require.True(t, ok)
	require.Equal(t, "initial", first.State)
	first.State = "mutated-after-get"

	second, ok := store.Get("session")
	require.True(t, ok)
	require.Equal(t, "initial", second.State)
}
