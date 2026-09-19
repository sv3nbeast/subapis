package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration243AddsKimiChannelPricingWithoutRestrictingModels(t *testing.T) {
	content, err := FS.ReadFile("243_add_kimi_channel_pricing.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "name = 'Kimi最新模型'")
	require.Contains(t, sql, "platform = 'kimi'")
	require.Contains(t, sql, "'Kimi 模型'")
	require.Contains(t, sql, "billing_model_source = 'requested'")
	require.Contains(t, sql, "restrict_models = false")
	require.Contains(t, sql, "ON CONFLICT (channel_id, group_id) DO NOTHING")
	require.Contains(t, sql, "existing.display_only = false")

	for _, model := range []string{"kimi-k3", "kimi-k2.7-code", "kimi-k2.6"} {
		require.Contains(t, sql, model)
	}
	for _, price := range []string{
		"0.000003000000",
		"0.000015000000",
		"0.000000300000",
		"0.000000950000",
		"0.000004000000",
		"0.000000190000",
		"0.000000150000",
	} {
		require.Contains(t, sql, price)
	}

	require.Equal(t, 3, strings.Count(sql, "INSERT INTO channel_model_pricing"))
	require.Equal(t, 3, strings.Count(sql, "AND p.models @>"))
}
