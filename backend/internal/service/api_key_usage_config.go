package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const maxAPIKeyUsageExtraConfigBytes = 16 * 1024

// APIKeyUsageConfig controls defaults rendered in the user-facing "Use key" dialog.
type APIKeyUsageConfig struct {
	ClaudeCodeDefaultModel               string `json:"claude_code_default_model"`
	ClaudeCodeDisableNonessentialTraffic bool   `json:"claude_code_disable_nonessential_traffic"`
	ClaudeCodeAttributionHeader          int    `json:"claude_code_attribution_header"`
	GeminiCLIDefaultModel                string `json:"gemini_cli_default_model"`
	CodexModel                           string `json:"codex_model"`
	CodexReviewModel                     string `json:"codex_review_model"`
	CodexReasoningEffort                 string `json:"codex_reasoning_effort"`
	CodexDisableResponseStorage          bool   `json:"codex_disable_response_storage"`
	CodexNetworkAccess                   string `json:"codex_network_access"`
	CodexGoalsEnabled                    bool   `json:"codex_goals_enabled"`
	CodexWebSocketEnabled                bool   `json:"codex_websocket_enabled"`
	CodexIncludeLegacyWSFeature          bool   `json:"codex_include_legacy_ws_feature"`
	CodexExtraConfig                     string `json:"codex_extra_config"`
}

func DefaultAPIKeyUsageConfig() *APIKeyUsageConfig {
	return &APIKeyUsageConfig{
		ClaudeCodeDefaultModel:               "claude-opus-4-7",
		ClaudeCodeDisableNonessentialTraffic: true,
		ClaudeCodeAttributionHeader:          0,
		GeminiCLIDefaultModel:                "gemini-2.0-flash",
		CodexModel:                           "gpt-5.5",
		CodexReviewModel:                     "gpt-5.5",
		CodexReasoningEffort:                 "xhigh",
		CodexDisableResponseStorage:          true,
		CodexNetworkAccess:                   "enabled",
		CodexGoalsEnabled:                    true,
		CodexWebSocketEnabled:                true,
		CodexIncludeLegacyWSFeature:          false,
	}
}

func normalizeAPIKeyUsageConfig(cfg *APIKeyUsageConfig) *APIKeyUsageConfig {
	defaults := DefaultAPIKeyUsageConfig()
	if cfg == nil {
		return defaults
	}

	normalized := *cfg
	normalized.ClaudeCodeDefaultModel = firstNonEmpty(normalized.ClaudeCodeDefaultModel, defaults.ClaudeCodeDefaultModel)
	normalized.GeminiCLIDefaultModel = firstNonEmpty(normalized.GeminiCLIDefaultModel, defaults.GeminiCLIDefaultModel)
	normalized.CodexModel = firstNonEmpty(normalized.CodexModel, defaults.CodexModel)
	normalized.CodexReviewModel = firstNonEmpty(normalized.CodexReviewModel, normalized.CodexModel)

	switch effort := strings.ToLower(strings.TrimSpace(normalized.CodexReasoningEffort)); effort {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		normalized.CodexReasoningEffort = effort
	default:
		normalized.CodexReasoningEffort = defaults.CodexReasoningEffort
	}

	normalized.CodexNetworkAccess = firstNonEmpty(normalized.CodexNetworkAccess, defaults.CodexNetworkAccess)
	if normalized.ClaudeCodeAttributionHeader != 1 {
		normalized.ClaudeCodeAttributionHeader = 0
	}
	normalized.CodexExtraConfig = strings.TrimSpace(normalized.CodexExtraConfig)
	return &normalized
}

func parseAPIKeyUsageConfig(raw string) *APIKeyUsageConfig {
	cfg := DefaultAPIKeyUsageConfig()
	if strings.TrimSpace(raw) == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return DefaultAPIKeyUsageConfig()
	}
	return normalizeAPIKeyUsageConfig(cfg)
}

func (s *SettingService) GetAPIKeyUsageConfig(ctx context.Context) (*APIKeyUsageConfig, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAPIKeyUsageConfig)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultAPIKeyUsageConfig(), nil
		}
		return nil, fmt.Errorf("get api key usage config: %w", err)
	}
	return parseAPIKeyUsageConfig(value), nil
}

func (s *SettingService) SetAPIKeyUsageConfig(ctx context.Context, cfg *APIKeyUsageConfig) error {
	if cfg == nil {
		return infraerrors.BadRequest("API_KEY_USAGE_CONFIG_REQUIRED", "api key usage config cannot be nil")
	}
	normalized := normalizeAPIKeyUsageConfig(cfg)
	if len(normalized.CodexExtraConfig) > maxAPIKeyUsageExtraConfigBytes {
		return infraerrors.BadRequest(
			"API_KEY_USAGE_EXTRA_CONFIG_TOO_LARGE",
			fmt.Sprintf("codex_extra_config exceeds %d bytes", maxAPIKeyUsageExtraConfigBytes),
		)
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal api key usage config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAPIKeyUsageConfig, string(data)); err != nil {
		return fmt.Errorf("set api key usage config: %w", err)
	}
	return nil
}
