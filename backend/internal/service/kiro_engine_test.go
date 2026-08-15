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

func TestNianzsKiroSelectionDoesNotReplaceStickyBindingBeforeSuccess(t *testing.T) {
	groupID := int64(29)
	sessionHash := "warm-session"
	cache := &stubGatewayCache{sessionBindings: map[string]int64{sessionHash: 2536}}
	svc := &GatewayService{
		cache: cache,
		cfg:   &config.Config{Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineNianzs}},
	}
	ctx := svc.withGroupContext(context.Background(), &Group{
		ID:       groupID,
		Platform: PlatformKiro,
		Status:   StatusActive,
		Hydrated: true,
	})

	require.NoError(t, svc.bindGatewayStickySessionDuringSelection(ctx, &groupID, sessionHash, 2537))
	require.Equal(t, int64(2536), cache.sessionBindings[sessionHash], "a selected account must not replace the warm binding before terminal success")
}

func TestNianzsKiroSelectionResultRequiresSuccessBeforeBinding(t *testing.T) {
	groupID := int64(29)
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineNianzs}}}
	account := &Account{ID: 2537, Platform: PlatformKiro}

	selection, err := svc.newSelectionResultNianzs(context.Background(), account, true, nil, nil)
	require.NoError(t, err)
	require.True(t, selection.DeferStickyMigration)

	svc.applyKiroSelectionBindingPolicy(selection, &groupID, "warm-session", false, false)
	require.True(t, selection.DeferStickyMigration)
	require.False(t, svc.shouldBindSelectionBeforeSuccess(context.Background(), account, &groupID, "warm-session", false, selection.DeferStickyMigration))
}

func TestNianzsEngineDoesNotDeferNonKiroGroupSelection(t *testing.T) {
	groupID := int64(30)
	sessionHash := "anthropic-session"
	cache := &stubGatewayCache{sessionBindings: map[string]int64{sessionHash: 100}}
	svc := &GatewayService{
		cache: cache,
		cfg:   &config.Config{Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineNianzs}},
	}
	ctx := svc.withGroupContext(context.Background(), &Group{
		ID:       groupID,
		Platform: PlatformAnthropic,
		Status:   StatusActive,
		Hydrated: true,
	})

	require.NoError(t, svc.bindGatewayStickySessionDuringSelection(ctx, &groupID, sessionHash, 101))
	require.Equal(t, int64(101), cache.sessionBindings[sessionHash])
}
