package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// These fixtures are copied from nianzs/sub2api@d483aefe. They protect the
// group-level cache accounting inputs consumed by the namespaced runtime.
func TestNianzsGroupKiroCacheEmulationModes(t *testing.T) {
	uniform := &Group{
		Platform:                        PlatformKiro,
		KiroCacheEmulationEnabled:       true,
		KiroCacheEmulationRatio:         0.5,
		KiroCacheEmulationMode:          KiroCacheEmulationModeUniform,
		KiroCacheCreationEmulationRatio: 0.9,
		KiroCacheReadEmulationRatio:     0.2,
	}
	creationRatio, readRatio := uniform.EffectiveKiroCacheEmulationRatios()
	require.InDelta(t, 0.5, creationRatio, 1e-12)
	require.InDelta(t, 0.5, readRatio, 1e-12)
	require.True(t, uniform.EffectiveKiroCacheEmulationEnabled())

	independent := &Group{
		Platform:                        PlatformKiro,
		KiroCacheEmulationEnabled:       true,
		KiroCacheEmulationRatio:         0.5,
		KiroCacheEmulationMode:          KiroCacheEmulationModeIndependent,
		KiroCacheCreationEmulationRatio: 0.9,
		KiroCacheReadEmulationRatio:     0.2,
	}
	creationRatio, readRatio = independent.EffectiveKiroCacheEmulationRatios()
	require.InDelta(t, 0.9, creationRatio, 1e-12)
	require.InDelta(t, 0.2, readRatio, 1e-12)
	require.True(t, independent.EffectiveKiroCacheEmulationEnabled())

	independent.KiroCacheCreationEmulationRatio = 0
	independent.KiroCacheReadEmulationRatio = 0
	require.False(t, independent.EffectiveKiroCacheEmulationEnabled())
}

func TestNianzsNormalizeKiroCacheEmulationFieldsSynchronizesAndClears(t *testing.T) {
	uniform := &Group{
		Platform:                        PlatformKiro,
		KiroCacheEmulationEnabled:       true,
		KiroCacheEmulationRatio:         0,
		KiroCacheEmulationMode:          KiroCacheEmulationModeUniform,
		KiroCacheCreationEmulationRatio: 0.8,
		KiroCacheReadEmulationRatio:     0.4,
	}
	normalizeKiroCacheEmulationFields(uniform)
	require.Zero(t, uniform.KiroCacheEmulationRatio)
	require.Zero(t, uniform.KiroCacheCreationEmulationRatio)
	require.Zero(t, uniform.KiroCacheReadEmulationRatio)
	require.False(t, uniform.EffectiveKiroCacheEmulationEnabled())

	independent := &Group{
		Platform:                        PlatformKiro,
		KiroCacheEmulationEnabled:       true,
		KiroCacheEmulationRatio:         math.NaN(),
		KiroCacheEmulationMode:          KiroCacheEmulationModeIndependent,
		KiroCacheCreationEmulationRatio: 0,
		KiroCacheReadEmulationRatio:     0,
	}
	normalizeKiroCacheEmulationFields(independent)
	require.Zero(t, independent.KiroCacheEmulationRatio)
	require.Zero(t, independent.KiroCacheCreationEmulationRatio)
	require.Zero(t, independent.KiroCacheReadEmulationRatio)

	nonKiro := &Group{
		Platform:                        PlatformAnthropic,
		KiroCacheEmulationEnabled:       true,
		KiroCacheEmulationMode:          KiroCacheEmulationModeIndependent,
		KiroCacheCreationEmulationRatio: 0.8,
		KiroCacheReadEmulationRatio:     0.3,
	}
	normalizeKiroCacheEmulationFields(nonKiro)
	require.False(t, nonKiro.KiroCacheEmulationEnabled)
	require.Equal(t, KiroCacheEmulationModeUniform, nonKiro.KiroCacheEmulationMode)
	require.Zero(t, nonKiro.KiroCacheCreationEmulationRatio)
	require.Zero(t, nonKiro.KiroCacheReadEmulationRatio)
}
