//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStatusProbeService_LoadConfig_LegacyGlobalCredentialsPopulateModelConfig(t *testing.T) {
	repo := &settingRepoStub{
		values: map[string]string{
			SettingKeyStatusProbeConfig: `{
				"enabled": true,
				"interval_minutes": 5,
				"retention_days": 7,
				"api_key": "legacy-key",
				"base_url": "https://api.subapis.com/",
				"models": [
					{"model":"claude-sonnet-4-6","display_name":"Claude","sort_order":0,"enabled":true},
					{"model":"gpt-5.4","display_name":"GPT","sort_order":1,"enabled":true,"api_key":"model-key","base_url":"https://other.example.com/"}
				]
			}`,
		},
	}
	svc := NewStatusProbeService(nil, NewSettingService(repo, &config.Config{}))

	cfg, err := svc.LoadConfig(context.Background())
	require.NoError(t, err)
	require.Len(t, cfg.Models, 2)

	require.Equal(t, "legacy-key", cfg.Models[0].ApiKey)
	require.Equal(t, "https://api.subapis.com", cfg.Models[0].BaseURL)
	require.Equal(t, "model-key", cfg.Models[1].ApiKey)
	require.Equal(t, "https://other.example.com", cfg.Models[1].BaseURL)
	require.Empty(t, cfg.ApiKey)
	require.Empty(t, cfg.BaseURL)
}

func TestStatusProbeService_buildModelStatus_MarksStaleResultsUnknown(t *testing.T) {
	svc := &StatusProbeService{}
	now := time.Date(2026, 4, 8, 22, 20, 0, 0, time.UTC)
	results := []probeRawResult{
		{Model: "claude-sonnet-4-6", Status: "error", CreatedAt: now.Add(-11 * time.Minute)},
		{Model: "claude-sonnet-4-6", Status: "error", CreatedAt: now.Add(-16 * time.Minute)},
		{Model: "claude-sonnet-4-6", Status: "error", CreatedAt: now.Add(-21 * time.Minute)},
	}

	ms := svc.buildModelStatus("claude-sonnet-4-6", "Claude", results, 5, now)
	require.Equal(t, "unknown", ms.CurrentStatus)
	require.Equal(t, 3, ms.TotalProbes)
}

func TestStatusProbeService_buildModelStatus_UsesRecentStatusesWhenFresh(t *testing.T) {
	svc := &StatusProbeService{}
	now := time.Date(2026, 4, 9, 0, 42, 0, 0, time.UTC)
	results := []probeRawResult{
		{Model: "claude-sonnet-4-6", Status: "ok", CreatedAt: now.Add(-2 * time.Minute)},
		{Model: "claude-sonnet-4-6", Status: "error", CreatedAt: now.Add(-7 * time.Minute)},
		{Model: "claude-sonnet-4-6", Status: "error", CreatedAt: now.Add(-12 * time.Minute)},
	}

	ms := svc.buildModelStatus("claude-sonnet-4-6", "Claude", results, 5, now)
	require.Equal(t, "degraded", ms.CurrentStatus)
}

func TestComputeOverallStatus_DegradesWhenAllModelsAreUnknown(t *testing.T) {
	status := computeOverallStatus([]ModelStatus{
		{Model: "a", CurrentStatus: "unknown"},
		{Model: "b", CurrentStatus: "unknown"},
	})
	require.Equal(t, "degraded", status)
}
