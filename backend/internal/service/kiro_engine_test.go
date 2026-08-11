package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestKiroEngineForGroup_DefaultsToLegacy(t *testing.T) {
	svc := &GatewayService{cfg: &config.Config{}}
	groupID := int64(29)
	require.Equal(t, KiroEngineLegacy, svc.KiroEngineForGroup(&groupID))
	require.Equal(t, KiroEngineLegacy, (*GatewayService)(nil).KiroEngineForGroup(&groupID))
}

func TestKiroEngineForGroup_GlobalNianzs(t *testing.T) {
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineNianzs}}}
	groupID := int64(29)
	require.Equal(t, KiroEngineNianzs, svc.KiroEngineForGroup(&groupID))
	require.Equal(t, KiroEngineNianzs, svc.KiroEngineForGroup(nil))
}

func TestKiroEngineForGroup_LegacyCanaryAllowlist(t *testing.T) {
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		KiroEngine:         config.KiroEngineLegacy,
		KiroNianzsGroupIDs: []int64{29, 33},
	}}}
	group29 := int64(29)
	group30 := int64(30)
	require.Equal(t, KiroEngineNianzs, svc.KiroEngineForGroup(&group29))
	require.Equal(t, KiroEngineLegacy, svc.KiroEngineForGroup(&group30))
}

func TestUseNianzsKiroSchedulerOnlyForKiroGroup(t *testing.T) {
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineNianzs}}}
	groupID := int64(29)
	kiroCtx := svc.withGroupContext(context.Background(), &Group{ID: groupID, Platform: PlatformKiro, Status: StatusActive, Hydrated: true})
	commonCtx := svc.withGroupContext(context.Background(), &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true})

	require.True(t, svc.useNianzsKiroScheduler(kiroCtx, &groupID))
	require.False(t, svc.useNianzsKiroScheduler(commonCtx, &groupID))
}
