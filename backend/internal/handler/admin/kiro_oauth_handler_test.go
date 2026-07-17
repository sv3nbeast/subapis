package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKiroOAuthHandlerRefreshTokenPreservesExternalIDPMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotScope string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotScope = r.Form.Get("scope")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access-token","expires_in":1800}`))
	}))
	defer tokenServer.Close()

	body := []byte(`{
		"refresh_token":"refresh-token",
		"auth_method":"external_idp",
		"provider":"ExternalIdp",
		"client_id":"client-id",
		"profile_arn":"profile-arn",
		"issuer_url":"https://login.microsoftonline.com/tenant/v2.0",
		"token_endpoint":"` + tokenServer.URL + `",
		"scopes":"openid offline_access"
	}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/admin/kiro/oauth/refresh-token", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler := NewKiroOAuthHandler(service.NewKiroOAuthService(nil))
	handler.RefreshToken(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "openid offline_access", gotScope)
	require.Contains(t, recorder.Body.String(), `"token_endpoint":"`+tokenServer.URL+`"`)
	require.Contains(t, recorder.Body.String(), `"issuer_url":"https://login.microsoftonline.com/tenant/v2.0"`)
}
