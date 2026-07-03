package kiro

import (
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
