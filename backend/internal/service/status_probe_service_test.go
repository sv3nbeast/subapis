//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
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

func TestStatusProbeService_runProbe_UsesGeminiEndpointAndPayload(t *testing.T) {
	var (
		gotPath          string
		gotGoogleAPIKey  string
		gotAnthropicVers string
		gotBody          map[string]any
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotGoogleAPIKey = r.Header.Get("x-goog-api-key")
		gotAnthropicVers = r.Header.Get("anthropic-version")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
	}))
	defer ts.Close()

	svc := &StatusProbeService{httpClient: ts.Client()}
	latencyMs, errMsg := svc.runProbe(context.Background(), StatusProbeModelConfig{
		Model:   "gemini-3-flash",
		ApiKey:  "test-key",
		BaseURL: ts.URL,
	})

	require.Empty(t, errMsg)
	require.GreaterOrEqual(t, latencyMs, 0)
	require.Equal(t, "/v1beta/models/gemini-3-flash:generateContent", gotPath)
	require.Equal(t, "test-key", gotGoogleAPIKey)
	require.Empty(t, gotAnthropicVers)
	require.Nil(t, gotBody["model"])
	require.Nil(t, gotBody["messages"])
	contents, ok := gotBody["contents"].([]any)
	require.True(t, ok)
	require.Len(t, contents, 1)
}

func TestStatusProbeService_runAllProbes_PerModelTimeoutDoesNotStarveLaterModels(t *testing.T) {
	origProbeTimeout := statusProbePerModelTimeout
	origRecordTimeout := statusProbeRecordTimeout
	defer func() {
		statusProbePerModelTimeout = origProbeTimeout
		statusProbeRecordTimeout = origRecordTimeout
	}()
	statusProbePerModelTimeout = 20 * time.Millisecond
	statusProbeRecordTimeout = 100 * time.Millisecond

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "claude-key" {
			time.Sleep(60 * time.Millisecond)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"message","id":"msg_1"}`))
	}))
	defer ts.Close()

	cfgJSON := `{
		"enabled": true,
		"interval_minutes": 5,
		"retention_days": 0,
		"models": [
			{"model":"claude-sonnet-4-6","display_name":"Claude","sort_order":0,"enabled":true,"api_key":"claude-key","base_url":"` + ts.URL + `"},
			{"model":"gpt-5.4","display_name":"GPT","sort_order":1,"enabled":true,"api_key":"gpt-key","base_url":"` + ts.URL + `"}
		]
	}`
	repo := &settingRepoStub{
		values: map[string]string{
			SettingKeyStatusProbeConfig: cfgJSON,
		},
	}

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`INSERT INTO status_probe_results`).
		WithArgs("claude-sonnet-4-6", "error", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO status_probe_results`).
		WithArgs("gpt-5.4", "ok", sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	svc := NewStatusProbeService(db, NewSettingService(repo, &config.Config{}))
	svc.httpClient = ts.Client()

	svc.runAllProbes()

	require.NoError(t, mock.ExpectationsWereMet())
}
