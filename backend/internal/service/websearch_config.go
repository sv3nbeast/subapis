package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"golang.org/x/sync/singleflight"
)

// WebSearchEmulationConfig holds the global web search emulation configuration.
type WebSearchEmulationConfig struct {
	Enabled   bool                      `json:"enabled"`
	Providers []WebSearchProviderConfig `json:"providers"`
}

// WebSearchProviderConfig describes a single search provider (Brave or Tavily).
type WebSearchProviderConfig struct {
	Type                 string `json:"type"`                   // websearch.ProviderTypeBrave | Tavily
	APIKey               string `json:"api_key,omitempty"`      // secret — omitted in API responses
	APIKeyConfigured     bool   `json:"api_key_configured"`     // read-only mask
	Priority             int    `json:"priority"`               // lower = higher priority
	QuotaLimit           int64  `json:"quota_limit"`            // 0 = unlimited
	QuotaRefreshInterval string `json:"quota_refresh_interval"` // websearch.QuotaRefresh*
	QuotaUsed            int64  `json:"quota_used,omitempty"`   // read-only: current period usage
	ProxyID              *int64 `json:"proxy_id"`               // optional proxy association
	ExpiresAt            *int64 `json:"expires_at,omitempty"`   // optional expiration timestamp
}

type webSearchProxyReader interface {
	GetByID(ctx context.Context, id int64) (*Proxy, error)
}

// --- Validation ---

const maxWebSearchProviders = 10

var validProviderTypes = map[string]bool{
	websearch.ProviderTypeBrave:  true,
	websearch.ProviderTypeTavily: true,
}

var validQuotaIntervals = map[string]bool{
	websearch.QuotaRefreshDaily:   true,
	websearch.QuotaRefreshWeekly:  true,
	websearch.QuotaRefreshMonthly: true,
	"":                            true, // defaults to monthly
}

func validateWebSearchConfig(cfg *WebSearchEmulationConfig) error {
	if cfg == nil {
		return nil
	}
	if len(cfg.Providers) > maxWebSearchProviders {
		return fmt.Errorf("too many providers (max %d)", maxWebSearchProviders)
	}
	seen := make(map[string]bool, len(cfg.Providers))
	for i, p := range cfg.Providers {
		if !validProviderTypes[p.Type] {
			return fmt.Errorf("provider[%d]: invalid type %q", i, p.Type)
		}
		if !validQuotaIntervals[p.QuotaRefreshInterval] {
			return fmt.Errorf("provider[%d]: invalid quota_refresh_interval %q", i, p.QuotaRefreshInterval)
		}
		if p.QuotaLimit < 0 {
			return fmt.Errorf("provider[%d]: quota_limit must be >= 0", i)
		}
		if seen[p.Type] {
			return fmt.Errorf("provider[%d]: duplicate type %q", i, p.Type)
		}
		seen[p.Type] = true
	}
	return nil
}

// --- In-process cache (same pattern as gateway forwarding settings) ---

const sfKeyWebSearchConfig = "web_search_emulation_config"

type cachedWebSearchEmulationConfig struct {
	config    *WebSearchEmulationConfig
	expiresAt int64 // unix nano
}

var webSearchEmulationCache atomic.Value // *cachedWebSearchEmulationConfig
var webSearchEmulationSF singleflight.Group

const (
	webSearchEmulationCacheTTL  = 60 * time.Second
	webSearchEmulationErrorTTL  = 5 * time.Second
	webSearchEmulationDBTimeout = 5 * time.Second
)

// GetWebSearchEmulationConfig returns the configuration with in-process cache + singleflight.
func (s *SettingService) GetWebSearchEmulationConfig(ctx context.Context) (*WebSearchEmulationConfig, error) {
	if cached := webSearchEmulationCache.Load(); cached != nil {
		c, ok := cached.(*cachedWebSearchEmulationConfig)
		if ok && c != nil && time.Now().UnixNano() < c.expiresAt {
			return c.config, nil
		}
	}
	result, err, _ := webSearchEmulationSF.Do(sfKeyWebSearchConfig, func() (any, error) {
		return s.loadWebSearchConfigFromDB()
	})
	if err != nil {
		return &WebSearchEmulationConfig{}, err
	}
	cfg, ok := result.(*WebSearchEmulationConfig)
	if !ok || cfg == nil {
		return &WebSearchEmulationConfig{}, fmt.Errorf("websearch: unexpected config result type %T", result)
	}
	return cfg, nil
}

func (s *SettingService) loadWebSearchConfigFromDB() (*WebSearchEmulationConfig, error) {
	dbCtx, cancel := context.WithTimeout(context.Background(), webSearchEmulationDBTimeout)
	defer cancel()

	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyWebSearchEmulationConfig)
	if err != nil {
		webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{
			config:    &WebSearchEmulationConfig{},
			expiresAt: time.Now().Add(webSearchEmulationErrorTTL).UnixNano(),
		})
		return &WebSearchEmulationConfig{}, err
	}
	cfg := parseWebSearchConfigJSON(raw)
	webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{
		config:    cfg,
		expiresAt: time.Now().Add(webSearchEmulationCacheTTL).UnixNano(),
	})
	return cfg, nil
}

func parseWebSearchConfigJSON(raw string) *WebSearchEmulationConfig {
	cfg := &WebSearchEmulationConfig{}
	if raw == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		slog.Warn("websearch: failed to parse config JSON", "error", err)
		return &WebSearchEmulationConfig{}
	}
	return cfg
}

// SaveWebSearchEmulationConfig validates and persists the configuration.
// Empty API keys in the input are preserved from the existing config.
func (s *SettingService) SaveWebSearchEmulationConfig(ctx context.Context, cfg *WebSearchEmulationConfig) error {
	if err := validateWebSearchConfig(cfg); err != nil {
		return infraerrors.BadRequest("INVALID_WEB_SEARCH_CONFIG", err.Error())
	}
	s.mergeExistingAPIKeys(ctx, cfg)

	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("websearch: marshal config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyWebSearchEmulationConfig, string(data)); err != nil {
		return fmt.Errorf("websearch: save config: %w", err)
	}
	// Invalidate: forget singleflight first, then store new value
	webSearchEmulationSF.Forget(sfKeyWebSearchConfig)
	webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{
		config:    cfg,
		expiresAt: time.Now().Add(webSearchEmulationCacheTTL).UnixNano(),
	})

	// Hot-reload: rebuild the global Manager with new config
	s.RebuildWebSearchManager(ctx)
	return nil
}

// mergeExistingAPIKeys preserves API keys from the current config when incoming value is empty.
func (s *SettingService) mergeExistingAPIKeys(ctx context.Context, cfg *WebSearchEmulationConfig) {
	existing, _ := s.getWebSearchEmulationConfigRaw(ctx)
	if existing == nil || cfg == nil {
		return
	}
	existingByType := make(map[string]string, len(existing.Providers))
	for _, p := range existing.Providers {
		if p.APIKey != "" {
			existingByType[p.Type] = p.APIKey
		}
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].APIKey == "" {
			if key, ok := existingByType[cfg.Providers[i].Type]; ok {
				cfg.Providers[i].APIKey = key
			}
		}
	}
}

func (s *SettingService) getWebSearchEmulationConfigRaw(ctx context.Context) (*WebSearchEmulationConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyWebSearchEmulationConfig)
	if err != nil {
		return nil, err
	}
	return parseWebSearchConfigJSON(raw), nil
}

// IsWebSearchEmulationEnabled is a quick check for whether the global switch is on.
func (s *SettingService) IsWebSearchEmulationEnabled(ctx context.Context) bool {
	cfg, err := s.GetWebSearchEmulationConfig(ctx)
	if err != nil {
		return false
	}
	return cfg.Enabled && len(cfg.Providers) > 0
}

// SetWebSearchRedisClient injects the Redis client used for quota tracking.
// Call after construction, before first use. Triggers initial Manager build.
func (s *SettingService) SetWebSearchRedisClient(ctx context.Context, redisClient *websearch.RedisClient) {
	s.webSearchRedis = redisClient
	s.RebuildWebSearchManager(ctx)
}

// RebuildWebSearchManager reads the current config and (re)creates the global websearch.Manager.
// Called on startup and after SaveWebSearchEmulationConfig.
func (s *SettingService) RebuildWebSearchManager(ctx context.Context) {
	cfg, err := s.GetWebSearchEmulationConfig(ctx)
	if err != nil || !cfg.Enabled || len(cfg.Providers) == 0 {
		SetWebSearchManager(nil)
		return
	}
	providerConfigs := s.buildWebSearchProviderConfigs(ctx, cfg)
	SetWebSearchManager(websearch.NewManager(providerConfigs, s.webSearchRedis))
	slog.Info("websearch: manager rebuilt", "provider_count", len(providerConfigs))
}

func (s *SettingService) buildWebSearchProviderConfigs(ctx context.Context, cfg *WebSearchEmulationConfig) []websearch.ProviderConfig {
	providerConfigs := make([]websearch.ProviderConfig, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		pc := websearch.ProviderConfig{
			Type:                 p.Type,
			APIKey:               p.APIKey,
			Priority:             p.Priority,
			QuotaLimit:           p.QuotaLimit,
			QuotaRefreshInterval: p.QuotaRefreshInterval,
			ExpiresAt:            p.ExpiresAt,
		}
		if p.ProxyID != nil && s.webSearchProxyReader != nil {
			proxy, err := s.webSearchProxyReader.GetByID(ctx, *p.ProxyID)
			if err != nil {
				slog.Warn("websearch: failed to resolve provider proxy", "provider", p.Type, "proxy_id", *p.ProxyID, "error", err)
			} else if proxy != nil && proxy.IsActive() {
				pc.ProxyID = proxy.ID
				pc.ProxyURL = proxy.URL()
			}
		}
		providerConfigs = append(providerConfigs, pc)
	}
	return providerConfigs
}

// WebSearchTestResult holds the result of a search test.
type WebSearchTestResult struct {
	Provider string                   `json:"provider"`
	Results  []websearch.SearchResult `json:"results"`
	Query    string                   `json:"query"`
}

// TestWebSearch executes a test search using the currently configured Manager.
// Uses Manager.TestSearch which bypasses quota tracking.
const testSearchTimeout = 15 * time.Second

func TestWebSearch(ctx context.Context, query string) (*WebSearchTestResult, error) {
	mgr := getWebSearchManager()
	if mgr == nil {
		return nil, infraerrors.BadRequest(
			"WEB_SEARCH_EMULATION_DISABLED",
			"web search emulation is disabled or not configured",
		)
	}
	testCtx, cancel := context.WithTimeout(ctx, testSearchTimeout)
	defer cancel()
	resp, providerName, err := mgr.TestSearch(testCtx, websearch.SearchRequest{
		Query:      query,
		MaxResults: webSearchDefaultMaxResults,
	})
	if err != nil {
		if strings.Contains(err.Error(), "no available provider") {
			return nil, infraerrors.BadRequest(
				"WEB_SEARCH_NO_PROVIDER",
				"no available web search provider",
			).WithCause(err)
		}
		return nil, infraerrors.ServiceUnavailable(
			"WEB_SEARCH_TEST_FAILED",
			"web search test failed",
		).WithCause(err)
	}
	return &WebSearchTestResult{
		Provider: providerName,
		Results:  resp.Results,
		Query:    resp.Query,
	}, nil
}

// PopulateWebSearchUsage returns a copy with quota usage populated from the current manager.
func PopulateWebSearchUsage(ctx context.Context, cfg *WebSearchEmulationConfig) *WebSearchEmulationConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	out.Providers = make([]WebSearchProviderConfig, len(cfg.Providers))

	mgr := getWebSearchManager()
	for i, p := range cfg.Providers {
		out.Providers[i] = p
		out.Providers[i].APIKeyConfigured = p.APIKey != ""
		if mgr != nil {
			used, _ := mgr.GetUsage(ctx, p.Type, p.QuotaRefreshInterval)
			out.Providers[i].QuotaUsed = used
		}
	}
	return &out
}

// ResetWebSearchUsage deletes the usage counter for the given provider type.
func ResetWebSearchUsage(ctx context.Context, providerType string) error {
	mgr := getWebSearchManager()
	if mgr == nil {
		return fmt.Errorf("web search manager not initialized")
	}
	return mgr.ResetUsage(ctx, providerType)
}

// SanitizeWebSearchConfig returns a copy with api_key fields masked for API responses.
func SanitizeWebSearchConfig(cfg *WebSearchEmulationConfig) *WebSearchEmulationConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	out.Providers = make([]WebSearchProviderConfig, len(cfg.Providers))
	for i, p := range cfg.Providers {
		out.Providers[i] = p
		out.Providers[i].APIKeyConfigured = p.APIKey != ""
		out.Providers[i].APIKey = "" // never return the secret
	}
	return &out
}
