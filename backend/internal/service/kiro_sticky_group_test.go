package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroup_EffectiveKiroStickySessionTTLSecondsRuntime(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{name: "default", in: 0, want: DefaultKiroStickySessionTTLSeconds},
		{name: "min", in: 1, want: MinKiroStickySessionTTLSeconds},
		{name: "custom", in: 7200, want: 7200},
		{name: "max", in: 999999, want: MaxKiroStickySessionTTLSeconds},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := &Group{Platform: PlatformKiro, KiroStickySessionTTLSeconds: tc.in}
			require.Equal(t, tc.want, group.EffectiveKiroStickySessionTTLSeconds())
		})
	}
}

func TestNormalizeGroupRuntimeFieldsKiroStickySettingsRuntime(t *testing.T) {
	group := &Group{
		Platform:                    PlatformKiro,
		KiroAutoStickyEnabled:       true,
		KiroStickySessionTTLSeconds: 1,
		KiroCacheEmulationEnabled:   true,
		KiroCacheEmulationRatio:     2,
	}

	NormalizeGroupRuntimeFields(group)

	require.True(t, group.KiroAutoStickyEnabled)
	require.Equal(t, MinKiroStickySessionTTLSeconds, group.KiroStickySessionTTLSeconds)
	require.Equal(t, 1.0, group.KiroCacheEmulationRatio)
}

func TestNormalizeGroupRuntimeFieldsClearsKiroSettingsForNonKiroRuntime(t *testing.T) {
	group := &Group{
		Platform:                    PlatformOpenAI,
		KiroAutoStickyEnabled:       true,
		KiroStickySessionTTLSeconds: 3600,
		KiroCacheEmulationEnabled:   true,
		KiroCacheEmulationRatio:     0.5,
	}

	NormalizeGroupRuntimeFields(group)

	require.False(t, group.KiroAutoStickyEnabled)
	require.Zero(t, group.KiroStickySessionTTLSeconds)
	require.False(t, group.KiroCacheEmulationEnabled)
	require.Zero(t, group.KiroCacheEmulationRatio)
}
