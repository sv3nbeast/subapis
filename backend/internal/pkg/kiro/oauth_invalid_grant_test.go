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

func TestRegisterIDCClientIncludesDeviceCodeGrant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/client/register", r.URL.Path)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Contains(t, payload["grantTypes"], "urn:ietf:params:oauth:grant-type:device_code")
		require.Contains(t, payload["grantTypes"], "authorization_code")
		require.Contains(t, payload["grantTypes"], "refresh_token")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"clientId":"client-id","clientSecret":"client-secret"}`))
	}))
	defer server.Close()

	previous := oidcEndpointOverride
	oidcEndpointOverride = server.URL
	t.Cleanup(func() { oidcEndpointOverride = previous })

	reg, err := RegisterIDCClient(context.Background(), "", "http://127.0.0.1:9876/oauth/callback", BuilderIDStartURL, "us-east-1")
	require.NoError(t, err)
	require.Equal(t, "client-id", reg.ClientID)
}

func TestStartIDCDeviceAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/device_authorization", r.URL.Path)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, map[string]string{
			"clientId":     "client-id",
			"clientSecret": "client-secret",
			"startUrl":     BuilderIDStartURL,
		}, payload)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deviceCode":"device-code","userCode":"ABCD-EFGH","verificationUri":"https://device.sso.us-east-1.amazonaws.com/","verificationUriComplete":"https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD-EFGH","expiresIn":600,"interval":5}`))
	}))
	defer server.Close()

	previous := oidcEndpointOverride
	oidcEndpointOverride = server.URL
	t.Cleanup(func() { oidcEndpointOverride = previous })

	device, err := StartIDCDeviceAuthorization(context.Background(), "", "client-id", "client-secret", BuilderIDStartURL, "us-east-1")
	require.NoError(t, err)
	require.Equal(t, "device-code", device.DeviceCode)
	require.Equal(t, "ABCD-EFGH", device.UserCode)
	require.Equal(t, "https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD-EFGH", device.VerificationURIComplete)
}

func TestExchangeIDCDeviceCodePreservesProfileArn(t *testing.T) {
	const profileArn = "arn:aws:codewhisperer:us-east-1:123456789012:profile/DEVICE"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			var payload map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, map[string]string{
				"clientId":     "client-id",
				"clientSecret": "client-secret",
				"deviceCode":   "device-code",
				"grantType":    "urn:ietf:params:oauth:grant-type:device_code",
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

	token, err := ExchangeIDCDeviceCode(context.Background(), "", "client-id", "client-secret", "device-code", "us-east-1", BuilderIDStartURL)
	require.NoError(t, err)
	require.Equal(t, profileArn, token.ProfileArn)
	require.Equal(t, "kiro@example.com", token.Email)
}

func TestExchangeIDCDeviceCodeAcceptsSnakeCaseTokenResponse(t *testing.T) {
	const profileArn = "arn:aws:codewhisperer:us-east-1:123456789012:profile/SNAKE"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access-token","refresh_token":"refresh-token","profile_arn":"` + profileArn + `","expires_in":3600}`))
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

	token, err := ExchangeIDCDeviceCode(context.Background(), "", "client-id", "client-secret", "device-code", "us-east-1", BuilderIDStartURL)
	require.NoError(t, err)
	require.Equal(t, "access-token", token.AccessToken)
	require.Equal(t, "refresh-token", token.RefreshToken)
	require.Equal(t, profileArn, token.ProfileArn)
	require.Equal(t, "kiro@example.com", token.Email)
}

func TestExchangeIDCDeviceCodeRejectsMissingTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"expires_in":3600}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	previous := oidcEndpointOverride
	oidcEndpointOverride = server.URL
	t.Cleanup(func() { oidcEndpointOverride = previous })

	token, err := ExchangeIDCDeviceCode(context.Background(), "", "client-id", "client-secret", "device-code", "us-east-1", BuilderIDStartURL)
	require.Error(t, err)
	require.Nil(t, token)
	require.Contains(t, err.Error(), "missing access token")
}

func TestExchangeIDCAuthCodeUnwrapsJWTPlaintextCode(t *testing.T) {
	const wrappedCode = "eyJraWQiOiJrZXktMTU2NDAyODA3OCIsImFsZyI6IkhTMzg0In0.eyJwbGFpbnRleHQiOiJaZ3pVWC1xbXhaQ09vRWl2QThTYmI1am81cGR4bk1tZmdWekYyNnhoRUhnIiwiZXhwIjoxNzgwMzk5NTQ5LCJ0eXBlIjoiYXV0aENvZGUifQ.Uj7PTQ4lvIu8IEy9Jdgv8Ipoifsu8CA5qu5Xp35CSMBhIwdrrshIY33InFh8eSX4"
	const plaintextCode = "ZgzUX-qmxZCOoEivA8Sbb5jo5pdxnMmfgVzF26xhEHg"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			var payload map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, plaintextCode, payload["code"])
			require.NotEqual(t, wrappedCode, payload["code"])

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

	previous := oidcEndpointOverride
	oidcEndpointOverride = server.URL
	t.Cleanup(func() { oidcEndpointOverride = previous })

	token, err := ExchangeIDCAuthCode(context.Background(), "", "client-id", "client-secret", wrappedCode, "verifier", "http://127.0.0.1:9876/oauth/callback", "us-east-1", BuilderIDStartURL)
	require.NoError(t, err)
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

func TestRefreshIDCTokenAcceptsSnakeCaseTokenResponse(t *testing.T) {
	const profileArn = "arn:aws:codewhisperer:us-east-1:123456789012:profile/REFRESH-SNAKE"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","profile_arn":"` + profileArn + `","expires_in":3600}`))
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
	require.Equal(t, "new-access", token.AccessToken)
	require.Equal(t, "new-refresh", token.RefreshToken)
	require.Equal(t, profileArn, token.ProfileArn)
	require.Equal(t, "kiro@example.com", token.Email)
}

func TestRefreshIDCTokenAllowsMissingRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3600}`))
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

	token, err := RefreshIDCToken(context.Background(), "", "client-id", "client-secret", "old-refresh-token", "us-east-1", BuilderIDStartURL)
	require.NoError(t, err)
	require.Equal(t, "new-access", token.AccessToken)
	require.Empty(t, token.RefreshToken)
	require.Equal(t, "kiro@example.com", token.Email)
}

func TestValidateAuthCodeNotExpiredDetectsExpiredJWTLikeCode(t *testing.T) {
	code := "eyJraWQiOiJrZXktMTU2NDAyODA3OCIsImFsZyI6IkhTMzg0In0.eyJwbGFpbnRleHQiOiI1Z19USklTRG00MlB4MjhYbFFDNndxSkFuZm56eDg4SEdKU1lxRl9rUExBIiwiZXhwIjoxNzgwMzcyMTg4LCJ0eXBlIjoiYXV0aENvZGUifQ.TACeqgd_6ck5znqkDPqbGx-zbtCIo4Ni11ORtNODLDBWszzk_FNQgxcabti25yJW"

	expiresAt, ok := ParseAuthCodeExpiry(code)
	require.True(t, ok)
	require.Equal(t, time.Date(2026, 6, 2, 3, 49, 48, 0, time.UTC), expiresAt.UTC())
	plaintext, ok := ParseAuthCodePlaintext(code)
	require.True(t, ok)
	require.Equal(t, "5g_TJISDm42Px28XlQC6wqJAnfnzx88HGJSYqF_kPLA", plaintext)
	require.Equal(t, plaintext, ResolveAuthCodeForTokenExchange(code))

	err := ValidateAuthCodeNotExpired(code, expiresAt.Add(time.Second))
	require.Error(t, err)

	var expired *AuthCodeExpiredError
	require.True(t, errors.As(err, &expired))
	require.Equal(t, expiresAt, expired.ExpiresAt)
}

func TestValidateAuthCodeNotExpiredIgnoresOpaqueCode(t *testing.T) {
	require.NoError(t, ValidateAuthCodeNotExpired("opaque-code", time.Now()))
}
