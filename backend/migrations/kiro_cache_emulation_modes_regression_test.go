package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration196AddsIndependentKiroCacheRatiosWithoutChangingExistingBilling(t *testing.T) {
	content, err := FS.ReadFile("196_add_group_kiro_cache_emulation_modes.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, column := range []string{
		"kiro_cache_emulation_mode",
		"kiro_cache_creation_emulation_ratio",
		"kiro_cache_read_emulation_ratio",
	} {
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS "+column)
	}
	require.Contains(t, sql, "kiro_cache_creation_emulation_ratio = kiro_cache_emulation_ratio")
	require.Contains(t, sql, "kiro_cache_read_emulation_ratio = kiro_cache_emulation_ratio")
	require.Contains(t, sql, "CHECK (kiro_cache_emulation_mode IN ('uniform', 'independent'))")
	require.Contains(t, sql, "CHECK (kiro_cache_creation_emulation_ratio >= 0 AND kiro_cache_creation_emulation_ratio <= 1)")
	require.Contains(t, sql, "CHECK (kiro_cache_read_emulation_ratio >= 0 AND kiro_cache_read_emulation_ratio <= 1)")
}
