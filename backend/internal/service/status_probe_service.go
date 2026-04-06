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
	"sort"
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
}

// StatusProbeConfig is the JSON-serialised configuration stored in the settings table.
type StatusProbeConfig struct {
	Enabled         bool                     `json:"enabled"`
	IntervalMinutes int                      `json:"interval_minutes"`
	RetentionDays   int                      `json:"retention_days"`
	Models          []StatusProbeModelConfig  `json:"models"`
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
	Model           string        `json:"model"`
	DisplayName     string        `json:"display_name"`
	CurrentStatus   string        `json:"current_status"`
	UptimePercent   float64       `json:"uptime_percentage"`
	AvgLatencyMs    float64       `json:"avg_latency_ms"`
	TotalProbes     int           `json:"total_probes"`
	RecentFailures  []ProbeFailure `json:"recent_failures"`
	HourlyStats     []HourlyStat  `json:"hourly_stats"`
}

// ServiceStatusResponse is the top-level response returned by GetStatus.
type ServiceStatusResponse struct {
	OverallStatus string        `json:"overall_status"`
	Models        []ModelStatus `json:"models"`
	UpdatedAt     time.Time     `json:"last_updated"`
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

// ─── Service struct ──────────────────────────────────────────────────────────

// StatusProbeService periodically probes monitored models via the local
// /v1/messages endpoint and records the results for uptime monitoring.
type StatusProbeService struct {
	db             *sql.DB
	settingService *SettingService

	cron      *cron.Cron
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewStatusProbeService creates a new StatusProbeService.
func NewStatusProbeService(db *sql.DB, settingService *SettingService) *StatusProbeService {
	return &StatusProbeService{
		db:             db,
		settingService: settingService,
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
	return &cfg, nil
}

// SaveConfig writes the probe configuration to the settings table.
func (s *StatusProbeService) SaveConfig(ctx context.Context, cfg *StatusProbeConfig) error {
	data, err := json.Marshal(cfg)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
		latencyMs, errMsg := s.runProbe(ctx, m.Model)
		status := "ok"
		if errMsg != "" {
			status = "error"
		}
		if err := s.recordResult(ctx, m.Model, status, latencyMs, errMsg); err != nil {
			slog.Warn("[StatusProbe] failed to record result", "model", m.Model, "error", err)
		}
	}

	// Cleanup old data.
	if cfg.RetentionDays > 0 {
		if err := s.cleanup(ctx, cfg.RetentionDays); err != nil {
			slog.Warn("[StatusProbe] cleanup failed", "error", err)
		}
	}
}

// runProbe sends a minimal /v1/messages request to the local server and measures latency.
func (s *StatusProbeService) runProbe(ctx context.Context, model string) (latencyMs int, errMsg string) {
	start := time.Now()

	port := 8080
	if s.settingService != nil && s.settingService.cfg != nil && s.settingService.cfg.Server.Port != 0 {
		port = s.settingService.cfg.Server.Port
	}

	reqBody := []byte(fmt.Sprintf(`{"model":"%s","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`, model))

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("http://127.0.0.1:%d/v1/messages", port), bytes.NewReader(reqBody))
	if err != nil {
		return int(time.Since(start).Milliseconds()), fmt.Sprintf("create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	// Use admin API key for authentication.
	adminKey, keyErr := s.settingService.GetAdminAPIKey(ctx)
	if keyErr != nil {
		return int(time.Since(start).Milliseconds()), fmt.Sprintf("get admin api key: %v", keyErr)
	}
	if adminKey == "" {
		return int(time.Since(start).Milliseconds()), "admin api key not configured"
	}
	req.Header.Set("x-api-key", adminKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	latencyMs = int(time.Since(start).Milliseconds())
	if err != nil {
		return latencyMs, fmt.Sprintf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Read body (limited to 4KB to avoid memory issues).
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 500 {
		return latencyMs, fmt.Sprintf("server error %d: %s", resp.StatusCode, truncateString(string(body), 256))
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return latencyMs, fmt.Sprintf("auth error %d: %s", resp.StatusCode, truncateString(string(body), 256))
	}
	// 2xx and 4xx (e.g. 400 from max_tokens=1) are considered successful probes —
	// they prove the upstream model is reachable and the gateway is functioning.
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// 4xx other than auth errors means the request reached the model layer.
		// This is a successful probe — the model is reachable.
		return latencyMs, ""
	}

	return latencyMs, ""
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

// GetStatus queries the last 30 days of probe results and builds the response.
func (s *StatusProbeService) GetStatus(ctx context.Context) (*ServiceStatusResponse, error) {
	cfg, err := s.LoadConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
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
			OverallStatus: "operational",
			Models:        []ModelStatus{},
			UpdatedAt:     time.Now().UTC(),
		}, nil
	}

	// Query all results from the last 30 days.
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
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
	for _, m := range cfg.Models {
		if !m.Enabled {
			continue
		}
		results := resultsByModel[m.Model]
		ms := s.buildModelStatus(m.Model, m.DisplayName, results)
		models = append(models, ms)
	}

	// Compute overall status from all model statuses.
	overallStatus := "operational"
	for _, ms := range models {
		if ms.CurrentStatus == "down" {
			overallStatus = "major_outage"
			break
		} else if ms.CurrentStatus == "degraded" {
			overallStatus = "degraded"
		}
	}

	return &ServiceStatusResponse{
		OverallStatus: overallStatus,
		Models:        models,
		UpdatedAt:     time.Now().UTC(),
	}, nil
}

// buildModelStatus computes current status, uptime, and hourly stats from raw results.
func (s *StatusProbeService) buildModelStatus(model, displayName string, results []probeRawResult) ModelStatus {
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

	// Results are ordered DESC by created_at. Determine current status from last 3 probes.
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
		ms.CurrentStatus = "down"
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

	// Aggregate by hour.
	hourlyMap := make(map[time.Time]*HourlyStat)
	for _, r := range results {
		hour := r.CreatedAt.UTC().Truncate(time.Hour)
		hs, ok := hourlyMap[hour]
		if !ok {
			hs = &HourlyStat{Hour: hour}
			hourlyMap[hour] = hs
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
