//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

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
	exchangeRedirectURI string
	loginEmail          string
	loginPassword       string
	loginResult         *GrokPasswordLoginResult
	refreshResponse     *xai.TokenResponse
	ssoResponse         *xai.TokenResponse
	deviceResponse      *xai.DeviceAuthorizationResponse
	deviceToken         *xai.TokenResponse
	devicePollErr       error
	deviceStartErr      error
	deviceStartCalls    int
	devicePollCalls     int
	deviceProxyURL      string
	deviceClientID      string
	deviceScope         string
	exchangeCalls       int
	principalType       string
	principalID         string
}

func (s *grokOAuthClientStub) StartDeviceAuthorization(_ context.Context, proxyURL, clientID, scope string) (*xai.DeviceAuthorizationResponse, error) {
	s.deviceStartCalls++
	s.deviceProxyURL = proxyURL
	s.deviceClientID = clientID
	s.deviceScope = scope
	return s.deviceResponse, s.deviceStartErr
}

func (s *grokOAuthClientStub) PollDeviceAuthorization(context.Context, string, string, string) (*xai.TokenResponse, error) {
	s.devicePollCalls++
	return s.deviceToken, s.devicePollErr
}

func (s *grokOAuthClientStub) ExchangeCode(_ context.Context, _, _, redirectURI, _, _ string) (*xai.TokenResponse, error) {
	s.exchangeCalls++
	s.exchangeRedirectURI = redirectURI
	return &xai.TokenResponse{AccessToken: "access-token"}, nil
}

func (s *grokOAuthClientStub) RefreshToken(_ context.Context, _, _, _, principalType, principalID string) (*xai.TokenResponse, error) {
	s.principalType = principalType
	s.principalID = principalID
	return s.refreshResponse, nil
}

func (s *grokOAuthClientStub) LoginWithPassword(_ context.Context, email, password, _ string) (*GrokPasswordLoginResult, error) {
	s.loginEmail = email
	s.loginPassword = password
	return s.loginResult, nil
}

func (s *grokOAuthClientStub) ConvertSSOToBuild(context.Context, string, string) (*xai.TokenResponse, error) {
	return s.ssoResponse, nil
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

func TestGrokOAuthServiceDeviceFlowDelaysPollingBindsOperationAndCompletesIdempotently(t *testing.T) {
	client := &grokOAuthClientStub{
		deviceResponse: &xai.DeviceAuthorizationResponse{
			DeviceCode:              "device-secret",
			UserCode:                "ABCD-EFGH",
			VerificationURI:         "https://accounts.x.ai/oauth2/device",
			VerificationURIComplete: "https://accounts.x.ai/oauth2/device?user_code=ABCD-EFGH",
			Interval:                5,
			ExpiresIn:               1800,
		},
		deviceToken: &xai.TokenResponse{
			AccessToken:  "device-access",
			RefreshToken: "device-refresh",
			ExpiresIn:    3600,
		},
	}
	svc := NewGrokOAuthService(nil, client)
	defer svc.Stop()

	accountID := int64(42)
	expectedCredentials := map[string]any{"refresh_token": "generation-a", "model_mapping": map[string]any{"grok": "grok-4"}}
	started, err := svc.StartDeviceAuthorization(context.Background(), nil, &accountID, expectedCredentials)
	require.NoError(t, err)
	expectedCredentials["refresh_token"] = "caller-mutated"
	require.Equal(t, 1, client.deviceStartCalls)
	require.Equal(t, xai.DefaultClientID, client.deviceClientID)
	require.Equal(t, xai.DefaultScope, client.deviceScope)
	require.Equal(t, "ABCD-EFGH", started.UserCode)

	pending, err := svc.PollDeviceAuthorization(context.Background(), started.SessionID)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	require.Zero(t, client.devicePollCalls, "a fresh device code must not be polled immediately")

	session, ok := svc.deviceStore.Get(started.SessionID)
	require.True(t, ok)
	session.mu.Lock()
	session.nextPollAt = time.Now().Add(-time.Second)
	session.mu.Unlock()
	authorized, err := svc.PollDeviceAuthorization(context.Background(), started.SessionID)
	require.NoError(t, err)
	require.Equal(t, "authorized", authorized.Status)
	require.Equal(t, 1, client.devicePollCalls)
	payload, err := json.Marshal(authorized)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "device-access")
	require.NotContains(t, string(payload), "device-refresh")

	callbackCalls := 0
	_, err = svc.CompleteDeviceAuthorization(started.SessionID, nil, nil, func(*GrokTokenInfo, map[string]any) (int64, error) {
		callbackCalls++
		return 99, nil
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_DEVICE_ACCOUNT_CHANGED")
	require.Zero(t, callbackCalls)

	completedID, err := svc.CompleteDeviceAuthorization(started.SessionID, nil, &accountID, func(info *GrokTokenInfo, generation map[string]any) (int64, error) {
		callbackCalls++
		require.Equal(t, "device-access", info.AccessToken)
		require.Equal(t, "device-refresh", info.RefreshToken)
		require.Equal(t, "generation-a", generation["refresh_token"])
		return 99, nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(99), completedID)
	require.Equal(t, 1, callbackCalls)

	completedID, err = svc.CompleteDeviceAuthorization(started.SessionID, nil, &accountID, func(*GrokTokenInfo, map[string]any) (int64, error) {
		callbackCalls++
		return 100, nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(99), completedID)
	require.Equal(t, 1, callbackCalls, "completion retry must return the stored receipt")

	authorized, err = svc.PollDeviceAuthorization(context.Background(), started.SessionID)
	require.NoError(t, err)
	require.Equal(t, "authorized", authorized.Status)
	require.Equal(t, 1, client.devicePollCalls, "completed sessions must not poll xAI again")
}

func TestGrokOAuthServiceDeviceFlowHonorsSlowDown(t *testing.T) {
	client := &grokOAuthClientStub{
		deviceResponse: &xai.DeviceAuthorizationResponse{
			DeviceCode:      "device-secret",
			UserCode:        "ABCD-EFGH",
			VerificationURI: "https://accounts.x.ai/oauth2/device",
			Interval:        5,
			ExpiresIn:       1800,
		},
		devicePollErr: &xai.DeviceAuthorizationError{Code: "slow_down", RetryAfter: 22 * time.Second},
	}
	svc := NewGrokOAuthService(nil, client)
	defer svc.Stop()

	started, err := svc.StartDeviceAuthorization(context.Background(), nil, nil, nil)
	require.NoError(t, err)
	session, ok := svc.deviceStore.Get(started.SessionID)
	require.True(t, ok)
	session.mu.Lock()
	session.nextPollAt = time.Now().Add(-time.Second)
	session.mu.Unlock()

	pending, err := svc.PollDeviceAuthorization(context.Background(), started.SessionID)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	require.Equal(t, 22, pending.RetryAfterSeconds)
}

func TestGrokOAuthServiceRefreshAccountTokenForwardsJWTPrincipal(t *testing.T) {
	client := &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "new-access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	}
	svc := NewGrokOAuthService(nil, client)
	defer svc.Stop()

	info, err := svc.RefreshAccountToken(context.Background(), &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": grokTestJWT(t, map[string]any{
				"sub":            "user-1",
				"principal_type": "User",
				"principal_id":   "principal-1",
			}),
			"refresh_token": "refresh-token",
			"client_id":     "client-id",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "new-access-token", info.AccessToken)
	require.Equal(t, "User", client.principalType)
	require.Equal(t, "principal-1", client.principalID)
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
	require.NoError(t, err)
	require.Equal(t, 1, client.exchangeCalls)

	_, err = svc.ExchangeCode(context.Background(), &GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "replayed-code",
		State:     auth.State,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_SESSION_NOT_FOUND")
	require.Equal(t, 1, client.exchangeCalls)
}

func TestGrokOAuthServiceExchangeCodeRejectsMissingClientWithoutConsumingSession(t *testing.T) {
	svc := NewGrokOAuthService(nil, nil)
	defer svc.Stop()
	auth, err := svc.GenerateAuthURL(context.Background(), nil, "")
	require.NoError(t, err)

	_, err = svc.ExchangeCode(context.Background(), &GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "code",
		State:     auth.State,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_CLIENT_NOT_CONFIGURED")
	_, ok := svc.sessionStore.Get(auth.SessionID)
	require.True(t, ok)
}

func TestGrokOAuthServiceExchangeCodeRequiresStateForBareCode(t *testing.T) {
	client := &grokOAuthClientStub{}
	svc := NewGrokOAuthService(nil, client)
	defer svc.Stop()
	auth, err := svc.GenerateAuthURL(context.Background(), nil, "")
	require.NoError(t, err)

	_, err = svc.ExchangeCode(context.Background(), &GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "bare-authorization-code",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_STATE_REQUIRED")
	require.Zero(t, client.exchangeCalls)
	_, ok := svc.sessionStore.Get(auth.SessionID)
	require.True(t, ok)
}

func TestGrokOAuthServiceExchangeCodeRejectsRedirectURIOverride(t *testing.T) {
	client := &grokOAuthClientStub{}
	svc := NewGrokOAuthService(nil, client)
	defer svc.Stop()
	auth, err := svc.GenerateAuthURL(context.Background(), nil, "")
	require.NoError(t, err)

	_, err = svc.ExchangeCode(context.Background(), &GrokExchangeCodeInput{
		SessionID:   auth.SessionID,
		Code:        "authorization-code",
		State:       auth.State,
		RedirectURI: "http://127.0.0.1:9999/callback",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_REDIRECT_URI_MISMATCH")
	require.Zero(t, client.exchangeCalls)

	_, err = svc.ExchangeCode(context.Background(), &GrokExchangeCodeInput{
		SessionID:   auth.SessionID,
		Code:        "authorization-code",
		State:       auth.State,
		RedirectURI: xai.DefaultRedirectURI,
	})
	require.NoError(t, err)
	require.Equal(t, xai.DefaultRedirectURI, client.exchangeRedirectURI)
}

func TestGrokOAuthServiceExternalFlowsRejectMissingClient(t *testing.T) {
	svc := NewGrokOAuthService(nil, nil)
	defer svc.Stop()

	_, err := svc.RefreshToken(context.Background(), "refresh-token", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_CLIENT_NOT_CONFIGURED")

	_, err = svc.ValidateSSOToken(context.Background(), "sso-token", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_CLIENT_NOT_CONFIGURED")
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

func TestMergeGrokOAuthManagedCredentialsPreservesDeviceOriginButDropsStaleFailure(t *testing.T) {
	merged := MergeGrokOAuthManagedCredentials(
		map[string]any{
			"access_token":                           "old-access",
			"refresh_token":                          "old-refresh",
			"id_token":                               "stale-id-token",
			GrokOAuthFlowCredentialKey:               GrokOAuthFlowDevice,
			GrokOAuthRefreshFailureCodeCredentialKey: "invalid_grant",
			GrokOAuthRefreshFailureAtCredentialKey:   "2026-08-04T00:00:00Z",
			"model_mapping":                          map[string]any{"grok": "grok-4"},
		},
		map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
		},
	)

	require.Equal(t, "new-access", merged["access_token"])
	require.Equal(t, "new-refresh", merged["refresh_token"])
	require.Equal(t, GrokOAuthFlowDevice, merged[GrokOAuthFlowCredentialKey])
	require.NotContains(t, merged, "id_token")
	require.NotContains(t, merged, GrokOAuthRefreshFailureCodeCredentialKey)
	require.NotContains(t, merged, GrokOAuthRefreshFailureAtCredentialKey)
	require.Contains(t, merged, "model_mapping")
}

func TestEnrichGrokOAuthCredentialsBuildsStablePrincipalIdentity(t *testing.T) {
	first := EnrichGrokOAuthCredentials(map[string]any{
		"access_token": grokTestJWT(t, map[string]any{
			"sub":            "user-1",
			"email":          "User@Example.com",
			"team_id":        "team-1",
			"principal_type": "User",
			"principal_id":   "principal-1",
		}),
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
	require.Equal(t, "User", first["principal_type"])
	require.Equal(t, "principal-1", first["principal_id"])
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
