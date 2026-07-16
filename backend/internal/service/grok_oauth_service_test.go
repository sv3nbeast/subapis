//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func grokTestJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

type grokOAuthClientStub struct {
	refreshResponse *xai.TokenResponse
	exchangeCalls   int
}

func (s *grokOAuthClientStub) ExchangeCode(context.Context, string, string, string, string, string) (*xai.TokenResponse, error) {
	s.exchangeCalls++
	return &xai.TokenResponse{}, nil
}

func (s *grokOAuthClientStub) RefreshToken(context.Context, string, string, string) (*xai.TokenResponse, error) {
	return s.refreshResponse, nil
}

func TestGrokOAuthServiceRefreshTokenPreservesOriginalRefreshTokenWhenNotRotated(t *testing.T) {
	svc := NewGrokOAuthService(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "new-access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	defer svc.Stop()

	info, err := svc.RefreshToken(context.Background(), "original-refresh-token", "", "client-id")
	require.NoError(t, err)
	require.Equal(t, "new-access-token", info.AccessToken)
	require.Equal(t, "original-refresh-token", info.RefreshToken)
	require.Equal(t, "client-id", info.ClientID)
}

func TestGrokOAuthServiceExchangeCodeRequiresStateForCallbackURLAndConsumesSession(t *testing.T) {
	client := &grokOAuthClientStub{}
	svc := NewGrokOAuthService(nil, client)
	defer svc.Stop()

	auth, err := svc.GenerateAuthURL(context.Background(), nil, "")
	require.NoError(t, err)

	_, err = svc.ExchangeCode(context.Background(), &GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "http://127.0.0.1:56121/callback?code=code-without-state",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_STATE_REQUIRED")
	require.Zero(t, client.exchangeCalls)

	_, err = svc.ExchangeCode(context.Background(), &GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "code-with-state",
		State:     auth.State,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_SESSION_NOT_FOUND")
	require.Zero(t, client.exchangeCalls)
}

func TestInferGrokBaseURL(t *testing.T) {
	tests := []struct {
		name        string
		accessToken string
		want        string
	}{
		{name: "API token has tier claim", accessToken: grokTestJWT(t, map[string]any{"tier": "api"}), want: xai.DefaultBaseURL},
		{name: "free CLI token has no tier claim", accessToken: grokTestJWT(t, map[string]any{"sub": "free-user"}), want: xai.DefaultCLIBaseURL},
		{name: "opaque token keeps API default", accessToken: "opaque-token", want: xai.DefaultBaseURL},
		{name: "malformed payload keeps API default", accessToken: "eyJhbGciOiJSUzI1NiJ9.invalid.signature", want: xai.DefaultBaseURL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, inferGrokBaseURL(tt.accessToken))
		})
	}
}

func TestGrokOAuthServiceBuildAccountCredentialsUsesInferredCLIBaseURL(t *testing.T) {
	svc := NewGrokOAuthService(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: grokTestJWT(t, map[string]any{"sub": "free-user"}),
			ExpiresIn:   3600,
		},
	})
	defer svc.Stop()

	info, err := svc.RefreshToken(context.Background(), "refresh-token", "", "client-id")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultCLIBaseURL, info.BaseURL)
	require.Equal(t, xai.DefaultCLIBaseURL, svc.BuildAccountCredentials(info)["base_url"])
}

func TestEnrichGrokOAuthCredentialsBuildsStablePrincipalIdentity(t *testing.T) {
	first := EnrichGrokOAuthCredentials(map[string]any{
		"access_token":  grokTestJWT(t, map[string]any{"sub": "user-1", "email": "User@Example.com", "team_id": "team-1"}),
		"refresh_token": "refresh-a",
		"client_id":     xai.DefaultClientID,
	})
	second := EnrichGrokOAuthCredentials(map[string]any{
		"access_token":  grokTestJWT(t, map[string]any{"sub": "user-1", "email": "User@Example.com", "team_id": "team-1"}),
		"refresh_token": "refresh-b",
		"client_id":     xai.DefaultClientID,
	})

	require.Equal(t, "user-1", first["user_id"])
	require.Equal(t, "team-1", first["team_id"])
	require.Equal(t, first["identity_key"], second["identity_key"])
	require.True(t, SameGrokOAuthIdentity(first, second))
}

func TestSameGrokOAuthIdentitySupportsLegacyEmailAndSeparatesTeams(t *testing.T) {
	legacy := map[string]any{"email": "User@Example.com", "client_id": xai.DefaultClientID}
	sameUser := map[string]any{"email": "user@example.com", "client_id": xai.DefaultClientID, "team_id": "team-1"}
	otherTeam := map[string]any{"email": "user@example.com", "client_id": xai.DefaultClientID, "team_id": "team-2"}

	require.True(t, SameGrokOAuthIdentity(legacy, sameUser))
	require.False(t, SameGrokOAuthIdentity(sameUser, otherTeam))
}

func TestPreserveGrokOAuthRoutingCredentials(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Credentials: map[string]any{
			"base_url":      xai.DefaultCLIBaseURL,
			"model_mapping": map[string]any{"grok-latest": "grok-4.5"},
			"scope":         "old-scope",
		},
	}

	merged := PreserveGrokOAuthRoutingCredentials(account, map[string]any{
		"access_token": "new-access-token",
		"scope":        "new-scope",
	})

	require.Equal(t, xai.DefaultCLIBaseURL, merged["base_url"])
	require.Equal(t, map[string]any{"grok-latest": "grok-4.5"}, merged["model_mapping"])
	require.Equal(t, "new-scope", merged["scope"])
}

func TestPreserveGrokOAuthRoutingCredentialsInfersMissingRoute(t *testing.T) {
	accessToken := grokTestJWT(t, map[string]any{"sub": "free-user"})
	merged := PreserveGrokOAuthRoutingCredentials(&Account{Platform: PlatformGrok}, map[string]any{
		"access_token": accessToken,
	})

	require.Equal(t, xai.DefaultCLIBaseURL, merged["base_url"])
}
