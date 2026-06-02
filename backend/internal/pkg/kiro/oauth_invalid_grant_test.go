package kiro

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRefreshSocialTokenInvalidGrantReturnsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/refreshToken", r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","message":"Invalid refresh token provided"}`))
	}))
	defer server.Close()

	previous := socialAuthEndpointURL
	socialAuthEndpointURL = server.URL
	t.Cleanup(func() { socialAuthEndpointURL = previous })

	_, err := RefreshSocialToken(context.Background(), "", "revoked-refresh-token", "Google")
	require.Error(t, err)

	var invalid *RefreshTokenInvalidError
	require.True(t, errors.As(err, &invalid))
	require.Equal(t, http.StatusBadRequest, invalid.StatusCode)
	require.Contains(t, invalid.Body, "invalid_grant")
}

func TestRefreshIDCTokenInvalidGrantReturnsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/token", r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","message":"Invalid refresh token provided"}`))
	}))
	defer server.Close()

	previous := oidcEndpointOverride
	oidcEndpointOverride = server.URL
	t.Cleanup(func() { oidcEndpointOverride = previous })

	_, err := RefreshIDCToken(context.Background(), "", "client-id", "client-secret", "revoked-refresh-token", "us-east-1", BuilderIDStartURL)
	require.Error(t, err)

	var invalid *RefreshTokenInvalidError
	require.True(t, errors.As(err, &invalid))
	require.Equal(t, http.StatusBadRequest, invalid.StatusCode)
	require.Contains(t, invalid.Body, "invalid_grant")
}

func TestExchangeIDCAuthCodePreservesProfileArn(t *testing.T) {
	const profileArn = "arn:aws:codewhisperer:us-east-1:123456789012:profile/EXCHANGE"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.Contains(t, r.Header.Get("User-Agent"), "api/sso-oidc#")
			require.Contains(t, r.Header.Get("User-Agent"), "KiroIDE")
			require.Contains(t, r.Header.Get("x-amz-user-agent"), "KiroIDE")
			require.NotEmpty(t, r.Header.Get("amz-sdk-invocation-id"))
			require.Equal(t, "attempt=1; max=4", r.Header.Get("amz-sdk-request"))

			var payload map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, map[string]string{
				"clientId":     "client-id",
				"clientSecret": "client-secret",
				"code":         "code",
				"codeVerifier": "verifier",
				"redirectUri":  "http://127.0.0.1:9876/oauth/callback",
				"grantType":    "authorization_code",
			}, payload)

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessToken":"access-token","refreshToken":"refresh-token","profileArn":"` + profileArn + `","expiresIn":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"kiro@example.com"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	previous := oidcEndpointOverride
	oidcEndpointOverride = server.URL
	t.Cleanup(func() { oidcEndpointOverride = previous })

	token, err := ExchangeIDCAuthCode(context.Background(), "", "client-id", "client-secret", "code", "verifier", "http://127.0.0.1:9876/oauth/callback", "us-east-1", BuilderIDStartURL)
	require.NoError(t, err)
	require.Equal(t, profileArn, token.ProfileArn)
	require.Equal(t, "kiro@example.com", token.Email)
}

func TestRefreshIDCTokenPreservesProfileArn(t *testing.T) {
	const profileArn = "arn:aws:codewhisperer:us-east-1:123456789012:profile/REFRESH"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessToken":"access-token","refreshToken":"refresh-token","profileArn":"` + profileArn + `","expiresIn":3600}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"kiro@example.com"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	previous := oidcEndpointOverride
	oidcEndpointOverride = server.URL
	t.Cleanup(func() { oidcEndpointOverride = previous })

	token, err := RefreshIDCToken(context.Background(), "", "client-id", "client-secret", "refresh-token", "us-east-1", BuilderIDStartURL)
	require.NoError(t, err)
	require.Equal(t, profileArn, token.ProfileArn)
	require.Equal(t, "kiro@example.com", token.Email)
}

func TestValidateAuthCodeNotExpiredDetectsExpiredJWTLikeCode(t *testing.T) {
	code := "eyJraWQiOiJrZXktMTU2NDAyODA3OCIsImFsZyI6IkhTMzg0In0.eyJwbGFpbnRleHQiOiI1Z19USklTRG00MlB4MjhYbFFDNndxSkFuZm56eDg4SEdKU1lxRl9rUExBIiwiZXhwIjoxNzgwMzcyMTg4LCJ0eXBlIjoiYXV0aENvZGUifQ.TACeqgd_6ck5znqkDPqbGx-zbtCIo4Ni11ORtNODLDBWszzk_FNQgxcabti25yJW"

	expiresAt, ok := ParseAuthCodeExpiry(code)
	require.True(t, ok)
	require.Equal(t, time.Date(2026, 6, 2, 3, 49, 48, 0, time.UTC), expiresAt.UTC())

	err := ValidateAuthCodeNotExpired(code, expiresAt.Add(time.Second))
	require.Error(t, err)

	var expired *AuthCodeExpiredError
	require.True(t, errors.As(err, &expired))
	require.Equal(t, expiresAt, expired.ExpiresAt)
}

func TestValidateAuthCodeNotExpiredIgnoresOpaqueCode(t *testing.T) {
	require.NoError(t, ValidateAuthCodeNotExpired("opaque-code", time.Now()))
}
