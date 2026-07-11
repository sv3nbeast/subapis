package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type apiKeyUsageSettingRepoStub struct {
	values map[string]string
}

func (s *apiKeyUsageSettingRepoStub) Get(_ context.Context, key string) (*Setting, error) {
	value, ok := s.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (s *apiKeyUsageSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *apiKeyUsageSettingRepoStub) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func (s *apiKeyUsageSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (s *apiKeyUsageSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	for key, value := range settings {
		s.values[key] = value
	}
	return nil
}

func (s *apiKeyUsageSettingRepoStub) GetAll(_ context.Context) (map[string]string, error) {
	values := make(map[string]string, len(s.values))
	for key, value := range s.values {
		values[key] = value
	}
	return values, nil
}

func (s *apiKeyUsageSettingRepoStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func TestParseAPIKeyUsageConfigUsesDefaultsForMissingFields(t *testing.T) {
	cfg := parseAPIKeyUsageConfig(`{"codex_model":"gpt-custom","codex_goals_enabled":false}`)

	require.Equal(t, "gpt-custom", cfg.CodexModel)
	require.Equal(t, "gpt-5.5", cfg.CodexReviewModel)
	require.False(t, cfg.CodexGoalsEnabled)
	require.True(t, cfg.CodexWebSocketEnabled)
	require.Equal(t, "claude-opus-4-7", cfg.ClaudeCodeDefaultModel)
}

func TestParseAPIKeyUsageConfigFallsBackOnInvalidJSON(t *testing.T) {
	cfg := parseAPIKeyUsageConfig(`{"codex_model":`)
	require.Equal(t, DefaultAPIKeyUsageConfig(), cfg)
}

func TestNormalizeAPIKeyUsageConfigSanitizesValues(t *testing.T) {
	cfg := normalizeAPIKeyUsageConfig(&APIKeyUsageConfig{
		CodexModel:                  " custom-model ",
		CodexReasoningEffort:        "INVALID",
		ClaudeCodeAttributionHeader: 7,
		CodexExtraConfig:            "\nservice_tier = \"fast\"\n",
	})

	require.Equal(t, "custom-model", cfg.CodexModel)
	require.Equal(t, "custom-model", cfg.CodexReviewModel)
	require.Equal(t, "xhigh", cfg.CodexReasoningEffort)
	require.Equal(t, 0, cfg.ClaudeCodeAttributionHeader)
	require.Equal(t, `service_tier = "fast"`, cfg.CodexExtraConfig)
}

func TestAPIKeyUsageConfigServicePersistsNormalizedConfig(t *testing.T) {
	repo := &apiKeyUsageSettingRepoStub{values: map[string]string{}}
	service := NewSettingService(repo, &config.Config{})

	initial, err := service.GetAPIKeyUsageConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, DefaultAPIKeyUsageConfig(), initial)

	err = service.SetAPIKeyUsageConfig(context.Background(), &APIKeyUsageConfig{
		ClaudeCodeDefaultModel:      " claude-custom ",
		GeminiCLIDefaultModel:       " gemini-custom ",
		CodexModel:                  " gpt-custom ",
		CodexReasoningEffort:        "HIGH",
		CodexWebSocketEnabled:       true,
		CodexIncludeLegacyWSFeature: true,
		CodexExtraConfig:            "\nservice_tier = \"fast\"\n",
	})
	require.NoError(t, err)

	stored, err := service.GetAPIKeyUsageConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, "claude-custom", stored.ClaudeCodeDefaultModel)
	require.Equal(t, "gemini-custom", stored.GeminiCLIDefaultModel)
	require.Equal(t, "gpt-custom", stored.CodexModel)
	require.Equal(t, "gpt-custom", stored.CodexReviewModel)
	require.Equal(t, "high", stored.CodexReasoningEffort)
	require.True(t, stored.CodexWebSocketEnabled)
	require.True(t, stored.CodexIncludeLegacyWSFeature)
	require.Equal(t, `service_tier = "fast"`, stored.CodexExtraConfig)
}

func TestGetPublicSettingsIncludesAPIKeyUsageConfig(t *testing.T) {
	repo := &apiKeyUsageSettingRepoStub{values: map[string]string{
		SettingKeyAPIKeyUsageConfig: `{"codex_model":"gpt-public","codex_goals_enabled":false}`,
	}}
	service := NewSettingService(repo, &config.Config{})

	settings, err := service.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "gpt-public", settings.APIKeyUsageConfig.CodexModel)
	require.False(t, settings.APIKeyUsageConfig.CodexGoalsEnabled)
	require.Equal(t, "claude-opus-4-7", settings.APIKeyUsageConfig.ClaudeCodeDefaultModel)
}

func TestSetAPIKeyUsageConfigRejectsOversizedExtraConfig(t *testing.T) {
	repo := &apiKeyUsageSettingRepoStub{values: map[string]string{}}
	service := NewSettingService(repo, &config.Config{})

	err := service.SetAPIKeyUsageConfig(context.Background(), &APIKeyUsageConfig{
		CodexExtraConfig: strings.Repeat("x", maxAPIKeyUsageExtraConfigBytes+1),
	})
	require.ErrorContains(t, err, "codex_extra_config exceeds")
	require.NotContains(t, repo.values, SettingKeyAPIKeyUsageConfig)
}

func validAPIKeyUsageTemplateProfile() APIKeyUsageTemplateProfile {
	return APIKeyUsageTemplateProfile{
		ID:       "grok-claude",
		Name:     "Grok Claude Code",
		Enabled:  true,
		Priority: 100,
		Mode:     APIKeyUsageTemplateModeAppend,
		Match: APIKeyUsageTemplateMatch{
			Platforms:      []string{" GROK ", "grok"},
			GroupIDs:       []int64{9, 3, 9, -1},
			ClaudeCodeOnly: APIKeyUsageClaudeCodeOnlyRequired,
		},
		Templates: []APIKeyUsageClientTemplate{{
			ID:        "grok-claude-code",
			Label:     "Claude Code",
			Kind:      "CLAUDE_CODE",
			Enabled:   true,
			SortOrder: 10,
			Variants: []APIKeyUsageTemplateVariant{{
				ID:    "unix",
				Label: "macOS / Linux",
				Files: []APIKeyUsageTemplateFile{{
					Path:    " Terminal ",
					Content: `export ANTHROPIC_AUTH_TOKEN="{{api_key}}"`,
				}},
			}},
		}},
	}
}

func TestParseAPIKeyUsageConfigSupportsMissingTemplateProfiles(t *testing.T) {
	cfg := parseAPIKeyUsageConfig(`{"codex_model":"gpt-compatible"}`)

	require.NotNil(t, cfg.TemplateProfiles)
	require.Empty(t, cfg.TemplateProfiles)
}

func TestNormalizeAPIKeyUsageTemplateProfilesNormalizesMatchValues(t *testing.T) {
	profiles, err := normalizeAPIKeyUsageTemplateProfiles([]APIKeyUsageTemplateProfile{validAPIKeyUsageTemplateProfile()}, true)

	require.NoError(t, err)
	require.Len(t, profiles, 1)
	require.Equal(t, []string{"grok"}, profiles[0].Match.Platforms)
	require.Equal(t, []int64{3, 9}, profiles[0].Match.GroupIDs)
	require.Equal(t, "claude_code", profiles[0].Templates[0].Kind)
	require.Equal(t, "Terminal", profiles[0].Templates[0].Variants[0].Files[0].Path)
}

func TestSetAPIKeyUsageConfigRejectsInvalidTemplateProfiles(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*APIKeyUsageTemplateProfile)
		match  string
	}{
		{
			name: "invalid profile id",
			mutate: func(profile *APIKeyUsageTemplateProfile) {
				profile.ID = "invalid profile"
			},
			match: "profile id",
		},
		{
			name: "invalid mode",
			mutate: func(profile *APIKeyUsageTemplateProfile) {
				profile.Mode = "merge"
			},
			match: "unsupported mode",
		},
		{
			name: "duplicate template id",
			mutate: func(profile *APIKeyUsageTemplateProfile) {
				profile.Templates = append(profile.Templates, profile.Templates[0])
			},
			match: "duplicate template id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &apiKeyUsageSettingRepoStub{values: map[string]string{}}
			service := NewSettingService(repo, &config.Config{})
			profile := validAPIKeyUsageTemplateProfile()
			tt.mutate(&profile)

			err := service.SetAPIKeyUsageConfig(context.Background(), &APIKeyUsageConfig{
				TemplateProfiles: []APIKeyUsageTemplateProfile{profile},
			})

			require.ErrorContains(t, err, tt.match)
			require.NotContains(t, repo.values, SettingKeyAPIKeyUsageConfig)
		})
	}
}

func TestSetAPIKeyUsageConfigRejectsOversizedTemplateContent(t *testing.T) {
	repo := &apiKeyUsageSettingRepoStub{values: map[string]string{}}
	service := NewSettingService(repo, &config.Config{})
	profile := validAPIKeyUsageTemplateProfile()
	profile.Templates[0].Variants[0].Files[0].Content = strings.Repeat("x", maxAPIKeyUsageTemplateContentBytes+1)

	err := service.SetAPIKeyUsageConfig(context.Background(), &APIKeyUsageConfig{
		TemplateProfiles: []APIKeyUsageTemplateProfile{profile},
	})

	require.ErrorContains(t, err, "template profile content exceeds")
	require.NotContains(t, repo.values, SettingKeyAPIKeyUsageConfig)
}

func TestAPIKeyUsageTemplateProfilesPersistAndArePublic(t *testing.T) {
	repo := &apiKeyUsageSettingRepoStub{values: map[string]string{}}
	service := NewSettingService(repo, &config.Config{})

	err := service.SetAPIKeyUsageConfig(context.Background(), &APIKeyUsageConfig{
		TemplateProfiles: []APIKeyUsageTemplateProfile{validAPIKeyUsageTemplateProfile()},
	})
	require.NoError(t, err)

	stored, err := service.GetAPIKeyUsageConfig(context.Background())
	require.NoError(t, err)
	require.Len(t, stored.TemplateProfiles, 1)
	require.Equal(t, []string{"grok"}, stored.TemplateProfiles[0].Match.Platforms)

	settings, err := service.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Len(t, settings.APIKeyUsageConfig.TemplateProfiles, 1)
	require.Contains(t, settings.APIKeyUsageConfig.TemplateProfiles[0].Templates[0].Variants[0].Files[0].Content, "{{api_key}}")
}

func TestParseAPIKeyUsageConfigSkipsCorruptTemplateProfiles(t *testing.T) {
	cfg := parseAPIKeyUsageConfig(`{
		"template_profiles": [
			{"id":"bad id","name":"Bad","enabled":true,"mode":"append","match":{},"templates":[]},
			{"id":"bad-replace","name":"Bad replace","enabled":true,"mode":"replace","match":{},"templates":[{"id":"broken","label":"Broken","kind":"generic","enabled":true,"variants":[]}]},
			{"id":"valid","name":"Valid","enabled":true,"mode":"append","match":{},"templates":[{"id":"curl","label":"cURL","kind":"generic","enabled":true,"variants":[{"id":"default","label":"Default","files":[{"path":"Terminal","content":"curl {{base_url}}"}]}]}]}
		]
	}`)

	require.Len(t, cfg.TemplateProfiles, 1)
	require.Equal(t, "valid", cfg.TemplateProfiles[0].ID)
	require.Equal(t, APIKeyUsageClaudeCodeOnlyAny, cfg.TemplateProfiles[0].Match.ClaudeCodeOnly)
}
