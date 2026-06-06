//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	droidpkg "github.com/Wei-Shaw/sub2api/internal/pkg/droid"
	"github.com/stretchr/testify/require"
)

func TestDroidOAuthServiceGenerateAuthURLUsesDeviceAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/user_management/authorize/device", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"device-code","user_code":"ABCD-EFGH","verification_uri":"https://device.example/","verification_uri_complete":"https://device.example/?user_code=ABCD-EFGH","expires_in":600,"interval":5}`))
	}))
	defer server.Close()

	oldAuthorize := droidpkg.WorkOSDeviceAuthorizeURL
	t.Cleanup(func() {
		droidpkg.WorkOSDeviceAuthorizeURL = oldAuthorize
	})
	droidpkg.WorkOSDeviceAuthorizeURL = server.URL + "/user_management/authorize/device"

	svc := NewDroidOAuthService(nil)
	result, err := svc.GenerateAuthURL(context.Background(), &DroidGenerateAuthURLInput{})
	require.NoError(t, err)
	require.Equal(t, "ABCD-EFGH", result.UserCode)
	require.Equal(t, "https://device.example/?user_code=ABCD-EFGH", result.VerificationURIComplete)
	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)
	require.Equal(t, "device-code", session.DeviceCode)
}

func TestDroidOAuthServiceExchangeCodePending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/user_management/authenticate", r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"authorization_pending","error_description":"pending","interval":7}`))
	}))
	defer server.Close()

	oldToken := droidpkg.WorkOSTokenURL
	t.Cleanup(func() {
		droidpkg.WorkOSTokenURL = oldToken
	})
	droidpkg.WorkOSTokenURL = server.URL + "/user_management/authenticate"

	svc := NewDroidOAuthService(nil)
	svc.sessionStore.Set("session-id", &droidpkg.DeviceAuthSession{
		State:      "session-id",
		DeviceCode: "device-code",
		CreatedAt:  time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &DroidExchangeCodeInput{SessionID: "session-id"})
	require.Error(t, err)
	authErr, ok := err.(*droidpkg.DeviceAuthError)
	require.True(t, ok)
	require.Equal(t, "authorization_pending", authErr.Code)
	require.Equal(t, 7, authErr.RetryAfter)
}
