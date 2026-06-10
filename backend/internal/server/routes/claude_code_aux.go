package routes

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func registerClaudeCodeAuxCompatRoutes(
	r *gin.Engine,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	requireGroupAnthropic gin.HandlerFunc,
	cfg *config.Config,
) {
	aux := r.Group("")
	aux.Use(gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic)
	{
		aux.GET("/v1/mcp_servers", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxMCPServers))
		aux.GET("/api/claude_cli/bootstrap", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxBootstrap))
		aux.GET("/api/claude_code/settings", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxClaudeCodeSettings))
		aux.GET("/api/claude_code/policy_limits", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxPolicyLimits))
		aux.GET("/api/claude_code_penguin_mode", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxPenguinMode))
		aux.GET("/api/claude_code_grove", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxGrove))
		aux.GET("/api/oauth/profile", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxOAuthProfile))
		aux.GET("/api/oauth/claude_cli/roles", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxClaudeCLIRoles))
		aux.GET("/api/oauth/account/settings", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxAccountSettings))
		aux.GET("/mcp-registry/v0/servers", claudeCodeAuxCompatHandler(cfg, claudeCodeAuxMCPRegistry))
	}
}

type claudeCodeAuxEndpoint string

const (
	claudeCodeAuxMCPServers         claudeCodeAuxEndpoint = "mcp_servers"
	claudeCodeAuxBootstrap          claudeCodeAuxEndpoint = "claude_cli_bootstrap"
	claudeCodeAuxClaudeCodeSettings claudeCodeAuxEndpoint = "claude_code_settings"
	claudeCodeAuxPolicyLimits       claudeCodeAuxEndpoint = "claude_code_policy_limits"
	claudeCodeAuxPenguinMode        claudeCodeAuxEndpoint = "claude_code_penguin_mode"
	claudeCodeAuxGrove              claudeCodeAuxEndpoint = "claude_code_grove"
	claudeCodeAuxOAuthProfile       claudeCodeAuxEndpoint = "oauth_profile"
	claudeCodeAuxClaudeCLIRoles     claudeCodeAuxEndpoint = "oauth_claude_cli_roles"
	claudeCodeAuxAccountSettings    claudeCodeAuxEndpoint = "oauth_account_settings"
	claudeCodeAuxMCPRegistry        claudeCodeAuxEndpoint = "mcp_registry_servers"
)

func claudeCodeAuxCompatHandler(cfg *config.Config, endpoint claudeCodeAuxEndpoint) gin.HandlerFunc {
	return func(c *gin.Context) {
		mode := claudeCodeAuxCompatMode(cfg)
		if mode == config.ClaudeCodeAuxCompatModeOff {
			c.JSON(http.StatusNotFound, gin.H{
				"type":  "error",
				"error": gin.H{"type": "not_found_error", "message": "Claude Code auxiliary compatibility is disabled"},
			})
			return
		}

		logClaudeCodeAuxCompat(c, endpoint, mode)
		writeClaudeCodeAuxCompatResponse(c, endpoint)
	}
}

func claudeCodeAuxCompatMode(cfg *config.Config) string {
	if cfg == nil {
		return config.ClaudeCodeAuxCompatModeRecord
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Gateway.ClaudeCodeAuxCompat.Mode))
	switch mode {
	case "", config.ClaudeCodeAuxCompatModeRecord:
		return config.ClaudeCodeAuxCompatModeRecord
	case config.ClaudeCodeAuxCompatModeOff:
		return config.ClaudeCodeAuxCompatModeOff
	case config.ClaudeCodeAuxCompatModeForward:
		// Reserved for a future explicit forward implementation. Today this is
		// intentionally record-compatible so telemetry-like traffic is not sent upstream.
		return config.ClaudeCodeAuxCompatModeForward
	default:
		return config.ClaudeCodeAuxCompatModeRecord
	}
}

func writeClaudeCodeAuxCompatResponse(c *gin.Context, endpoint claudeCodeAuxEndpoint) {
	switch endpoint {
	case claudeCodeAuxMCPServers:
		c.JSON(http.StatusOK, gin.H{"data": []any{}, "next_page": nil})
	case claudeCodeAuxBootstrap:
		c.JSON(http.StatusOK, claudeCodeAuxBootstrapResponse(c))
	case claudeCodeAuxClaudeCodeSettings:
		c.Header("Cache-Control", "private, max-age=60")
		c.JSON(http.StatusOK, claudeCodeAuxClaudeCodeSettingsResponse())
	case claudeCodeAuxPolicyLimits:
		c.Header("Cache-Control", "max-age=3600")
		c.JSON(http.StatusOK, claudeCodeAuxPolicyLimitsResponse())
	case claudeCodeAuxPenguinMode:
		c.JSON(http.StatusOK, gin.H{"enabled": false, "disabled_reason": "extra_usage_disabled"})
	case claudeCodeAuxGrove:
		c.JSON(http.StatusOK, gin.H{
			"grove_enabled":             true,
			"domain_excluded":           false,
			"notice_is_grace_period":    false,
			"notice_reminder_frequency": 0,
		})
	case claudeCodeAuxOAuthProfile:
		c.JSON(http.StatusOK, claudeCodeAuxOAuthProfileResponse(c))
	case claudeCodeAuxClaudeCLIRoles:
		c.JSON(http.StatusOK, claudeCodeAuxClaudeCLIRolesResponse(c))
	case claudeCodeAuxAccountSettings:
		c.JSON(http.StatusOK, claudeCodeAuxAccountSettingsResponse())
	case claudeCodeAuxMCPRegistry:
		c.JSON(http.StatusOK, gin.H{
			"servers": []any{},
			"metadata": gin.H{
				"count":      0,
				"nextCursor": nil,
			},
		})
	default:
		c.JSON(http.StatusOK, gin.H{})
	}
}

func claudeCodeAuxClaudeCodeSettingsResponse() any {
	type settingsPayload struct {
		ChannelsEnabled bool `json:"channelsEnabled"`
	}
	type response struct {
		UUID     string          `json:"uuid"`
		Checksum string          `json:"checksum"`
		Settings settingsPayload `json:"settings"`
	}
	return response{
		UUID:     "d3642035-4f89-4d00-8c6d-45e3ec9cc28a",
		Checksum: "sha256:f3bc73acb96c25445a9a56726132f88b353aa50cfff5bd5f4e59ce5f9b664120",
		Settings: settingsPayload{
			ChannelsEnabled: true,
		},
	}
}

func claudeCodeAuxPolicyLimitsResponse() any {
	type allowedRestriction struct {
		Allowed bool `json:"allowed"`
	}
	type restrictionsPayload struct {
		AllowCobaltPlinth            allowedRestriction `json:"allow_cobalt_plinth"`
		EnforceWebSearchMCPIsolation allowedRestriction `json:"enforce_web_search_mcp_isolation"`
	}
	type response struct {
		Restrictions     restrictionsPayload `json:"restrictions"`
		ComplianceTaints []any               `json:"compliance_taints"`
	}
	return response{
		Restrictions: restrictionsPayload{
			AllowCobaltPlinth:            allowedRestriction{Allowed: false},
			EnforceWebSearchMCPIsolation: allowedRestriction{Allowed: false},
		},
		ComplianceTaints: []any{},
	}
}

func claudeCodeAuxOAuthProfileResponse(c *gin.Context) any {
	accountEmail := ""
	if apiKey, ok := middleware.GetAPIKeyFromContext(c); ok && apiKey != nil && apiKey.User != nil {
		accountEmail = strings.TrimSpace(apiKey.User.Email)
	}
	type accountPayload struct {
		UUID         string `json:"uuid"`
		FullName     string `json:"full_name"`
		DisplayName  string `json:"display_name"`
		Email        string `json:"email"`
		HasClaudeMax bool   `json:"has_claude_max"`
		HasClaudePro bool   `json:"has_claude_pro"`
		CreatedAt    any    `json:"created_at"`
	}
	type organizationPayload struct {
		UUID                        string         `json:"uuid"`
		Name                        string         `json:"name"`
		OrganizationType            string         `json:"organization_type"`
		BillingType                 string         `json:"billing_type"`
		RateLimitTier               string         `json:"rate_limit_tier"`
		SeatTier                    any            `json:"seat_tier"`
		HasExtraUsageEnabled        bool           `json:"has_extra_usage_enabled"`
		SubscriptionStatus          string         `json:"subscription_status"`
		SubscriptionCreatedAt       any            `json:"subscription_created_at"`
		CCOnboardingFlags           map[string]any `json:"cc_onboarding_flags"`
		ClaudeCodeTrialEndsAt       any            `json:"claude_code_trial_ends_at"`
		ClaudeCodeTrialDurationDays any            `json:"claude_code_trial_duration_days"`
		PaymentAuthHostedInvoiceURL any            `json:"payment_auth_hosted_invoice_url"`
	}
	type applicationPayload struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	type response struct {
		Account        accountPayload      `json:"account"`
		Organization   organizationPayload `json:"organization"`
		Application    applicationPayload  `json:"application"`
		EnabledPlugins []any               `json:"enabled_plugins"`
	}
	return response{
		Account: accountPayload{
			UUID:         "",
			FullName:     "",
			DisplayName:  "",
			Email:        accountEmail,
			HasClaudeMax: false,
			HasClaudePro: false,
			CreatedAt:    nil,
		},
		Organization: organizationPayload{
			UUID:                        "",
			Name:                        organizationNameForEmail(accountEmail),
			OrganizationType:            "claude_pro",
			BillingType:                 "",
			RateLimitTier:               "default_claude_ai",
			SeatTier:                    nil,
			HasExtraUsageEnabled:        false,
			SubscriptionStatus:          "",
			SubscriptionCreatedAt:       nil,
			CCOnboardingFlags:           map[string]any{},
			ClaudeCodeTrialEndsAt:       nil,
			ClaudeCodeTrialDurationDays: nil,
			PaymentAuthHostedInvoiceURL: nil,
		},
		Application: applicationPayload{
			UUID: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
			Name: "Claude Code",
			Slug: "claude-code",
		},
		EnabledPlugins: []any{},
	}
}

func claudeCodeAuxClaudeCLIRolesResponse(c *gin.Context) any {
	accountEmail := ""
	if apiKey, ok := middleware.GetAPIKeyFromContext(c); ok && apiKey != nil && apiKey.User != nil {
		accountEmail = strings.TrimSpace(apiKey.User.Email)
	}
	type response struct {
		OrganizationUUID string `json:"organization_uuid"`
		OrganizationName string `json:"organization_name"`
		OrganizationRole string `json:"organization_role"`
		WorkspaceUUID    any    `json:"workspace_uuid"`
		WorkspaceName    any    `json:"workspace_name"`
		WorkspaceRole    any    `json:"workspace_role"`
	}
	return response{
		OrganizationUUID: "",
		OrganizationName: organizationNameForEmail(accountEmail),
		OrganizationRole: "user",
		WorkspaceUUID:    nil,
		WorkspaceName:    nil,
		WorkspaceRole:    nil,
	}
}

func claudeCodeAuxBootstrapResponse(c *gin.Context) gin.H {
	accountEmail := ""
	if apiKey, ok := middleware.GetAPIKeyFromContext(c); ok && apiKey != nil && apiKey.User != nil {
		accountEmail = strings.TrimSpace(apiKey.User.Email)
	}
	if accountEmail == "" {
		return gin.H{
			"client_data":              nil,
			"additional_model_options": nil,
			"additional_model_costs":   nil,
			"oauth_account":            nil,
		}
	}
	return gin.H{
		"client_data":              nil,
		"additional_model_options": nil,
		"additional_model_costs":   nil,
		"oauth_account": gin.H{
			"account_uuid":                 "",
			"account_email":                accountEmail,
			"organization_uuid":            "",
			"organization_name":            organizationNameForEmail(accountEmail),
			"organization_type":            "claude_pro",
			"organization_rate_limit_tier": "default_claude_ai",
			"user_rate_limit_tier":         nil,
			"seat_tier":                    nil,
		},
	}
}

func organizationNameForEmail(email string) string {
	if email == "" {
		return ""
	}
	return email + "'s Organization"
}

func claudeCodeAuxAccountSettingsResponse() gin.H {
	return gin.H{
		"input_menu_pinned_items":              nil,
		"has_seen_mm_examples":                 nil,
		"has_seen_starter_prompts":             nil,
		"has_started_claudeai_onboarding":      true,
		"has_finished_claudeai_onboarding":     true,
		"has_finished_console_onboarding":      nil,
		"dismissed_claudeai_banners":           nil,
		"dismissed_artifacts_announcement":     nil,
		"preview_feature_uses_artifacts":       nil,
		"preview_feature_uses_latex":           nil,
		"preview_feature_uses_citations":       nil,
		"preview_feature_uses_harmony":         nil,
		"enabled_artifacts_attachments":        false,
		"enabled_turmeric":                     nil,
		"enable_chat_suggestions":              nil,
		"dismissed_artifact_feedback_form":     nil,
		"enabled_mm_pdfs":                      nil,
		"enabled_gdrive":                       nil,
		"enabled_bananagrams":                  nil,
		"enabled_web_search":                   true,
		"enabled_compass":                      nil,
		"enabled_sourdough":                    nil,
		"enabled_foccacia":                     nil,
		"enabled_yukon_gold":                   nil,
		"dismissed_claude_code_spotlight":      nil,
		"enabled_geolocation":                  nil,
		"enabled_mcp_tools":                    nil,
		"enabled_connector_suggestions":        nil,
		"enabled_cli_ops":                      nil,
		"enabled_megaminds":                    nil,
		"paprika_mode":                         "off",
		"default_model":                        nil,
		"enabled_full_thinking":                nil,
		"tool_search_mode":                     "auto",
		"enabled_monkeys_in_a_barrel":          nil,
		"enabled_wiggle_egress":                nil,
		"wiggle_egress_allowed_hosts":          nil,
		"wiggle_egress_hosts_template":         nil,
		"wiggle_egress_spotlight_viewed_at":    nil,
		"browser_extension_settings":           nil,
		"enabled_saffron":                      nil,
		"enabled_saffron_search":               nil,
		"enabled_melange":                      nil,
		"internal_melange_store_id":            nil,
		"internal_melange_backfilled_at":       nil,
		"orbit_enabled":                        nil,
		"orbit_timezone":                       nil,
		"dismissed_saffron_themes":             true,
		"grove_enabled":                        true,
		"grove_updated_at":                     time.Now().UTC().Format(time.RFC3339Nano),
		"grove_notice_viewed_at":               nil,
		"internal_tier_org_type":               nil,
		"internal_tier_rate_limit_tier":        nil,
		"internal_tier_seat_tier":              nil,
		"internal_tier_override_expires_at":    nil,
		"has_acknowledged_mcp_app_dev_terms":   nil,
		"onboarding_use_case":                  nil,
		"voice_preference":                     nil,
		"voice_speed":                          nil,
		"voice_language_code":                  nil,
		"ccr_sharing_enforce_repo_check":       nil,
		"ccr_sharing_show_display_name":        nil,
		"ccr_sharing_auto_share_on_pr":         nil,
		"ccr_auto_archive_on_pr_close":         nil,
		"ccr_autofix_on_pr_create":             nil,
		"ccr_auto_create_pr_on_push":           nil,
		"ccr_auto_create_pr_as_draft":          nil,
		"ccr_session_state_buckets":            nil,
		"ccr_persistent_memory":                nil,
		"ccr_plugins_mount":                    nil,
		"cowork_sms_enabled":                   nil,
		"cowork_onboarding_completed_at":       nil,
		"dittos_mobile_onboarding_seen_at":     nil,
		"internal_cowork_trial_started_at":     nil,
		"internal_cowork_trial_ends_at":        nil,
		"internal_has_used_remote_control":     nil,
		"internal_tangelo_credit_claimed":      nil,
		"internal_cc_onboarding_settings":      nil,
		"internal_sonnet_45_retirement_cohort": nil,
		"synthetic_probe_last_touch_ms":        nil,
	}
}

func logClaudeCodeAuxCompat(c *gin.Context, endpoint claudeCodeAuxEndpoint, mode string) {
	if c == nil || c.Request == nil {
		return
	}
	apiKeyID := int64(0)
	groupID := int64(0)
	if apiKey, ok := middleware.GetAPIKeyFromContext(c); ok && apiKey != nil {
		apiKeyID = apiKey.ID
		if apiKey.GroupID != nil {
			groupID = *apiKey.GroupID
		}
	}
	slog.Info("claude_code_aux_compat",
		"endpoint", string(endpoint),
		"mode", mode,
		"path", c.Request.URL.Path,
		"status", http.StatusOK,
		"api_key_id", apiKeyID,
		"group_id", groupID,
		"client_version", extractClaudeCodeAuxClientVersion(c.GetHeader("User-Agent")),
		"session_hash", hashForAuxCompatLog(c.GetHeader("X-Claude-Code-Session-Id")),
		"request_id_hash", hashForAuxCompatLog(c.GetHeader("x-client-request-id")),
	)
}

func extractClaudeCodeAuxClientVersion(userAgent string) string {
	version := service.ExtractCLIVersion(userAgent)
	if version != "" {
		return version
	}
	trimmed := strings.TrimSpace(userAgent)
	for _, prefix := range []string{"claude-code/", "claude-cli/"} {
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			rest := trimmed[len(prefix):]
			if i := strings.IndexAny(rest, " ;("); i >= 0 {
				rest = rest[:i]
			}
			return rest
		}
	}
	return ""
}

func hashForAuxCompatLog(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
