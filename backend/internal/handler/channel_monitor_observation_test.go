package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMonitorObservationPublicDTO(t *testing.T) {
	now := time.Date(2026, 9, 8, 6, 0, 0, 0, time.UTC)
	item := userMonitorViewToItem(&service.UserMonitorView{
		PrimaryStatus: "error", PrimaryCheckedAt: &now, IntervalSeconds: 3600,
		CheckMode: "probe", ProbePath: "/v1/chat/completions",
	}, false)
	raw, err := json.Marshal(item)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, "error", decoded["primary_status"])
	require.Equal(t, "2026-09-08T06:00:00Z", decoded["primary_checked_at"])
	require.Equal(t, float64(3600), decoded["interval_seconds"])
	require.Equal(t, "/v1/chat/completions", decoded["probe_path"])
	require.NotContains(t, decoded, "api_key")
	require.NotContains(t, decoded, "endpoint")
	require.NotContains(t, decoded, "message")
}
