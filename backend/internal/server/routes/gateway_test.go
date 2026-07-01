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

func assertRouteJSONTokenOrder(t *testing.T, body string, tokens ...string) {
	t.Helper()

	last := -1
	for _, token := range tokens {
		pos := strings.Index(body, token)
		require.NotEqualf(t, -1, pos, "missing token %s in body %s", token, body)
		require.Greaterf(t, pos, last, "token %s should appear after previous tokens in body %s", token, body)
		last = pos
	}
}

func newGatewayRoutesTestRouter(platformOverride ...string) *gin.Engine {
	platform := service.PlatformOpenAI
	if len(platformOverride) > 0 {
		platform = platformOverride[0]
	}
	return newGatewayRoutesTestRouterForPlatform(platform, config.ClaudeCodeAuxCompatModeRecord)
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
			path:       "/api/claude_code/settings",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantJSON: map[string]any{
				"uuid":     "d3642035-4f89-4d00-8c6d-45e3ec9cc28a",
				"checksum": "sha256:f3bc73acb96c25445a9a56726132f88b353aa50cfff5bd5f4e59ce5f9b664120",
				"settings": map[string]any{
					"channelsEnabled": true,
				},
			},
		},
		{
			path:       "/api/claude_code/policy_limits",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantJSON: map[string]any{
				"restrictions": map[string]any{
					"allow_cobalt_plinth": map[string]any{
						"allowed": false,
					},
					"enforce_web_search_mcp_isolation": map[string]any{
						"allowed": false,
					},
				},
				"compliance_taints": []any{},
			},
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
			path:       "/api/oauth/profile",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantJSON: map[string]any{
				"account": map[string]any{
					"uuid":           "",
					"full_name":      "",
					"display_name":   "",
					"email":          "user@example.test",
					"has_claude_max": false,
					"has_claude_pro": false,
					"created_at":     nil,
				},
				"organization": map[string]any{
					"uuid":                            "",
					"name":                            "user@example.test's Organization",
					"organization_type":               "claude_pro",
					"billing_type":                    "",
					"rate_limit_tier":                 "default_claude_ai",
					"seat_tier":                       nil,
					"has_extra_usage_enabled":         false,
					"subscription_status":             "",
					"subscription_created_at":         nil,
					"cc_onboarding_flags":             map[string]any{},
					"claude_code_trial_ends_at":       nil,
					"claude_code_trial_duration_days": nil,
					"payment_auth_hosted_invoice_url": nil,
				},
				"application": map[string]any{
					"uuid": "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
					"name": "Claude Code",
					"slug": "claude-code",
				},
				"enabled_plugins": []any{},
			},
		},
		{
			path:       "/api/oauth/claude_cli/roles",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantJSON: map[string]any{
				"organization_uuid": "",
				"organization_name": "user@example.test's Organization",
				"organization_role": "user",
				"workspace_uuid":    nil,
				"workspace_name":    nil,
				"workspace_role":    nil,
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

func TestGatewayRoutesClaudeCodeAuxCompatCapturedProfileOrder(t *testing.T) {
	router := newGatewayRoutesTestRouterForPlatform(service.PlatformAnthropic, config.ClaudeCodeAuxCompatModeRecord)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/profile", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	assertRouteJSONTokenOrder(t, body, `"account"`, `"organization"`, `"application"`, `"enabled_plugins"`)
	assertRouteJSONTokenOrder(t, body, `"uuid"`, `"full_name"`, `"display_name"`, `"email"`, `"has_claude_max"`, `"has_claude_pro"`, `"created_at"`)
	assertRouteJSONTokenOrder(t, body, `"organization_type"`, `"billing_type"`, `"rate_limit_tier"`, `"seat_tier"`, `"has_extra_usage_enabled"`, `"subscription_status"`, `"subscription_created_at"`, `"cc_onboarding_flags"`, `"claude_code_trial_ends_at"`, `"claude_code_trial_duration_days"`, `"payment_auth_hosted_invoice_url"`)
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

func TestGatewayRoutesGrokImagesAndVideosPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformGrok)

	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/images/generations",
		"/images/edits",
		"/v1/videos/generations",
		"/videos/generations",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"grok-imagine","prompt":"draw a cat"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit Grok media handler", path)
		require.NotContains(t, w.Body.String(), "not supported for this platform")
	}

	for _, path := range []string{
		"/v1/videos/request-123",
		"/videos/request-123",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit Grok video handler", path)
		require.NotContains(t, w.Body.String(), "not supported for this platform")
	}
}

func TestGatewayRoutesNonGrokVideosAreRejectedAtPlatformGate(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformOpenAI)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/videos/generations", `{"model":"grok-imagine-video-1.5","prompt":"waves"}`},
		{http.MethodPost, "/videos/generations", `{"model":"grok-imagine-video-1.5","prompt":"waves"}`},
		{http.MethodGet, "/v1/videos/request-123", ""},
		{http.MethodGet, "/videos/request-123", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "method=%s path=%s", tc.method, tc.path)
		require.Contains(t, w.Body.String(), "Videos API is not supported for this platform")
	}
}

func TestGatewayRoutesGrokAllowsCLICompatibilityEntrypoints(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformGrok)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/messages"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/chat/completions"},
		{http.MethodGet, "/v1/responses"},
		{http.MethodGet, "/responses"},
		{http.MethodGet, "/backend-api/codex/responses"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"grok"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "method=%s path=%s", tc.method, tc.path)
		require.NotContains(t, w.Body.String(), "not supported for Grok groups")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"grok","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "Token counting is not supported for this platform")

	for _, path := range []string{
		"/v1/responses",
		"/responses",
		"/backend-api/codex/responses",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"grok","input":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should still reach Responses handler", path)
	}
}

func TestGatewayRoutesOpenAICountTokensPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformOpenAI)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	require.NotEqual(t, http.StatusNotFound, w.Code)
}
