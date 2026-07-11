package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const maxAPIKeyUsageExtraConfigBytes = 16 * 1024

const (
	maxAPIKeyUsageTemplateProfiles     = 50
	maxAPIKeyUsageTemplatesPerProfile  = 20
	maxAPIKeyUsageVariantsPerTemplate  = 10
	maxAPIKeyUsageFilesPerVariant      = 10
	maxAPIKeyUsageTemplateContentBytes = 512 * 1024
)

var apiKeyUsageTemplateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

const (
	APIKeyUsageTemplateModeAppend  = "append"
	APIKeyUsageTemplateModeReplace = "replace"

	APIKeyUsageClaudeCodeOnlyAny       = "any"
	APIKeyUsageClaudeCodeOnlyRequired  = "required"
	APIKeyUsageClaudeCodeOnlyForbidden = "forbidden"
)

// APIKeyUsageTemplateFile is one rendered file/code block in a client variant.
// Content supports frontend placeholders such as {{base_url}}, {{api_key}},
// {{group_name}}, {{platform}}, and the centrally configured model variables.
type APIKeyUsageTemplateFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Hint    string `json:"hint,omitempty"`
}

// APIKeyUsageTemplateVariant represents an OS, shell, or configuration variant.
type APIKeyUsageTemplateVariant struct {
	ID    string                    `json:"id"`
	Label string                    `json:"label"`
	Files []APIKeyUsageTemplateFile `json:"files"`
}

// APIKeyUsageClientTemplate is one user-visible client tab.
type APIKeyUsageClientTemplate struct {
	ID          string                       `json:"id"`
	Label       string                       `json:"label"`
	Description string                       `json:"description,omitempty"`
	Note        string                       `json:"note,omitempty"`
	Kind        string                       `json:"kind"`
	Enabled     bool                         `json:"enabled"`
	SortOrder   int                          `json:"sort_order"`
	Variants    []APIKeyUsageTemplateVariant `json:"variants"`
}

// APIKeyUsageTemplateMatch controls which groups receive a template profile.
// Empty platforms and group_ids are wildcards. When both are supplied, both
// conditions must match.
type APIKeyUsageTemplateMatch struct {
	Platforms      []string `json:"platforms"`
	GroupIDs       []int64  `json:"group_ids"`
	ClaudeCodeOnly string   `json:"claude_code_only"`
}

// APIKeyUsageTemplateProfile appends to or replaces built-in client templates
// for matching groups. Profiles are applied by priority and then declaration
// order; a template with the same ID replaces the prior template.
type APIKeyUsageTemplateProfile struct {
	ID        string                      `json:"id"`
	Name      string                      `json:"name"`
	Enabled   bool                        `json:"enabled"`
	Priority  int                         `json:"priority"`
	Mode      string                      `json:"mode"`
	Match     APIKeyUsageTemplateMatch    `json:"match"`
	Templates []APIKeyUsageClientTemplate `json:"templates"`
}

// APIKeyUsageConfig controls defaults rendered in the user-facing "Use key" dialog.
type APIKeyUsageConfig struct {
	ClaudeCodeDefaultModel               string                       `json:"claude_code_default_model"`
	ClaudeCodeDisableNonessentialTraffic bool                         `json:"claude_code_disable_nonessential_traffic"`
	ClaudeCodeAttributionHeader          int                          `json:"claude_code_attribution_header"`
	GeminiCLIDefaultModel                string                       `json:"gemini_cli_default_model"`
	CodexModel                           string                       `json:"codex_model"`
	CodexReviewModel                     string                       `json:"codex_review_model"`
	CodexReasoningEffort                 string                       `json:"codex_reasoning_effort"`
	CodexDisableResponseStorage          bool                         `json:"codex_disable_response_storage"`
	CodexNetworkAccess                   string                       `json:"codex_network_access"`
	CodexGoalsEnabled                    bool                         `json:"codex_goals_enabled"`
	CodexWebSocketEnabled                bool                         `json:"codex_websocket_enabled"`
	CodexIncludeLegacyWSFeature          bool                         `json:"codex_include_legacy_ws_feature"`
	CodexExtraConfig                     string                       `json:"codex_extra_config"`
	TemplateProfiles                     []APIKeyUsageTemplateProfile `json:"template_profiles"`
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
		TemplateProfiles:                     []APIKeyUsageTemplateProfile{},
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
	profiles, _ := normalizeAPIKeyUsageTemplateProfiles(normalized.TemplateProfiles, false)
	normalized.TemplateProfiles = profiles
	return &normalized
}

func normalizeAPIKeyUsageTemplateProfiles(profiles []APIKeyUsageTemplateProfile, strict bool) ([]APIKeyUsageTemplateProfile, error) {
	if len(profiles) > maxAPIKeyUsageTemplateProfiles {
		if strict {
			return nil, fmt.Errorf("template_profiles exceeds %d entries", maxAPIKeyUsageTemplateProfiles)
		}
		profiles = profiles[:maxAPIKeyUsageTemplateProfiles]
	}

	normalized := make([]APIKeyUsageTemplateProfile, 0, len(profiles))
	seenProfiles := make(map[string]struct{}, len(profiles))
	totalContentBytes := 0
	for profileIndex, profile := range profiles {
		profile.ID = strings.TrimSpace(profile.ID)
		profile.Name = strings.TrimSpace(profile.Name)
		profile.Mode = strings.ToLower(strings.TrimSpace(profile.Mode))
		if profile.Mode == "" {
			profile.Mode = APIKeyUsageTemplateModeAppend
		}
		profile.Match.ClaudeCodeOnly = strings.ToLower(strings.TrimSpace(profile.Match.ClaudeCodeOnly))
		if profile.Match.ClaudeCodeOnly == "" {
			profile.Match.ClaudeCodeOnly = APIKeyUsageClaudeCodeOnlyAny
		}

		if err := validateAPIKeyUsageTemplateID("profile", profile.ID); err != nil {
			if strict {
				return nil, fmt.Errorf("template_profiles[%d]: %w", profileIndex, err)
			}
			continue
		}
		if _, exists := seenProfiles[profile.ID]; exists {
			if strict {
				return nil, fmt.Errorf("template_profiles[%d]: duplicate profile id %q", profileIndex, profile.ID)
			}
			continue
		}
		if profile.Name == "" || len(profile.Name) > 128 {
			if strict {
				return nil, fmt.Errorf("template_profiles[%d]: name must be 1-128 bytes", profileIndex)
			}
			continue
		}
		if profile.Mode != APIKeyUsageTemplateModeAppend && profile.Mode != APIKeyUsageTemplateModeReplace {
			if strict {
				return nil, fmt.Errorf("template_profiles[%d]: unsupported mode %q", profileIndex, profile.Mode)
			}
			continue
		}
		switch profile.Match.ClaudeCodeOnly {
		case APIKeyUsageClaudeCodeOnlyAny, APIKeyUsageClaudeCodeOnlyRequired, APIKeyUsageClaudeCodeOnlyForbidden:
		default:
			if strict {
				return nil, fmt.Errorf("template_profiles[%d]: unsupported claude_code_only mode %q", profileIndex, profile.Match.ClaudeCodeOnly)
			}
			continue
		}

		profile.Match.Platforms = normalizeAPIKeyUsagePlatforms(profile.Match.Platforms)
		profile.Match.GroupIDs = normalizeAPIKeyUsageGroupIDs(profile.Match.GroupIDs)
		if len(profile.Templates) > maxAPIKeyUsageTemplatesPerProfile {
			if strict {
				return nil, fmt.Errorf("template_profiles[%d].templates exceeds %d entries", profileIndex, maxAPIKeyUsageTemplatesPerProfile)
			}
			continue
		}

		profileContentBytes := 0
		seenTemplates := make(map[string]struct{}, len(profile.Templates))
		templates := make([]APIKeyUsageClientTemplate, 0, len(profile.Templates))
		validProfile := true
		for templateIndex, template := range profile.Templates {
			templateContentBytes := 0
			template.ID = strings.TrimSpace(template.ID)
			template.Label = strings.TrimSpace(template.Label)
			template.Description = strings.TrimSpace(template.Description)
			template.Note = strings.TrimSpace(template.Note)
			template.Kind = strings.ToLower(strings.TrimSpace(template.Kind))
			if template.Kind == "" {
				template.Kind = "generic"
			}
			if err := validateAPIKeyUsageTemplateID("template", template.ID); err != nil {
				if strict {
					return nil, fmt.Errorf("template_profiles[%d].templates[%d]: %w", profileIndex, templateIndex, err)
				}
				validProfile = false
				break
			}
			if _, exists := seenTemplates[template.ID]; exists {
				if strict {
					return nil, fmt.Errorf("template_profiles[%d].templates[%d]: duplicate template id %q", profileIndex, templateIndex, template.ID)
				}
				validProfile = false
				break
			}
			if template.Label == "" || len(template.Label) > 128 || len(template.Kind) > 32 || len(template.Description) > 2048 || len(template.Note) > 2048 {
				if strict {
					return nil, fmt.Errorf("template_profiles[%d].templates[%d]: invalid label or kind", profileIndex, templateIndex)
				}
				validProfile = false
				break
			}
			if len(template.Variants) == 0 || len(template.Variants) > maxAPIKeyUsageVariantsPerTemplate {
				if strict {
					return nil, fmt.Errorf("template_profiles[%d].templates[%d]: variants must contain 1-%d entries", profileIndex, templateIndex, maxAPIKeyUsageVariantsPerTemplate)
				}
				validProfile = false
				break
			}

			seenVariants := make(map[string]struct{}, len(template.Variants))
			variants := make([]APIKeyUsageTemplateVariant, 0, len(template.Variants))
			validTemplate := true
			for variantIndex, variant := range template.Variants {
				variant.ID = strings.TrimSpace(variant.ID)
				variant.Label = strings.TrimSpace(variant.Label)
				if err := validateAPIKeyUsageTemplateID("variant", variant.ID); err != nil {
					if strict {
						return nil, fmt.Errorf("template_profiles[%d].templates[%d].variants[%d]: %w", profileIndex, templateIndex, variantIndex, err)
					}
					validTemplate = false
					break
				}
				if _, exists := seenVariants[variant.ID]; exists || variant.Label == "" || len(variant.Label) > 128 {
					if strict {
						return nil, fmt.Errorf("template_profiles[%d].templates[%d].variants[%d]: duplicate id or invalid label", profileIndex, templateIndex, variantIndex)
					}
					validTemplate = false
					break
				}
				seenVariants[variant.ID] = struct{}{}
				if len(variant.Files) == 0 || len(variant.Files) > maxAPIKeyUsageFilesPerVariant {
					if strict {
						return nil, fmt.Errorf("template_profiles[%d].templates[%d].variants[%d]: files must contain 1-%d entries", profileIndex, templateIndex, variantIndex, maxAPIKeyUsageFilesPerVariant)
					}
					validTemplate = false
					break
				}
				for fileIndex := range variant.Files {
					file := &variant.Files[fileIndex]
					file.Path = strings.TrimSpace(file.Path)
					file.Hint = strings.TrimSpace(file.Hint)
					if file.Path == "" || len(file.Path) > 512 || file.Content == "" {
						if strict {
							return nil, fmt.Errorf("template_profiles[%d].templates[%d].variants[%d].files[%d]: path and content are required", profileIndex, templateIndex, variantIndex, fileIndex)
						}
						validTemplate = false
						break
					}
					templateContentBytes += len(file.Path) + len(file.Content) + len(file.Hint)
				}
				if !validTemplate {
					break
				}
				variants = append(variants, variant)
			}
			if !validTemplate {
				validProfile = false
				break
			}
			template.Variants = variants
			profileContentBytes += templateContentBytes
			seenTemplates[template.ID] = struct{}{}
			templates = append(templates, template)
		}
		if !validProfile {
			continue
		}
		profile.Templates = templates
		if totalContentBytes+profileContentBytes > maxAPIKeyUsageTemplateContentBytes {
			if strict {
				return nil, fmt.Errorf("template profile content exceeds %d bytes", maxAPIKeyUsageTemplateContentBytes)
			}
			continue
		}
		totalContentBytes += profileContentBytes
		seenProfiles[profile.ID] = struct{}{}
		normalized = append(normalized, profile)
	}
	return normalized, nil
}

func validateAPIKeyUsageTemplateID(kind, value string) error {
	if !apiKeyUsageTemplateIDPattern.MatchString(value) {
		return fmt.Errorf("%s id %q must match %s", kind, value, apiKeyUsageTemplateIDPattern.String())
	}
	return nil
}

func normalizeAPIKeyUsagePlatforms(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || len(value) > 50 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeAPIKeyUsageGroupIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
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
	profiles, err := normalizeAPIKeyUsageTemplateProfiles(cfg.TemplateProfiles, true)
	if err != nil {
		return infraerrors.BadRequest("API_KEY_USAGE_TEMPLATE_CONFIG_INVALID", err.Error())
	}
	normalized.TemplateProfiles = profiles
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal api key usage config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAPIKeyUsageConfig, string(data)); err != nil {
		return fmt.Errorf("set api key usage config: %w", err)
	}
	return nil
}
