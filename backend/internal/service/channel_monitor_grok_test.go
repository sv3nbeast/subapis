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

func TestRunCheckForModel_GrokUsesOpenAICompatibleChatCompletions(t *testing.T) {
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
				"message": map[string]any{"content": allGrokChallengeAnswers()},
			}},
		}))
	}))
	t.Cleanup(srv.Close)

	result := runCheckForModel(context.Background(), MonitorProviderGrok, srv.URL, "xai-test-key", "grok-4", nil)

	require.Equal(t, MonitorStatusOperational, result.Status, result.Message)
	require.Equal(t, providerOpenAIPath, path)
	require.Equal(t, "Bearer xai-test-key", authorization)
	require.Equal(t, "grok-4", body["model"])
	require.NotEmpty(t, body["messages"])
	require.Equal(t, false, body["stream"])
	require.NoError(t, validateProvider(MonitorProviderGrok))
	require.NoError(t, validateAPIMode(MonitorProviderGrok, MonitorAPIModeChatCompletions))
	require.ErrorIs(t, validateAPIMode(MonitorProviderGrok, MonitorAPIModeResponses), ErrChannelMonitorInvalidAPIMode)
}

func allGrokChallengeAnswers() string {
	var out strings.Builder
	for i := 0; i <= monitorChallengeMax*2; i++ {
		if i > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(strconv.Itoa(i))
	}
	return out.String()
}
