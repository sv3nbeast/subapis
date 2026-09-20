package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunCheckForModel_CNProvidersUseRegisteredOpenAICompatibleAdapters(t *testing.T) {
	tests := []struct {
		provider string
		path     string
	}{
		{provider: MonitorProviderKimi, path: providerOpenAIPath},
		{provider: MonitorProviderZhipu, path: providerOpenAIPath},
		{provider: MonitorProviderDeepseek, path: providerOpenAIPath},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			originalClient := monitorHTTPClient
			monitorHTTPClient = &http.Client{Timeout: 5 * time.Second}
			t.Cleanup(func() { monitorHTTPClient = originalClient })

			var body map[string]any
			var authorization string
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() { _ = r.Body.Close() }()
				path = r.URL.Path
				authorization = r.Header.Get("Authorization")
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{
						"message": map[string]any{"content": allPossibleMonitorChallengeAnswers()},
					}},
				}))
			}))
			t.Cleanup(srv.Close)

			res := runCheckForModel(context.Background(), tt.provider, srv.URL, "sk-test", "test-model", nil)

			require.Equal(t, MonitorStatusOperational, res.Status, res.Message)
			require.Equal(t, tt.path, path)
			require.Equal(t, "Bearer sk-test", authorization)
			require.Equal(t, "test-model", body["model"])
			require.Equal(t, false, body["stream"])
			require.True(t, providerSupportsProbe(tt.provider))
		})
	}
}

func TestRunCheckForModel_ZhipuFallsBackToNativePathAfterNotFound(t *testing.T) {
	originalClient := monitorHTTPClient
	monitorHTTPClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() { monitorHTTPClient = originalClient })

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == providerOpenAIPath {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404 page not found"))
			return
		}
		require.Equal(t, providerZhipuPath, r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": allPossibleMonitorChallengeAnswers()},
			}},
		}))
	}))
	t.Cleanup(srv.Close)

	res := runCheckForModel(context.Background(), MonitorProviderZhipu, srv.URL, "sk-test", "glm-5.3-flash", nil)

	require.Equal(t, MonitorStatusOperational, res.Status, res.Message)
	require.Equal(t, []string{providerOpenAIPath, providerZhipuPath}, paths)
}

func allPossibleMonitorChallengeAnswers() string {
	var out strings.Builder
	for i := 0; i <= monitorChallengeMax*2; i++ {
		if i > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(strconv.Itoa(i))
	}
	return out.String()
}
