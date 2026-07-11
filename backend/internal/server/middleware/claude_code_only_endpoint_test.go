//go:build unit

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeCodeOnlyEndpointRestrictionForGrokGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		method     string
		path       string
		restricted bool
		wantStatus int
	}{
		{name: "messages allowed for validation", method: http.MethodPost, path: "/v1/messages", restricted: true, wantStatus: http.StatusNoContent},
		{name: "models discovery allowed", method: http.MethodGet, path: "/v1/models", restricted: true, wantStatus: http.StatusNoContent},
		{name: "responses rejected", method: http.MethodPost, path: "/v1/responses", restricted: true, wantStatus: http.StatusForbidden},
		{name: "chat completions rejected", method: http.MethodPost, path: "/v1/chat/completions", restricted: true, wantStatus: http.StatusForbidden},
		{name: "images rejected", method: http.MethodPost, path: "/v1/images/generations", restricted: true, wantStatus: http.StatusForbidden},
		{name: "videos rejected", method: http.MethodPost, path: "/v1/videos/generations", restricted: true, wantStatus: http.StatusForbidden},
		{name: "unrestricted group unchanged", method: http.MethodPost, path: "/v1/responses", restricted: false, wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyAPIKey), &service.APIKey{
					Group: &service.Group{Platform: service.PlatformGrok, ClaudeCodeOnly: tt.restricted},
				})
				c.Next()
			})
			router.Use(ClaudeCodeOnlyEndpointRestriction())
			router.Handle(tt.method, tt.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			router.ServeHTTP(recorder, httptest.NewRequest(tt.method, tt.path, nil))

			require.Equal(t, tt.wantStatus, recorder.Code)
		})
	}
}
