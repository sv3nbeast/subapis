//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestGrokOAuthClientExchangeAndRefreshUseFormFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "client-id", r.Form.Get("client_id"))

		switch r.Form.Get("grant_type") {
		case "authorization_code":
			require.Equal(t, xai.CLIClientVersion, r.Header.Get(xai.CLIClientVersionHdr))
			require.Equal(t, "auth-code", r.Form.Get("code"))
			require.Equal(t, "http://127.0.0.1:56121/callback", r.Form.Get("redirect_uri"))
			require.Equal(t, "verifier", r.Form.Get("code_verifier"))
			require.Empty(t, r.Form.Get("code_challenge"))
			require.Empty(t, r.Form.Get("code_challenge_method"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "exchange-access",
				"refresh_token": "exchange-refresh",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"scope":         "openid api:access",
			})
		case "refresh_token":
			require.Equal(t, "refresh-token", r.Form.Get("refresh_token"))
			require.Equal(t, "User", r.Form.Get("principal_type"))
			require.Equal(t, "principal-1", r.Form.Get("principal_id"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "refresh-access",
				"refresh_token": "refresh-rotated",
				"token_type":    "Bearer",
				"expires_in":    7200,
			})
		default:
			http.Error(w, "unexpected grant_type", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv(xai.EnvTokenURL, server.URL)

	client := NewGrokOAuthClient()
	exchanged, err := client.ExchangeCode(context.Background(), "auth-code", "verifier", "http://127.0.0.1:56121/callback", "", "client-id")
	require.NoError(t, err)
	require.Equal(t, "exchange-access", exchanged.AccessToken)
	require.Equal(t, "exchange-refresh", exchanged.RefreshToken)
	require.Equal(t, int64(3600), exchanged.ExpiresIn)
	require.Equal(t, "openid api:access", exchanged.Scope)

	refreshed, err := client.RefreshToken(context.Background(), "refresh-token", "", "client-id", "User", "principal-1")
	require.NoError(t, err)
	require.Equal(t, "refresh-access", refreshed.AccessToken)
	require.Equal(t, "refresh-rotated", refreshed.RefreshToken)
	require.Equal(t, int64(7200), refreshed.ExpiresIn)
}

func TestGrokOAuthClientDeviceFlowMatchesOfficialContract(t *testing.T) {
	var tokenPolls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		require.Equal(t, xai.CLIClientVersion, r.Header.Get(xai.CLIClientVersionHdr))
		require.Equal(t, xai.DeviceSurfaceUI, r.Header.Get(xai.DeviceSurfaceHeader))

		switch r.URL.Path {
		case "/device":
			require.Equal(t, xai.DefaultClientID, r.Form.Get("client_id"))
			require.Equal(t, xai.DefaultScope, r.Form.Get("scope"))
			require.Equal(t, "grok-build", r.Form.Get("referrer"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":               "device-secret",
				"user_code":                 "ABCD-EFGH",
				"verification_uri":          "https://accounts.x.ai/oauth2/device",
				"verification_uri_complete": "https://accounts.x.ai/oauth2/device?user_code=ABCD-EFGH",
				"interval":                  5,
				"expires_in":                1800,
			})
		case "/token":
			tokenPolls++
			require.Equal(t, xai.DeviceGrantType, r.Form.Get("grant_type"))
			require.Equal(t, xai.DefaultClientID, r.Form.Get("client_id"))
			require.Equal(t, "device-secret", r.Form.Get("device_code"))
			if tokenPolls == 1 {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending","error_description":"waiting"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "device-access",
				"refresh_token": "device-refresh",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	t.Setenv(xai.EnvDeviceURL, server.URL+"/device")
	t.Setenv(xai.EnvTokenURL, server.URL+"/token")

	client := NewGrokOAuthClient()
	device, err := client.StartDeviceAuthorization(context.Background(), "", "", "")
	require.NoError(t, err)
	require.Equal(t, "ABCD-EFGH", device.UserCode)

	_, err = client.PollDeviceAuthorization(context.Background(), device.DeviceCode, "", "")
	var pending *xai.DeviceAuthorizationError
	require.ErrorAs(t, err, &pending)
	require.Equal(t, "authorization_pending", pending.Code)
	require.Equal(t, 7*time.Second, pending.RetryAfter)

	token, err := client.PollDeviceAuthorization(context.Background(), device.DeviceCode, "", "")
	require.NoError(t, err)
	require.Equal(t, "device-access", token.AccessToken)
	require.Equal(t, "device-refresh", token.RefreshToken)
}

func TestGrokOAuthClientDeviceFlowRejectsInvalidVerificationURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "device-secret",
			"user_code":        "ABCD-EFGH",
			"verification_uri": "javascript:alert(1)",
			"expires_in":       1800,
		})
	}))
	defer server.Close()
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	t.Setenv(xai.EnvDeviceURL, server.URL)

	client := NewGrokOAuthClient()
	_, err := client.StartDeviceAuthorization(context.Background(), "", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_DEVICE_VERIFICATION_URL_INVALID")
}

func TestGrokOAuthClientRefreshForbiddenClassifiesOnlyExplicitEntitlement(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantReason string
	}{
		{name: "explicit entitlement", body: `{"error":"access_denied"}`, wantReason: "GROK_OAUTH_ENTITLEMENT_DENIED"},
		{name: "generic forbidden", body: `{"error":"forbidden"}`, wantReason: "GROK_OAUTH_TOKEN_REFRESH_FAILED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			t.Setenv(xai.EnvTokenURL, server.URL)

			client := NewGrokOAuthClient()
			_, err := client.RefreshToken(context.Background(), "refresh-token", "", "client-id", "", "")
			require.Error(t, err)
			require.Contains(t, strings.ToUpper(err.Error()), tt.wantReason)
		})
	}
}

func TestGrokOAuthClientStatusErrorRedactsSensitiveResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","access_token":"access-secret","refresh_token":"refresh-secret","code_verifier":"verifier-secret"}`))
	}))
	defer server.Close()
	t.Setenv(xai.EnvTokenURL, server.URL)

	client := NewGrokOAuthClient()
	_, err := client.RefreshToken(context.Background(), "refresh-secret", "", "client-id", "", "")
	require.Error(t, err)

	errText := err.Error()
	require.Contains(t, errText, "status 400")
	require.Contains(t, errText, `\"refresh_token\":\"***\"`)
	require.NotContains(t, errText, "access-secret")
	require.NotContains(t, errText, "refresh-secret")
	require.NotContains(t, errText, "verifier-secret")
}

func TestGrokOAuthEntitlementDenialRequiresExplicitEvidence(t *testing.T) {
	t.Parallel()

	require.True(t, grokOAuthHasExplicitEntitlementDenial(`{"error":"access_denied"}`))
	require.True(t, grokOAuthHasExplicitEntitlementDenial(`{"code":"entitlement_denied"}`))
	require.True(t, grokOAuthHasExplicitEntitlementDenial(`{"message":"no active Grok subscription"}`))
	require.False(t, grokOAuthHasExplicitEntitlementDenial(`{"error":"forbidden","message":"request forbidden"}`))
	require.False(t, grokOAuthHasExplicitEntitlementDenial(`<html>403 Forbidden</html>`))
}
