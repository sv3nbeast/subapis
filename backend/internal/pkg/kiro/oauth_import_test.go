package kiro

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseImportedTokenInfersIDCAuthMetadataFromClientCredentials(t *testing.T) {
	token, err := ParseImportedToken(`{
		"accessToken": "access-token",
		"refreshToken": "refresh-token",
		"provider": "BuilderId",
		"clientId": "client-id",
		"clientSecret": "client-secret"
	}`, "")
	require.NoError(t, err)
	require.Equal(t, "idc", token.AuthMethod)
	require.Equal(t, ProviderBuilderId, token.Provider)
	require.Equal(t, defaultIDCRegion, token.Region)
}

func TestParseImportedTokenAcceptsCLIProxyAPIExternalIDPFormat(t *testing.T) {
	token, err := ParseImportedToken(`{
		"access_token": "access-token",
		"refresh_token": "refresh-token",
		"auth_method": "external_idp",
		"client_id": "client-id",
		"expired": "2026-07-08T06:13:47Z",
		"issuer_url": "https://login.microsoftonline.com/example/v2.0",
		"profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/PROFILEID",
		"region": "us-east-1",
		"scopes": "api://client-id/codewhisperer:conversations offline_access",
		"token_endpoint": "https://login.microsoftonline.com/example/oauth2/v2.0/token",
		"type": "kiro"
	}`, "")
	require.NoError(t, err)
	require.Equal(t, "access-token", token.AccessToken)
	require.Equal(t, "refresh-token", token.RefreshToken)
	require.Equal(t, "external_idp", token.AuthMethod)
	require.Equal(t, ProviderEnterprise, token.Provider)
	require.Equal(t, "client-id", token.ClientID)
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/PROFILEID", token.ProfileArn)
	require.Equal(t, "https://login.microsoftonline.com/example/v2.0", token.IssuerURL)
	require.Equal(t, "https://login.microsoftonline.com/example/oauth2/v2.0/token", token.TokenEndpoint)
	require.Equal(t, "api://client-id/codewhisperer:conversations offline_access", token.Scopes)
	_, err = time.Parse(time.RFC3339, token.ExpiresAt)
	require.NoError(t, err)
}

func TestRefreshExternalIDPTokenUsesFormRefreshGrant(t *testing.T) {
	var gotContentType string
	var gotGrantType string
	var gotClientID string
	var gotRefreshToken string
	var gotScope string
	var gotMethod string
	var parseErr error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		parseErr = r.ParseForm()
		gotGrantType = r.Form.Get("grant_type")
		gotClientID = r.Form.Get("client_id")
		gotRefreshToken = r.Form.Get("refresh_token")
		gotScope = r.Form.Get("scope")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access-token","expires_in":1800}`))
	}))
	defer server.Close()

	token, err := RefreshExternalIDPToken(
		context.Background(),
		"",
		"old-refresh-token",
		"client-id",
		"",
		server.URL,
		"scope-a offline_access",
		"us-east-1",
		"profile-arn",
		"https://issuer.example/v2.0",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, gotMethod)
	require.NoError(t, parseErr)
	require.Contains(t, gotContentType, "application/x-www-form-urlencoded")
	require.Equal(t, "refresh_token", gotGrantType)
	require.Equal(t, "client-id", gotClientID)
	require.Equal(t, "old-refresh-token", gotRefreshToken)
	require.Equal(t, "scope-a offline_access", gotScope)
	require.Equal(t, "new-access-token", token.AccessToken)
	require.Equal(t, "old-refresh-token", token.RefreshToken)
	require.Equal(t, "profile-arn", token.ProfileArn)
	require.Equal(t, "external_idp", token.AuthMethod)
	require.Equal(t, ProviderEnterprise, token.Provider)
	require.Equal(t, server.URL, token.TokenEndpoint)
}

func TestParseImportedTokenInfersIDCAuthMetadataFromDeviceRegistration(t *testing.T) {
	token, err := ParseImportedToken(`{
		"accessToken": "access-token",
		"refreshToken": "refresh-token",
		"provider": "Enterprise",
		"clientIdHash": "client-id-hash"
	}`, `{
		"clientId": "client-id",
		"clientSecret": "client-secret"
	}`)
	require.NoError(t, err)
	require.Equal(t, "client-id", token.ClientID)
	require.Equal(t, "client-secret", token.ClientSecret)
	require.Equal(t, "idc", token.AuthMethod)
	require.Equal(t, defaultIDCRegion, token.Region)
}

func TestParseImportedTokenNormalizesLegacyAWSProviderForIDC(t *testing.T) {
	token, err := ParseImportedToken(`{
		"accessToken": "access-token",
		"refreshToken": "refresh-token",
		"authMethod": "idc",
		"provider": "AWS",
		"clientId": "client-id",
		"clientSecret": "client-secret",
		"startUrl": "https://d-9066029b12.awsapps.com/start/"
	}`, "")
	require.NoError(t, err)
	require.Equal(t, ProviderEnterprise, token.Provider)
}

func TestParseImportedTokenRejectsMissingOrInvalidProvider(t *testing.T) {
	cases := []struct {
		name      string
		tokenJSON string
	}{
		{
			name:      "missing provider",
			tokenJSON: `{"accessToken":"access-token","refreshToken":"refresh-token","authMethod":"social"}`,
		},
		{
			name:      "empty provider",
			tokenJSON: `{"accessToken":"access-token","provider":"","authMethod":"social"}`,
		},
		{
			name:      "legacy AWS social provider rejected",
			tokenJSON: `{"accessToken":"access-token","provider":"AWS","authMethod":"social"}`,
		},
		{
			name:      "unknown provider",
			tokenJSON: `{"accessToken":"access-token","provider":"Gitlab","authMethod":"social"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseImportedToken(tc.tokenJSON, "")
			require.Error(t, err)
		})
	}
}

func TestParseImportedTokenAcceptsWhitelistedProviders(t *testing.T) {
	for _, provider := range []string{ProviderGoogle, ProviderGithub} {
		token, err := ParseImportedToken(`{
			"accessToken": "access-token",
			"refreshToken": "refresh-token",
			"authMethod": "social",
			"provider": "`+provider+`"
		}`, "")
		require.NoError(t, err)
		require.Equal(t, provider, token.Provider)
	}
}

func TestParseImportedTokenNormalizesExpiresAt(t *testing.T) {
	cases := []struct {
		name      string
		expiresAt string
	}{
		{"utc with millis", "2026-06-29T09:33:49.114Z"},
		{"utc no millis", "2026-06-29T09:33:49Z"},
		{"naive treated as utc", "2026-09-27T08:46:31.070"},
		{"with offset", "2026-06-29T16:56:19+08:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, err := ParseImportedToken(`{
				"accessToken": "access-token",
				"authMethod": "social",
				"provider": "Google",
				"expiresAt": "`+tc.expiresAt+`"
			}`, "")
			require.NoError(t, err)
			parsed, err := time.Parse(time.RFC3339, token.ExpiresAt)
			require.NoError(t, err)
			require.Equal(t, parsed.Local().Format(time.RFC3339), token.ExpiresAt)
		})
	}
}

func TestParseImportedTokenRejectsInvalidExpiresAt(t *testing.T) {
	_, err := ParseImportedToken(`{
		"accessToken": "access-token",
		"authMethod": "social",
		"provider": "Google",
		"expiresAt": "not-a-time"
	}`, "")
	require.Error(t, err)
}
