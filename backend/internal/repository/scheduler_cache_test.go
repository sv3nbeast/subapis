package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBuildSchedulerMetadataAccount_PreservesModelRateLimitsForSelection(t *testing.T) {
	resetAt := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Second)

	account := service.Account{
		ID:          101,
		Name:        "ag-rate-limited",
		Platform:    service.PlatformAntigravity,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-6": map[string]any{
					"rate_limit_reset_at": resetAt.Format(time.RFC3339),
				},
			},
			"unused_large_field": "should_not_be_copied",
		},
	}

	metadata := buildSchedulerMetadataAccount(account)

	require.NotNil(t, metadata.Extra)
	require.Contains(t, metadata.Extra, "model_rate_limits")
	require.NotContains(t, metadata.Extra, "unused_large_field")
	require.False(t, metadata.IsSchedulableForModelWithContext(context.Background(), "claude-sonnet-4-6"))
	require.True(t, metadata.IsSchedulableForModelWithContext(context.Background(), "claude-opus-4-6"))
}

func TestBuildSchedulerMetadataAccount_PreservesOpenAIKiroBridgeFlag(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		account := service.Account{
			ID:       102,
			Platform: service.PlatformKiro,
			Type:     service.AccountTypeOAuth,
			Extra: map[string]any{
				"openai_kiro_bridge_enabled": enabled,
				"unused_large_field":         "drop-me",
			},
		}

		metadata := buildSchedulerMetadataAccount(account)

		require.Equal(t, enabled, metadata.Extra["openai_kiro_bridge_enabled"])
		require.NotContains(t, metadata.Extra, "unused_large_field")
	}
}
