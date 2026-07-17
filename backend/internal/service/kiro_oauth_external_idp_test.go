package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

func TestKiroOAuthServiceExternalIDPTwoStageFlow(t *testing.T) {
	var gotCode string
	var gotVerifier string
	var gotRedirect string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotCode = r.Form.Get("code")
		gotVerifier = r.Form.Get("code_verifier")
		gotRedirect = r.Form.Get("redirect_uri")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"external-access","refresh_token":"external-refresh","expires_in":1800,"scope":"openid offline_access"}`))
	}))
	defer tokenServer.Close()

	previousDiscovery := kiroDiscoverExternalIDP
	kiroDiscoverExternalIDP = func(context.Context, string, string) (string, string, error) {
		return "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize", tokenServer.URL, nil
	}
	t.Cleanup(func() { kiroDiscoverExternalIDP = previousDiscovery })

	service := NewKiroOAuthService(nil)
	portal, err := service.GenerateAuthURL(context.Background(), &KiroGenerateAuthURLInput{Provider: kiropkg.ProviderExternalIdp})
	require.NoError(t, err)
	require.NotEmpty(t, portal.State)

	descriptor := "http://localhost/signin?login_option=external_idp&client_id=client-id&issuer_url=https%3A%2F%2Flogin.microsoftonline.com%2Ftenant%2Fv2.0&scopes=openid%20offline_access&login_hint=user%40example.com"
	idp, err := service.ExchangeCode(context.Background(), &KiroExchangeCodeInput{
		SessionID: portal.SessionID,
		State:     portal.State,
		Code:      descriptor,
	})
	require.NoError(t, err)
	require.Empty(t, idp.AccessToken)
	require.NotEmpty(t, idp.AuthURL)
	require.NotEqual(t, portal.State, idp.State)
	require.Contains(t, idp.AuthURL, "login_hint=user%40example.com")
	secondStageSession, ok := service.sessionStore.Get(idp.SessionID)
	require.True(t, ok)
	require.Equal(t, kiropkg.AuthMethodExternalIDP, secondStageSession.AuthType)
	require.Equal(t, kiropkg.ProviderExternalIdp, secondStageSession.Provider)
	require.WithinDuration(t, time.Now(), secondStageSession.CreatedAt, time.Second)

	_, err = service.ExchangeCode(context.Background(), &KiroExchangeCodeInput{
		SessionID: portal.SessionID,
		State:     portal.State,
		Code:      "stale-code",
	})
	require.ErrorContains(t, err, "state invalid")

	token, err := service.ExchangeCode(context.Background(), &KiroExchangeCodeInput{
		SessionID: idp.SessionID,
		State:     idp.State,
		Code:      "final-code",
	})
	require.NoError(t, err)
	require.Equal(t, "external-access", token.AccessToken)
	require.Equal(t, "external-refresh", token.RefreshToken)
	require.Equal(t, kiropkg.AuthMethodExternalIDP, token.AuthMethod)
	require.Equal(t, kiropkg.ProviderExternalIdp, token.Provider)
	require.Equal(t, "https://login.microsoftonline.com/tenant/v2.0", token.IssuerURL)
	require.Equal(t, tokenServer.URL, token.TokenEndpoint)
	require.Equal(t, "final-code", gotCode)
	require.NotEmpty(t, gotVerifier)
	require.Equal(t, kiroExternalIDPRedirectURI, gotRedirect)
}
