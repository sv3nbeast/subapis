//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSettingsCNQuotasAndThresholdsRoundTrip(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	payload := map[string]any{
		"default_platform_quotas": map[string]any{
			"kimi":     map[string]any{"daily": 0},
			"zhipu":    map[string]any{"weekly": 50},
			"deepseek": map[string]any{"monthly": 100},
		},
		"account_scheduling_thresholds": map[string]int{"kimi": 80, "zhipu": 90},
	}
	rec := doUpdateSettings(t, h, payload, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct{ Data map[string]json.RawMessage }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	var quotas map[string]*service.DefaultPlatformQuotaSetting
	require.NoError(t, json.Unmarshal(out.Data["default_platform_quotas"], &quotas))
	require.Equal(t, 0.0, *quotas["kimi"].DailyLimitUSD)
	require.Equal(t, 50.0, *quotas["zhipu"].WeeklyLimitUSD)
	require.Equal(t, 100.0, *quotas["deepseek"].MonthlyLimitUSD)
	var thresholds map[string]int
	require.NoError(t, json.Unmarshal(out.Data["account_scheduling_thresholds"], &thresholds))
	require.Equal(t, 80, thresholds["kimi"])
	require.Equal(t, 90, thresholds["zhipu"])
	storedQuotas := repo.values[service.SettingKeyDefaultPlatformQuotas]
	storedThresholds := repo.values[service.SettingKeyAccountSchedulingThresholds]
	require.NotEmpty(t, storedQuotas)
	require.NotEmpty(t, storedThresholds)
	// Updating an unrelated setting must preserve the newly writable fields.
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "Unrelated"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, storedQuotas, repo.values[service.SettingKeyDefaultPlatformQuotas])
	require.Equal(t, storedThresholds, repo.values[service.SettingKeyAccountSchedulingThresholds])
}

func TestSettingsGrokModelAndTTFTRoundTrip(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	for _, enabled := range []bool{true, false} {
		payload := map[string]any{"grok_default_text_model": "grok-4.3",
			"grok_cross_client_model_map_enabled": enabled, "openai_ttft_mode": "visible",
			"channel_monitor_mode": "v2", "channel_monitor_hide_throughput": enabled,
			"channel_monitor_show_quota": !enabled}
		rec := doUpdateSettings(t, h, payload, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out struct{ Data map[string]any }
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		for key, value := range payload {
			require.Equal(t, value, out.Data[key], key)
		}
		require.Equal(t, "grok-4.3", repo.values[service.SettingKeyGrokDefaultTextModel])
		require.Equal(t, "visible", repo.values[service.SettingKeyOpenAITTFTMode])
		require.Equal(t, xai.ModelMappingOptions{DefaultText: "grok-4.3", EnableCrossClientMap: enabled}, xai.RuntimeModelMappingOptions())
		version := xai.RuntimeModelMappingVersion()
		rec = doUpdateSettings(t, h, map[string]any{"site_name": "Unrelated"}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		for key, value := range payload {
			require.Equal(t, value, out.Data[key], key+" must survive unrelated writes")
		}
		require.Equal(t, version, xai.RuntimeModelMappingVersion(), "unchanged settings must not invalidate model caches")
	}
}
