package service

import (
	"context"
	"errors"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/stretchr/testify/require"
)

type webSearchProxyReaderStub struct {
	proxy *Proxy
	err   error
}

func (s *webSearchProxyReaderStub) GetByID(_ context.Context, _ int64) (*Proxy, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.proxy, nil
}

// --- validateWebSearchConfig ---

func TestValidateWebSearchConfig_Nil(t *testing.T) {
	require.NoError(t, validateWebSearchConfig(nil))
}

func TestValidateWebSearchConfig_Valid(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Enabled: true,
		Providers: []WebSearchProviderConfig{
			{Type: "brave", Priority: 1, QuotaLimit: 1000, QuotaRefreshInterval: "monthly"},
			{Type: "tavily", Priority: 2, QuotaLimit: 500, QuotaRefreshInterval: "daily"},
		},
	}
	require.NoError(t, validateWebSearchConfig(cfg))
}

func TestValidateWebSearchConfig_TooManyProviders(t *testing.T) {
	cfg := &WebSearchEmulationConfig{Providers: make([]WebSearchProviderConfig, 11)}
	for i := range cfg.Providers {
		cfg.Providers[i] = WebSearchProviderConfig{Type: "brave"}
	}
	err := validateWebSearchConfig(cfg)
	require.ErrorContains(t, err, "too many providers")
}

func TestValidateWebSearchConfig_InvalidType(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "bing"}},
	}
	require.ErrorContains(t, validateWebSearchConfig(cfg), "invalid type")
}

func TestValidateWebSearchConfig_InvalidQuotaInterval(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", QuotaRefreshInterval: "hourly"}},
	}
	require.ErrorContains(t, validateWebSearchConfig(cfg), "invalid quota_refresh_interval")
}

func TestValidateWebSearchConfig_NegativeQuotaLimit(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", QuotaLimit: -1}},
	}
	require.ErrorContains(t, validateWebSearchConfig(cfg), "quota_limit must be >= 0")
}

func TestValidateWebSearchConfig_DuplicateType(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{
			{Type: "brave", Priority: 1},
			{Type: "brave", Priority: 2},
		},
	}
	require.ErrorContains(t, validateWebSearchConfig(cfg), "duplicate type")
}

func TestValidateWebSearchConfig_EmptyQuotaInterval(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", QuotaRefreshInterval: ""}},
	}
	require.NoError(t, validateWebSearchConfig(cfg))
}

func TestValidateWebSearchConfig_ZeroQuotaLimit(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", QuotaLimit: 0}},
	}
	require.NoError(t, validateWebSearchConfig(cfg))
}

// --- parseWebSearchConfigJSON ---

func TestParseWebSearchConfigJSON_ValidJSON(t *testing.T) {
	raw := `{"enabled":true,"providers":[{"type":"brave","api_key":"sk-xxx"}]}`
	cfg := parseWebSearchConfigJSON(raw)
	require.True(t, cfg.Enabled)
	require.Len(t, cfg.Providers, 1)
	require.Equal(t, "brave", cfg.Providers[0].Type)
}

func TestParseWebSearchConfigJSON_EmptyString(t *testing.T) {
	cfg := parseWebSearchConfigJSON("")
	require.False(t, cfg.Enabled)
	require.Empty(t, cfg.Providers)
}

func TestParseWebSearchConfigJSON_InvalidJSON(t *testing.T) {
	cfg := parseWebSearchConfigJSON("not{json")
	require.False(t, cfg.Enabled)
	require.Empty(t, cfg.Providers)
}

// --- SanitizeWebSearchConfig ---

func TestSanitizeWebSearchConfig_MaskAPIKey(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Enabled: true,
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-secret-xxx"},
		},
	}
	out := SanitizeWebSearchConfig(cfg)
	require.Equal(t, "", out.Providers[0].APIKey)
	require.True(t, out.Providers[0].APIKeyConfigured)
}

func TestSanitizeWebSearchConfig_NoAPIKey(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", APIKey: ""}},
	}
	out := SanitizeWebSearchConfig(cfg)
	require.Equal(t, "", out.Providers[0].APIKey)
	require.False(t, out.Providers[0].APIKeyConfigured)
}

func TestSanitizeWebSearchConfig_Nil(t *testing.T) {
	require.Nil(t, SanitizeWebSearchConfig(nil))
}

func TestSanitizeWebSearchConfig_PreservesOtherFields(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Enabled: true,
		Providers: []WebSearchProviderConfig{
			{Type: "brave", APIKey: "secret", Priority: 10, QuotaLimit: 1000},
		},
	}
	out := SanitizeWebSearchConfig(cfg)
	require.True(t, out.Enabled)
	require.Equal(t, 10, out.Providers[0].Priority)
	require.Equal(t, int64(1000), out.Providers[0].QuotaLimit)
}

func TestTestWebSearch_ManagerNotInitialized(t *testing.T) {
	SetWebSearchManager(nil)
	_, err := TestWebSearch(context.Background(), "OpenAI")
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.Equal(t, "WEB_SEARCH_EMULATION_DISABLED", infraerrors.Reason(err))
}

func TestTestWebSearch_NoAvailableProvider(t *testing.T) {
	SetWebSearchManager(websearch.NewManager([]websearch.ProviderConfig{
		{Type: "brave", APIKey: ""},
	}, nil))
	_, err := TestWebSearch(context.Background(), "OpenAI")
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.Equal(t, "WEB_SEARCH_NO_PROVIDER", infraerrors.Reason(err))
}

func TestBuildWebSearchProviderConfigs_ResolvesProxy(t *testing.T) {
	proxyID := int64(42)
	svc := NewSettingService(nil, nil)
	svc.SetWebSearchProxyReader(&webSearchProxyReaderStub{
		proxy: &Proxy{
			ID:       proxyID,
			Protocol: "http",
			Host:     "127.0.0.1",
			Port:     8080,
			Status:   StatusActive,
		},
	})

	cfg := &WebSearchEmulationConfig{
		Enabled: true,
		Providers: []WebSearchProviderConfig{{
			Type: "brave", APIKey: "secret", ProxyID: &proxyID,
		}},
	}

	providers := svc.buildWebSearchProviderConfigs(context.Background(), cfg)
	require.Len(t, providers, 1)
	require.Equal(t, proxyID, providers[0].ProxyID)
	require.Equal(t, "http://127.0.0.1:8080", providers[0].ProxyURL)
}

func TestBuildWebSearchProviderConfigs_IgnoresProxyLookupFailure(t *testing.T) {
	proxyID := int64(42)
	svc := NewSettingService(nil, nil)
	svc.SetWebSearchProxyReader(&webSearchProxyReaderStub{err: errors.New("boom")})

	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{
			Type: "brave", APIKey: "secret", ProxyID: &proxyID,
		}},
	}

	providers := svc.buildWebSearchProviderConfigs(context.Background(), cfg)
	require.Len(t, providers, 1)
	require.Zero(t, providers[0].ProxyID)
	require.Empty(t, providers[0].ProxyURL)
}

func TestSanitizeWebSearchConfig_DoesNotMutateOriginal(t *testing.T) {
	cfg := &WebSearchEmulationConfig{
		Providers: []WebSearchProviderConfig{{Type: "brave", APIKey: "secret"}},
	}
	_ = SanitizeWebSearchConfig(cfg)
	require.Equal(t, "secret", cfg.Providers[0].APIKey)
}
