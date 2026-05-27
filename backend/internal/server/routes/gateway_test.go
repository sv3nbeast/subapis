package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newGatewayRoutesTestRouter() *gin.Engine {
	return newGatewayRoutesTestRouterForPlatform(service.PlatformOpenAI, config.ClaudeCodeAuxCompatModeRecord)
}

func newGatewayRoutesTestRouterForPlatform(platform string, auxMode string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			groupID := int64(1)
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				ID:      101,
				UserID:  202,
				GroupID: &groupID,
				User:    &service.User{Email: "user@example.test"},
				Group:   &service.Group{Platform: platform},
			})
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		&config.Config{
			Gateway: config.GatewayConfig{
				ClaudeCodeAuxCompat: config.GatewayClaudeCodeAuxCompatConfig{Mode: auxMode},
			},
		},
	)

	return router
}

func TestGatewayRoutesClaudeCodeAuxCompatRecordMode(t *testing.T) {
	router := newGatewayRoutesTestRouterForPlatform(service.PlatformAnthropic, config.ClaudeCodeAuxCompatModeRecord)

	tests := []struct {
		path       string
		method     string
		wantStatus int
		wantJSON   map[string]any
	}{
		{
			path:       "/v1/mcp_servers?limit=1000",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantJSON:   map[string]any{"data": []any{}, "next_page": nil},
		},
		{
			path:       "/api/claude_code_penguin_mode",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantJSON:   map[string]any{"enabled": false, "disabled_reason": "extra_usage_disabled"},
		},
		{
			path:       "/api/claude_code_grove",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantJSON: map[string]any{
				"grove_enabled":             true,
				"domain_excluded":           false,
				"notice_is_grace_period":    false,
				"notice_reminder_frequency": float64(0),
			},
		},
		{
			path:       "/mcp-registry/v0/servers?version=latest&limit=100",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantJSON: map[string]any{
				"servers": []any{},
				"metadata": map[string]any{
					"count":      float64(0),
					"nextCursor": nil,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{"events":[]}`))
			req.Header.Set("User-Agent", "claude-cli/2.1.111 (external, sdk-cli)")
			req.Header.Set("X-Claude-Code-Session-Id", "123e4567-e89b-12d3-a456-426614174000")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			require.Equal(t, tt.wantStatus, w.Code)
			if tt.wantJSON != nil {
				require.Contains(t, w.Header().Get("Content-Type"), "application/json")
				var got map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
				require.Equal(t, tt.wantJSON, got)
			}
		})
	}
}

func TestGatewayRoutesClaudeCodeAuxCompatBootstrapAndSettingsShape(t *testing.T) {
	router := newGatewayRoutesTestRouterForPlatform(service.PlatformAnthropic, config.ClaudeCodeAuxCompatModeRecord)

	req := httptest.NewRequest(http.MethodGet, "/api/claude_cli/bootstrap", nil)
	req.Header.Set("User-Agent", "claude-code/2.1.111")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var bootstrap map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bootstrap))
	require.Contains(t, bootstrap, "client_data")
	require.Contains(t, bootstrap, "additional_model_options")
	require.Contains(t, bootstrap, "additional_model_costs")
	oauthAccount, ok := bootstrap["oauth_account"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "user@example.test", oauthAccount["account_email"])
	require.Equal(t, "claude_pro", oauthAccount["organization_type"])

	req = httptest.NewRequest(http.MethodGet, "/api/oauth/account/settings", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var settings map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &settings))
	require.Equal(t, true, settings["has_started_claudeai_onboarding"])
	require.Equal(t, true, settings["has_finished_claudeai_onboarding"])
	require.Equal(t, true, settings["enabled_web_search"])
	require.Equal(t, "auto", settings["tool_search_mode"])
	require.Equal(t, true, settings["grove_enabled"])
}

func TestGatewayRoutesClaudeCodeAuxCompatOffMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	groupID := int64(1)

	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				ID:      101,
				UserID:  202,
				GroupID: &groupID,
				Group:   &service.Group{Platform: service.PlatformAnthropic},
			})
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		&config.Config{
			Gateway: config.GatewayConfig{
				ClaudeCodeAuxCompat: config.GatewayClaudeCodeAuxCompatConfig{Mode: config.ClaudeCodeAuxCompatModeOff},
			},
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/claude_code_grove", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "Claude Code auxiliary compatibility is disabled")
}

func TestGatewayRoutesOpenAIResponsesCompactPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/responses/compact",
		"/responses/compact",
		"/backend-api/codex/responses",
		"/backend-api/codex/responses/compact",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI responses handler", path)
	}
}

func TestGatewayRoutesOpenAIImagesPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/images/generations",
		"/images/edits",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-image-2","prompt":"draw a cat"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI images handler", path)
	}
}
