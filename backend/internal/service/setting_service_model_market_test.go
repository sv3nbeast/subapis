package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type modelMarketSettingRepoStub struct{ values map[string]string }

func (s *modelMarketSettingRepoStub) Get(context.Context, string) (*Setting, error) { return nil, nil }
func (s *modelMarketSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func (s *modelMarketSettingRepoStub) Set(context.Context, string, string) error { return nil }
func (s *modelMarketSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}
func (s *modelMarketSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (s *modelMarketSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return s.values, nil
}
func (s *modelMarketSettingRepoStub) Delete(context.Context, string) error { return nil }

func TestParsePublicModelMarketRate(t *testing.T) {
	require.InDelta(t, 7.35, parsePublicModelMarketRate("7.35", 7.2), 1e-12)
	require.InDelta(t, 7.2, parsePublicModelMarketRate("0", 7.2), 1e-12)
	require.InDelta(t, 7.2, parsePublicModelMarketRate("invalid", 7.2), 1e-12)
	require.InDelta(t, 1.0, normalizePublicModelMarketRate(101, 1), 1e-12)
}

func TestGetPublicSettingsIncludesModelMarketDisplayRates(t *testing.T) {
	svc := NewSettingService(&modelMarketSettingRepoStub{values: map[string]string{
		SettingKeyPublicModelMarketReferenceUSDCNYRate:  "7.35",
		SettingKeyPublicModelMarketSettlementUSDCNYRate: "1",
	}}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.InDelta(t, 7.35, settings.PublicModelMarketReferenceUSDCNYRate, 1e-12)
	require.InDelta(t, 1.0, settings.PublicModelMarketSettlementUSDCNYRate, 1e-12)

	defaults, err := NewSettingService(&modelMarketSettingRepoStub{values: map[string]string{
		SettingKeyPublicModelMarketReferenceUSDCNYRate:  "0",
		SettingKeyPublicModelMarketSettlementUSDCNYRate: "invalid",
	}}, &config.Config{}).GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.InDelta(t, 7.2, defaults.PublicModelMarketReferenceUSDCNYRate, 1e-12)
	require.InDelta(t, 1.0, defaults.PublicModelMarketSettlementUSDCNYRate, 1e-12)
}
