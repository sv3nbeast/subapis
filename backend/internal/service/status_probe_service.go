package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// ─── Config types ────────────────────────────────────────────────────────────

// StatusProbeModelConfig describes a single model to probe.
type StatusProbeModelConfig struct {
	Model       string `json:"model"`
	DisplayName string `json:"display_name"`
	SortOrder   int    `json:"sort_order"`
	Enabled     bool   `json:"enabled"`
	ApiKey      string `json:"api_key"`
	BaseURL     string `json:"base_url"`
}

// StatusProbeConfig is the JSON-serialised configuration stored in the settings table.
type StatusProbeConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
	RetentionDays   int  `json:"retention_days"`
	// Deprecated: kept for backward compatibility with pre-1bd4bfb4 configs.
	ApiKey string `json:"api_key,omitempty"`
	// Deprecated: kept for backward compatibility with pre-1bd4bfb4 configs.
	BaseURL string                   `json:"base_url,omitempty"`
	Models  []StatusProbeModelConfig `json:"models"`
}

// ─── Response types ──────────────────────────────────────────────────────────

// ProbeFailure represents a single recent failure event.
type ProbeFailure struct {
	Timestamp    time.Time `json:"time"`
	ErrorMessage string    `json:"error"`
	LatencyMs    int       `json:"latency_ms"`
}

// HourlyStat contains aggregated probe statistics for one hour.
type HourlyStat struct {
	Hour         time.Time      `json:"hour"`
	TotalProbes  int            `json:"total"`
	SuccessCount int            `json:"success"`
	Failures     []ProbeFailure `json:"failures,omitempty"`
	AvgLatencyMs float64        `json:"avg_latency_ms"`
}

// ModelStatus describes the current state and history of a single monitored model.
type ModelStatus struct {
	Model          string         `json:"model"`
	DisplayName    string         `json:"display_name"`
	CurrentStatus  string         `json:"current_status"`
	UptimePercent  float64        `json:"uptime_percentage"`
	AvgLatencyMs   float64        `json:"avg_latency_ms"`
	TotalProbes    int            `json:"total_probes"`
	RecentFailures []ProbeFailure `json:"recent_failures"`
	HourlyStats    []HourlyStat   `json:"hourly_stats"`
}

// ServiceStatusResponse is the top-level response returned by GetStatus.
type ServiceStatusResponse struct {
	OverallStatus   string        `json:"overall_status"`
	IntervalMinutes int           `json:"interval_minutes"`
	Models          []ModelStatus `json:"models"`
	UpdatedAt       time.Time     `json:"last_updated"`
}

// ─── Internal raw result type ────────────────────────────────────────────────

// probeRawResult is a row from the status_probe_results table.
type probeRawResult struct {
	ID           int64
	Model        string
	Status       string
	LatencyMs    int
	ErrorMessage sql.NullString
	CreatedAt    time.Time
}

// ─── Cron parser (package-level, same pattern as other services) ─────────────

var statusProbeCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

var (
	statusProbeLoadConfigTimeout = 5 * time.Second
	// Keep per-model probes bounded without letting one slow upstream starve the rest of the batch.
	statusProbePerModelTimeout = 25 * time.Second
	statusProbeRecordTimeout   = 5 * time.Second
	statusProbeCleanupTimeout  = 15 * time.Second
	statusProbeErrorBodyLimit  int64 = 4096
)

// ─── Service struct ──────────────────────────────────────────────────────────

// StatusProbeService periodically probes monitored models via HTTP requests
// to the gateway API and records the results for uptime monitoring.
type StatusProbeService struct {
	db             *sql.DB
	settingService *SettingService
	httpClient     *http.Client

	cron      *cron.Cron
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewStatusProbeService creates a new StatusProbeService.
func NewStatusProbeService(db *sql.DB, settingService *SettingService) *StatusProbeService {
	return &StatusProbeService{
		db:             db,
		settingService: settingService,
		httpClient:     &http.Client{Timeout: 60 * time.Second},
	}
}

// ─── Config load / save ──────────────────────────────────────────────────────

// LoadConfig reads the probe configuration from the settings table.
func (s *StatusProbeService) LoadConfig(ctx context.Context) (*StatusProbeConfig, error) {
	raw, err := s.settingService.settingRepo.GetValue(ctx, SettingKeyStatusProbeConfig)
	if err != nil {
		// Not found — return defaults.
		return &StatusProbeConfig{
			Enabled:         false,
			IntervalMinutes: 5,
			RetentionDays:   30,
			Models:          nil,
		}, nil
	}
	var cfg StatusProbeConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal status probe config: %w", err)
	}
	cfg = normalizeStatusProbeConfig(cfg)
	return &cfg, nil
}

// SaveConfig writes the probe configuration to the settings table.
func (s *StatusProbeService) SaveConfig(ctx context.Context, cfg *StatusProbeConfig) error {
	normalized := normalizeStatusProbeConfig(*cfg)
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal status probe config: %w", err)
	}
	return s.settingService.settingRepo.Set(ctx, SettingKeyStatusProbeConfig, string(data))
}

// ─── Cron Start / Stop ───────────────────────────────────────────────────────

// Start begins the cron-based probe scheduler.
func (s *StatusProbeService) Start() {
	if s == nil || s.db == nil {
		return
	}
	s.startOnce.Do(func() {
		// Load config to check if enabled.
		ctx, cancel := context.WithTimeout(context.Background(), statusProbeLoadConfigTimeout)
		defer cancel()
		cfg, err := s.LoadConfig(ctx)
		if err != nil {
			slog.Warn("[StatusProbe] failed to load config on start", "error", err)
			return
		}
		if !cfg.Enabled {
			slog.Info("[StatusProbe] not started (disabled)")
			return
		}

		interval := cfg.IntervalMinutes
		if interval <= 0 {
			interval = 5
		}
		schedule := fmt.Sprintf("*/%d * * * *", interval)

		c := cron.New(cron.WithParser(statusProbeCronParser))
		_, err = c.AddFunc(schedule, func() { s.runAllProbes() })
		if err != nil {
			slog.Warn("[StatusProbe] not started (invalid schedule)", "schedule", schedule, "error", err)
			return
		}
		s.cron = c
		s.cron.Start()
		slog.Info("[StatusProbe] started", "schedule", schedule)
	})
}

// Stop gracefully shuts down the cron scheduler.
func (s *StatusProbeService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cron != nil {
			ctx := s.cron.Stop()
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
				slog.Warn("[StatusProbe] cron stop timed out")
			}
		}
	})
}

// Restart stops any existing cron and starts fresh (used after config changes).
func (s *StatusProbeService) Restart() {
	if s == nil {
		return
	}
	// Reset once guards so Start/Stop can be called again.
	s.stopOnce.Do(func() {
		if s.cron != nil {
			ctx := s.cron.Stop()
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
		}
	})
	s.startOnce = sync.Once{}
	s.stopOnce = sync.Once{}
	s.Start()
}

// ─── Probe execution ─────────────────────────────────────────────────────────

// runAllProbes iterates enabled models, probes each, and records results.
func (s *StatusProbeService) runAllProbes() {
	ctx, cancel := context.WithTimeout(context.Background(), statusProbeLoadConfigTimeout)
	defer cancel()

	cfg, err := s.LoadConfig(ctx)
	if err != nil {
		slog.Warn("[StatusProbe] failed to load config", "error", err)
		return
	}
	if !cfg.Enabled {
		return
	}

	for _, m := range cfg.Models {
		if !m.Enabled {
			continue
		}
		if m.ApiKey == "" || m.BaseURL == "" {
			slog.Warn("[StatusProbe] skipped: api_key or base_url not configured", "model", m.Model)
			continue
		}
		probeCtx, probeCancel := context.WithTimeout(context.Background(), statusProbePerModelTimeout)
		latencyMs, errMsg := s.runProbe(probeCtx, m)
		probeCancel()
		status := "ok"
		if errMsg != "" {
			status = "error"
		}
		recordCtx, recordCancel := context.WithTimeout(context.Background(), statusProbeRecordTimeout)
		if err := s.recordResult(recordCtx, m.Model, status, latencyMs, errMsg); err != nil {
			slog.Warn("[StatusProbe] failed to record result", "model", m.Model, "error", err)
		}
		recordCancel()
	}

	// Cleanup old data.
	if cfg.RetentionDays > 0 {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), statusProbeCleanupTimeout)
		if err := s.cleanup(cleanupCtx, cfg.RetentionDays); err != nil {
			slog.Warn("[StatusProbe] cleanup failed", "error", err)
		}
		cleanupCancel()
	}
}

func normalizeStatusProbeConfig(cfg StatusProbeConfig) StatusProbeConfig {
	legacyAPIKey := strings.TrimSpace(cfg.ApiKey)
	legacyBaseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")

	for i := range cfg.Models {
		if strings.TrimSpace(cfg.Models[i].ApiKey) == "" && legacyAPIKey != "" {
			cfg.Models[i].ApiKey = legacyAPIKey
		}
		if strings.TrimSpace(cfg.Models[i].BaseURL) == "" && legacyBaseURL != "" {
			cfg.Models[i].BaseURL = legacyBaseURL
		}
		cfg.Models[i].BaseURL = strings.TrimRight(strings.TrimSpace(cfg.Models[i].BaseURL), "/")
	}

	// Do not keep deprecated top-level credentials when returning/saving normalized config.
	cfg.ApiKey = ""
	cfg.BaseURL = ""
	return cfg
}

// runProbe makes an actual HTTP request to the gateway API, just like monitor.sh.
// It sends a minimal chat completion request and checks for HTTP 200.
func (s *StatusProbeService) runProbe(ctx context.Context, m StatusProbeModelConfig) (latencyMs int, errMsg string) {
	req, err := newStatusProbeRequest(ctx, m)
	if err != nil {
		return 0, err.Error()
	}

	start := time.Now()
	resp, err := s.httpClient.Do(req)
	elapsed := time.Since(start)
	latencyMs = int(elapsed.Milliseconds())

	if err != nil {
		return latencyMs, fmt.Sprintf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Drain body to allow connection reuse
		_, _ = io.Copy(io.Discard, resp.Body)
		return latencyMs, ""
	}

	// Read a bounded error body for richer diagnostics without storing huge payloads.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, statusProbeErrorBodyLimit))
	var apiErr struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
		return latencyMs, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, apiErr.Error.Message)
	}
	return latencyMs, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

func newStatusProbeRequest(ctx context.Context, m StatusProbeModelConfig) (*http.Request, error) {
	if isStatusProbeGeminiModel(m.Model) {
		return newGeminiStatusProbeRequest(ctx, m)
	}
	return newMessagesStatusProbeRequest(ctx, m)
}

func newMessagesStatusProbeRequest(ctx context.Context, m StatusProbeModelConfig) (*http.Request, error) {
	reqBody, err := json.Marshal(map[string]any{
		"model":      m.Model,
		"max_tokens": 10,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %v", err)
	}

	fullURL := strings.TrimRight(m.BaseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", m.ApiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	return req, nil
}

func newGeminiStatusProbeRequest(ctx context.Context, m StatusProbeModelConfig) (*http.Request, error) {
	reqBody, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]string{
					{"text": "hi"},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %v", err)
	}

	fullURL := fmt.Sprintf(
		"%s/v1beta/models/%s:generateContent",
		strings.TrimRight(m.BaseURL, "/"),
		url.PathEscape(m.Model),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", m.ApiKey)
	return req, nil
}

func isStatusProbeGeminiModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gemini-")
}

// ─── Record & cleanup ────────────────────────────────────────────────────────

// recordResult inserts a probe result row.
func (s *StatusProbeService) recordResult(ctx context.Context, model, status string, latencyMs int, errMsg string) error {
	var errMsgPtr *string
	if errMsg != "" {
		errMsgPtr = &errMsg
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO status_probe_results (model, status, latency_ms, error_message, created_at) VALUES ($1, $2, $3, $4, NOW())`,
		model, status, latencyMs, errMsgPtr,
	)
	return err
}

// cleanup removes probe results older than the retention period.
func (s *StatusProbeService) cleanup(ctx context.Context, retentionDays int) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM status_probe_results WHERE created_at < $1`, cutoff,
	)
	return err
}

// ─── GetStatus — public query ────────────────────────────────────────────────

// GetStatus queries the last 24 hours of probe results and builds the response.
func (s *StatusProbeService) GetStatus(ctx context.Context) (*ServiceStatusResponse, error) {
	cfg, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	intervalMin := cfg.IntervalMinutes
	if intervalMin <= 0 {
		intervalMin = 5
	}

	// If probe is disabled, return empty response.
	if !cfg.Enabled {
		return &ServiceStatusResponse{
			OverallStatus:   "unknown",
			IntervalMinutes: intervalMin,
			Models:          []ModelStatus{},
			UpdatedAt:       time.Now().UTC(),
		}, nil
	}

	// Build a map of display names and sort orders from config.
	type modelMeta struct {
		displayName string
		sortOrder   int
	}
	metaMap := make(map[string]modelMeta)
	for _, m := range cfg.Models {
		if m.Enabled {
			metaMap[m.Model] = modelMeta{displayName: m.DisplayName, sortOrder: m.SortOrder}
		}
	}

	if len(metaMap) == 0 {
		return &ServiceStatusResponse{
			OverallStatus:   "operational",
			IntervalMinutes: intervalMin,
			Models:          []ModelStatus{},
			UpdatedAt:       time.Now().UTC(),
		}, nil
	}

	// Query all results from the last 24 hours.
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, model, status, latency_ms, error_message, created_at
		 FROM status_probe_results
		 WHERE created_at >= $1
		 ORDER BY created_at DESC`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query probe results: %w", err)
	}
	defer rows.Close()

	// Group results by model.
	resultsByModel := make(map[string][]probeRawResult)
	for rows.Next() {
		var r probeRawResult
		if err := rows.Scan(&r.ID, &r.Model, &r.Status, &r.LatencyMs, &r.ErrorMessage, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan probe result: %w", err)
		}
		if _, ok := metaMap[r.Model]; ok {
			resultsByModel[r.Model] = append(resultsByModel[r.Model], r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate probe results: %w", err)
	}

	// Build ModelStatus for each configured model.
	var models []ModelStatus
	now := time.Now().UTC()
	for _, m := range cfg.Models {
		if !m.Enabled {
			continue
		}
		results := resultsByModel[m.Model]
		ms := s.buildModelStatus(m.Model, m.DisplayName, results, intervalMin, now)
		models = append(models, ms)
	}

	overallStatus := computeOverallStatus(models)

	return &ServiceStatusResponse{
		OverallStatus:   overallStatus,
		IntervalMinutes: intervalMin,
		Models:          models,
		UpdatedAt:       time.Now().UTC(),
	}, nil
}

// buildModelStatus computes current status, uptime, and interval stats from raw results.
func (s *StatusProbeService) buildModelStatus(model, displayName string, results []probeRawResult, intervalMin int, now time.Time) ModelStatus {
	ms := ModelStatus{
		Model:          model,
		DisplayName:    displayName,
		CurrentStatus:  "unknown",
		RecentFailures: []ProbeFailure{},
		HourlyStats:    []HourlyStat{},
	}

	if len(results) == 0 {
		return ms
	}

	stale := isStatusProbeStale(results[0].CreatedAt, intervalMin, now)
	if stale {
		ms.CurrentStatus = "unknown"
	}

	// Results are ordered DESC by created_at. Determine current status from last 3 probes.
	if !stale {
		recentCount := 3
		if len(results) < recentCount {
			recentCount = len(results)
		}
		failCount := 0
		for i := 0; i < recentCount; i++ {
			if results[i].Status != "ok" {
				failCount++
			}
		}
		switch {
		case failCount == 0:
			ms.CurrentStatus = "operational"
		case failCount < recentCount:
			ms.CurrentStatus = "degraded"
		default:
			ms.CurrentStatus = "outage"
		}
	}

	// Compute totals and uptime.
	totalSuccess := 0
	var totalLatency int64
	for _, r := range results {
		if r.Status == "ok" {
			totalSuccess++
		}
		totalLatency += int64(r.LatencyMs)

		// Collect recent failures (up to 10).
		if r.Status != "ok" && len(ms.RecentFailures) < 10 {
			errStr := ""
			if r.ErrorMessage.Valid {
				errStr = r.ErrorMessage.String
			}
			ms.RecentFailures = append(ms.RecentFailures, ProbeFailure{
				Timestamp:    r.CreatedAt,
				ErrorMessage: errStr,
				LatencyMs:    r.LatencyMs,
			})
		}
	}

	ms.TotalProbes = len(results)
	if ms.TotalProbes > 0 {
		ms.UptimePercent = math.Round(float64(totalSuccess)/float64(ms.TotalProbes)*10000) / 100
		ms.AvgLatencyMs = math.Round(float64(totalLatency)/float64(ms.TotalProbes)*100) / 100
	}

	// Aggregate by probe interval window.
	intervalDur := time.Duration(intervalMin) * time.Minute
	hourlyMap := make(map[time.Time]*HourlyStat)
	for _, r := range results {
		window := r.CreatedAt.UTC().Truncate(intervalDur)
		hs, ok := hourlyMap[window]
		if !ok {
			hs = &HourlyStat{Hour: window}
			hourlyMap[window] = hs
		}
		hs.TotalProbes++
		if r.Status == "ok" {
			hs.SuccessCount++
		} else {
			errStr := ""
			if r.ErrorMessage.Valid {
				errStr = r.ErrorMessage.String
			}
			hs.Failures = append(hs.Failures, ProbeFailure{
				Timestamp:    r.CreatedAt,
				ErrorMessage: errStr,
				LatencyMs:    r.LatencyMs,
			})
		}
		hs.AvgLatencyMs += float64(r.LatencyMs)
	}

	for _, hs := range hourlyMap {
		if hs.TotalProbes > 0 {
			hs.AvgLatencyMs = math.Round(hs.AvgLatencyMs/float64(hs.TotalProbes)*100) / 100
		}
		ms.HourlyStats = append(ms.HourlyStats, *hs)
	}
	sort.Slice(ms.HourlyStats, func(i, j int) bool {
		return ms.HourlyStats[i].Hour.Before(ms.HourlyStats[j].Hour)
	})

	return ms
}

func isStatusProbeStale(latest time.Time, intervalMin int, now time.Time) bool {
	if latest.IsZero() {
		return true
	}
	if intervalMin <= 0 {
		intervalMin = 5
	}
	threshold := time.Duration(intervalMin*2) * time.Minute
	return now.UTC().After(latest.UTC().Add(threshold))
}

func computeOverallStatus(models []ModelStatus) string {
	if len(models) == 0 {
		return "operational"
	}

	unknownCount := 0
	degradedCount := 0
	outageCount := 0

	for _, ms := range models {
		switch ms.CurrentStatus {
		case "outage":
			outageCount++
		case "degraded":
			degradedCount++
		case "unknown":
			unknownCount++
		}
	}

	freshCount := len(models) - unknownCount
	switch {
	case freshCount == 0:
		return "degraded"
	case outageCount == freshCount && unknownCount == 0:
		return "major_outage"
	case outageCount > 0 || degradedCount > 0 || unknownCount > 0:
		return "degraded"
	default:
		return "operational"
	}
}
