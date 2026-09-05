package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestEnvBindingsPreserveAbsentStickyEscapeAndExplicitAlias(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("GATEWAY_OPENAI_SCHEDULER_STICKY_ESCAPE_ENABLED", "")
	t.Setenv("ENABLE_SERVER_TIMING", "true")
	require.NoError(t, viper.BindEnv("server.enable_server_timing", "ENABLE_SERVER_TIMING"))
	setDefaults()
	require.False(t, viper.IsSet("gateway.openai_scheduler.sticky_escape_enabled"), "binding must not manufacture an explicit false default")
	require.True(t, viper.GetBool("server.enable_server_timing"), "manual environment alias remains authoritative")
}
