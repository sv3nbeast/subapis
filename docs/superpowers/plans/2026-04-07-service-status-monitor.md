# Service Status Monitor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-model service uptime monitoring with active probes, a public status API, and frontend status pages (standalone, dashboard embed, public homepage).

**Architecture:** Backend StatusProbeService runs on a cron schedule, sends minimal API calls per configured model through the internal gateway, stores results in a dedicated PostgreSQL table. A public API aggregates results into hourly buckets over 30 days. Frontend renders responsive availability bars with hover tooltips. Admin configures monitored models via settings.

**Tech Stack:** Go (Gin, robfig/cron, Google Wire), PostgreSQL, Vue 3 (Composition API, Tailwind CSS), TypeScript

**Spec:** `docs/superpowers/specs/2026-04-06-service-status-monitor-design.md`

---

## File Structure

### Backend — New Files
| File | Responsibility |
|------|---------------|
| `backend/migrations/091_status_probe_results.sql` | Create `status_probe_results` table |
| `backend/internal/service/status_probe_service.go` | Probe scheduler, execution, data cleanup |
| `backend/internal/handler/status_handler.go` | Public `GET /api/v1/status` endpoint (no auth) |
| `backend/internal/handler/admin/status_probe_settings_handler.go` | Admin GET/PUT for probe config |

### Backend — Modified Files
| File | Change |
|------|--------|
| `backend/internal/handler/handler.go` | Add `Status` field to `Handlers`, add `StatusProbeSettings` to `AdminHandlers` |
| `backend/internal/handler/wire.go` | Register `StatusHandler` and admin handler providers |
| `backend/internal/service/wire.go` | Register `StatusProbeService` provider |
| `backend/internal/server/routes/router.go` | Register `GET /api/v1/status` as public route |
| `backend/internal/server/routes/admin.go` | Register admin settings routes |
| `backend/cmd/server/wire.go` | Add `StatusProbeService` param to `provideCleanup` and call `.Stop()` |

### Frontend — New Files
| File | Responsibility |
|------|---------------|
| `frontend/src/api/status.ts` | API client for `GET /api/v1/status` |
| `frontend/src/components/status/ServiceStatusBar.vue` | Single model availability bar component |
| `frontend/src/components/status/ServiceStatusOverview.vue` | Container: overall status + model list |
| `frontend/src/components/admin/settings/StatusProbeSettings.vue` | Admin probe configuration panel |
| `frontend/src/views/user/StatusView.vue` | Standalone `/status` page |

### Frontend — Modified Files
| File | Change |
|------|--------|
| `frontend/src/router/index.ts` | Add `/status` route |
| `frontend/src/components/layout/AppSidebar.vue` | Add "Service Status" nav item |
| `frontend/src/views/user/DashboardView.vue` | Embed compact status overview |
| `frontend/src/views/HomeView.vue` | Add status section to public homepage |
| `frontend/src/i18n/locales/en.ts` | Add English translations |
| `frontend/src/i18n/locales/zh.ts` | Add Chinese translations |
| Admin settings page component | Add StatusProbeSettings panel |

---

## Task 1: Database Migration

**Files:**
- Create: `backend/migrations/091_status_probe_results.sql`

- [ ] **Step 1: Write migration SQL**

```sql
-- 091_status_probe_results.sql
-- Service status probe results for uptime monitoring

CREATE TABLE IF NOT EXISTS status_probe_results (
    id         BIGSERIAL PRIMARY KEY,
    model      VARCHAR(128) NOT NULL,
    status     VARCHAR(16)  NOT NULL,  -- 'success' or 'fail'
    latency_ms INTEGER      NOT NULL DEFAULT 0,
    error_message TEXT,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_status_probe_results_model_created
    ON status_probe_results (model, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_status_probe_results_created
    ON status_probe_results (created_at);
```

- [ ] **Step 2: Verify migration file is embedded**

The `backend/migrations/migrations.go` file uses `//go:embed *.sql` — any new `.sql` file in this directory is automatically included. No code change needed.

- [ ] **Step 3: Commit**

```bash
git add backend/migrations/091_status_probe_results.sql
git commit -m "feat(db): add status_probe_results table migration"
```

---

## Task 2: StatusProbeService — Core Service

**Files:**
- Create: `backend/internal/service/status_probe_service.go`
- Modify: `backend/internal/service/wire.go` — add to ProviderSet

- [ ] **Step 1: Define the StatusProbeConfig types**

In `status_probe_service.go`, define the config struct that maps to the JSON stored in settings:

```go
package service

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "log/slog"
    "sync"
    "time"

    "github.com/robfig/cron/v3"
)

const SettingKeyStatusProbeConfig = "status_probe_config"

type StatusProbeModelConfig struct {
    Model       string `json:"model"`
    DisplayName string `json:"display_name"`
    SortOrder   int    `json:"sort_order"`
    Enabled     bool   `json:"enabled"`
}

type StatusProbeConfig struct {
    Enabled         bool                     `json:"enabled"`
    IntervalMinutes int                      `json:"interval_minutes"`
    RetentionDays   int                      `json:"retention_days"`
    Models          []StatusProbeModelConfig  `json:"models"`
}

func DefaultStatusProbeConfig() *StatusProbeConfig {
    return &StatusProbeConfig{
        Enabled:         false,
        IntervalMinutes: 5,
        RetentionDays:   30,
        Models:          []StatusProbeModelConfig{},
    }
}
```

- [ ] **Step 2: Define the result types and public API response structs**

Append to `status_probe_service.go`:

```go
type ProbeFailure struct {
    Time  time.Time `json:"time"`
    Error string    `json:"error"`
}

type HourlyStat struct {
    Hour       time.Time      `json:"hour"`
    Success    int            `json:"success"`
    Total      int            `json:"total"`
    AvgLatency int            `json:"avg_latency_ms"`
    Failures   []ProbeFailure `json:"failures,omitempty"`
}

type ModelStatus struct {
    Model            string       `json:"model"`
    DisplayName      string       `json:"display_name"`
    CurrentStatus    string       `json:"current_status"`    // operational, degraded, outage
    UptimePercentage float64      `json:"uptime_percentage"`
    HourlyStats      []HourlyStat `json:"hourly_stats"`
}

type ServiceStatusResponse struct {
    OverallStatus string        `json:"overall_status"` // operational, degraded, major_outage
    Models        []ModelStatus `json:"models"`
    LastUpdated   time.Time     `json:"last_updated"`
}
```

- [ ] **Step 3: Implement the service struct and constructor**

```go
type StatusProbeService struct {
    db             *sql.DB
    settingService *SettingService
    cron           *cron.Cron
    startOnce      sync.Once
    stopOnce       sync.Once
}

func NewStatusProbeService(db *sql.DB, settingService *SettingService) *StatusProbeService {
    return &StatusProbeService{
        db:             db,
        settingService: settingService,
    }
}
```

- [ ] **Step 4: Implement config load/save via SettingService**

```go
func (s *StatusProbeService) GetConfig(ctx context.Context) (*StatusProbeConfig, error) {
    value, err := s.settingService.settingRepo.GetValue(ctx, SettingKeyStatusProbeConfig)
    if err != nil {
        return DefaultStatusProbeConfig(), nil
    }
    var cfg StatusProbeConfig
    if err := json.Unmarshal([]byte(value), &cfg); err != nil {
        return DefaultStatusProbeConfig(), nil
    }
    return &cfg, nil
}

func (s *StatusProbeService) SaveConfig(ctx context.Context, cfg *StatusProbeConfig) error {
    data, err := json.Marshal(cfg)
    if err != nil {
        return fmt.Errorf("marshal status probe config: %w", err)
    }
    return s.settingService.settingRepo.Set(ctx, SettingKeyStatusProbeConfig, string(data))
}
```

- [ ] **Step 5: Implement probe execution**

```go
func (s *StatusProbeService) runProbe(ctx context.Context, model string) (latencyMs int, errMsg string) {
    start := time.Now()

    // Make a minimal API call through the internal gateway
    // Use a simple HTTP request to localhost with max_tokens=1
    // This will be refined when wiring with the actual gateway
    reqBody := fmt.Sprintf(`{"model":"%s","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`, model)

    // TODO: Wire with internal gateway client in Task 3
    _ = reqBody

    elapsed := time.Since(start).Milliseconds()
    return int(elapsed), ""
}

func (s *StatusProbeService) recordResult(ctx context.Context, model, status string, latencyMs int, errorMsg string) {
    _, err := s.db.ExecContext(ctx,
        `INSERT INTO status_probe_results (model, status, latency_ms, error_message, created_at) VALUES ($1, $2, $3, $4, NOW())`,
        model, status, latencyMs, errorMsg,
    )
    if err != nil {
        slog.Warn("status_probe_record_failed", "model", model, "error", err)
    }
}

func (s *StatusProbeService) runAllProbes() {
    ctx := context.Background()
    cfg, err := s.GetConfig(ctx)
    if err != nil || !cfg.Enabled {
        return
    }

    for _, m := range cfg.Models {
        if !m.Enabled {
            continue
        }
        latency, errMsg := s.runProbe(ctx, m.Model)
        status := "success"
        if errMsg != "" {
            status = "fail"
        }
        s.recordResult(ctx, m.Model, status, latency, errMsg)
    }
}
```

- [ ] **Step 6: Implement cron start/stop and cleanup**

```go
var statusProbeCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func (s *StatusProbeService) Start() {
    s.startOnce.Do(func() {
        cfg, err := s.GetConfig(context.Background())
        if err != nil || !cfg.Enabled {
            slog.Info("status_probe_disabled")
            return
        }

        interval := cfg.IntervalMinutes
        if interval <= 0 {
            interval = 5
        }
        schedule := fmt.Sprintf("*/%d * * * *", interval)

        c := cron.New(cron.WithParser(statusProbeCronParser))
        if _, err := c.AddFunc(schedule, s.runAllProbes); err != nil {
            slog.Error("status_probe_cron_add_failed", "error", err)
            return
        }

        // Also add daily cleanup at 3am
        if _, err := c.AddFunc("0 3 * * *", s.cleanup); err != nil {
            slog.Warn("status_probe_cleanup_cron_failed", "error", err)
        }

        s.cron = c
        s.cron.Start()
        slog.Info("status_probe_started", "interval_minutes", interval)
    })
}

func (s *StatusProbeService) Stop() {
    s.stopOnce.Do(func() {
        if s.cron != nil {
            ctx := s.cron.Stop()
            select {
            case <-ctx.Done():
            case <-time.After(3 * time.Second):
            }
        }
    })
}

func (s *StatusProbeService) cleanup() {
    cfg, _ := s.GetConfig(context.Background())
    days := 30
    if cfg != nil && cfg.RetentionDays > 0 {
        days = cfg.RetentionDays
    }
    cutoff := time.Now().AddDate(0, 0, -days)
    result, err := s.db.ExecContext(context.Background(),
        `DELETE FROM status_probe_results WHERE created_at < $1`, cutoff)
    if err != nil {
        slog.Warn("status_probe_cleanup_failed", "error", err)
        return
    }
    rows, _ := result.RowsAffected()
    slog.Info("status_probe_cleanup_done", "deleted", rows)
}
```

- [ ] **Step 7: Implement GetStatus query**

```go
func (s *StatusProbeService) GetStatus(ctx context.Context) (*ServiceStatusResponse, error) {
    cfg, err := s.GetConfig(ctx)
    if err != nil {
        return nil, err
    }
    if !cfg.Enabled || len(cfg.Models) == 0 {
        return nil, nil
    }

    cutoff := time.Now().AddDate(0, 0, -30)
    rows, err := s.db.QueryContext(ctx,
        `SELECT model, status, latency_ms, error_message, created_at
         FROM status_probe_results
         WHERE created_at >= $1
         ORDER BY model, created_at`, cutoff)
    if err != nil {
        return nil, fmt.Errorf("query status probes: %w", err)
    }
    defer rows.Close()

    // Group raw results by model
    modelResults := make(map[string][]probeRawResult)
    for rows.Next() {
        var r probeRawResult
        var model string
        var errMsg sql.NullString
        if err := rows.Scan(&model, &r.Status, &r.Latency, &errMsg, &r.CreatedAt); err != nil {
            continue
        }
        r.Error = errMsg.String
        modelResults[model] = append(modelResults[model], r)
    }

    // Build model config lookup
    configMap := make(map[string]StatusProbeModelConfig)
    for _, m := range cfg.Models {
        if m.Enabled {
            configMap[m.Model] = m
        }
    }

    resp := &ServiceStatusResponse{LastUpdated: time.Now()}
    hasOutage := false
    hasDegraded := false

    for _, mc := range cfg.Models {
        if !mc.Enabled {
            continue
        }
        results := modelResults[mc.Model]
        ms := buildModelStatus(mc, results)
        resp.Models = append(resp.Models, ms)

        switch ms.CurrentStatus {
        case "outage":
            hasOutage = true
        case "degraded":
            hasDegraded = true
        }
    }

    if hasOutage {
        resp.OverallStatus = "major_outage"
    } else if hasDegraded {
        resp.OverallStatus = "degraded"
    } else {
        resp.OverallStatus = "operational"
    }

    return resp, nil
}

func buildModelStatus(mc StatusProbeModelConfig, results []probeRawResult) ModelStatus {
    ms := ModelStatus{
        Model:       mc.Model,
        DisplayName: mc.DisplayName,
    }

    if len(results) == 0 {
        ms.CurrentStatus = "operational"
        ms.UptimePercentage = 100
        return ms
    }

    // Current status: last 3 probes
    lastN := results
    if len(lastN) > 3 {
        lastN = lastN[len(lastN)-3:]
    }
    failCount := 0
    for _, r := range lastN {
        if r.Status == "fail" {
            failCount++
        }
    }
    switch {
    case failCount == len(lastN):
        ms.CurrentStatus = "outage"
    case failCount > 0:
        ms.CurrentStatus = "degraded"
    default:
        ms.CurrentStatus = "operational"
    }

    // Uptime percentage
    totalSuccess := 0
    for _, r := range results {
        if r.Status == "success" {
            totalSuccess++
        }
    }
    ms.UptimePercentage = float64(totalSuccess) / float64(len(results)) * 100
    ms.UptimePercentage = math.Round(ms.UptimePercentage*100) / 100

    // Hourly aggregation
    hourlyMap := make(map[string]*HourlyStat)
    for _, r := range results {
        hourKey := r.CreatedAt.Truncate(time.Hour).Format(time.RFC3339)
        h, ok := hourlyMap[hourKey]
        if !ok {
            h = &HourlyStat{Hour: r.CreatedAt.Truncate(time.Hour)}
            hourlyMap[hourKey] = h
        }
        h.Total++
        if r.Status == "success" {
            h.Success++
        } else {
            h.Failures = append(h.Failures, ProbeFailure{Time: r.CreatedAt, Error: r.Error})
        }
        h.AvgLatency = ((h.AvgLatency * (h.Total - 1)) + r.Latency) / h.Total
    }

    // Sort hourly stats by time
    for _, h := range hourlyMap {
        ms.HourlyStats = append(ms.HourlyStats, *h)
    }
    sort.Slice(ms.HourlyStats, func(i, j int) bool {
        return ms.HourlyStats[i].Hour.Before(ms.HourlyStats[j].Hour)
    })

    return ms
}
```

Note: Add `"math"` and `"sort"` to imports. Also add this package-level type before `GetStatus`:

```go
type probeRawResult struct {
    Status    string
    Latency   int
    Error     string
    CreatedAt time.Time
}
```

- [ ] **Step 8: Register in wire.go**

In `backend/internal/service/wire.go`, add to `ProviderSet`:
```go
NewStatusProbeService,
```

- [ ] **Step 9: Commit**

```bash
git add backend/internal/service/status_probe_service.go backend/internal/service/wire.go
git commit -m "feat: add StatusProbeService with cron probes and status query"
```

---

## Task 3: Probe Execution via Internal HTTP

**Files:**
- Modify: `backend/internal/service/status_probe_service.go` — implement real `runProbe`

- [ ] **Step 1: Implement probe via localhost HTTP call**

Replace the placeholder `runProbe` with a real HTTP client that calls the local gateway:

```go
import (
    "bytes"
    "io"
    "net/http"
)

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
        return 0, fmt.Sprintf("create request: %v", err)
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("anthropic-version", "2023-06-01")

    // Use admin API key for auth
    adminKey, _ := s.settingService.settingRepo.GetValue(ctx, "admin_api_key")
    if adminKey != "" {
        req.Header.Set("x-api-key", adminKey)
    }

    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(req)
    elapsed := int(time.Since(start).Milliseconds())
    if err != nil {
        return elapsed, fmt.Sprintf("request failed: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
        return elapsed, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
    }

    // Drain body to ensure connection reuse
    io.Copy(io.Discard, resp.Body)
    return elapsed, ""
}
```

- [ ] **Step 2: Commit**

```bash
git add backend/internal/service/status_probe_service.go
git commit -m "feat: implement probe execution via internal HTTP gateway"
```

---

## Task 4: Public Status API Handler

**Files:**
- Create: `backend/internal/handler/status_handler.go`
- Modify: `backend/internal/handler/handler.go` — add `Status` field
- Modify: `backend/internal/handler/wire.go` — register provider
- Modify: `backend/internal/server/routes/router.go` — register route

- [ ] **Step 1: Create StatusHandler**

```go
package handler

import (
    "net/http"

    "github.com/Wei-Shaw/sub2api/internal/service"
    "github.com/gin-gonic/gin"
)

type StatusHandler struct {
    statusProbeService *service.StatusProbeService
}

func NewStatusHandler(statusProbeService *service.StatusProbeService) *StatusHandler {
    return &StatusHandler{statusProbeService: statusProbeService}
}

func (h *StatusHandler) GetStatus(c *gin.Context) {
    resp, err := h.statusProbeService.GetStatus(c.Request.Context())
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    if resp == nil {
        c.JSON(http.StatusOK, gin.H{"overall_status": "unknown", "models": []any{}, "last_updated": nil})
        return
    }
    c.Header("Cache-Control", "public, max-age=60")
    c.JSON(http.StatusOK, resp)
}
```

- [ ] **Step 2: Add Status field to Handlers struct**

In `handler.go`, add to `Handlers` struct:
```go
Status        *StatusHandler
```

- [ ] **Step 3: Register in wire.go**

In `backend/internal/handler/wire.go`, add `NewStatusHandler` to `ProviderSet` and add the `Status` field to the `ProvideHandlers` function.

- [ ] **Step 4: Register public route**

In `backend/internal/server/routes/router.go`, find where the `v1` group is created in `registerRoutes`. Add the status endpoint **before** any auth middleware is applied, directly on the `v1` group (same pattern as `/settings/public` in `auth.go`):

```go
// Public status endpoint (no auth required)
v1.GET("/status", h.Status.GetStatus)
```

Place this line right after `v1 := r.Group("/api/v1")` and before the auth route registrations.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/handler/status_handler.go backend/internal/handler/handler.go \
       backend/internal/handler/wire.go backend/internal/server/routes/
git commit -m "feat: add public GET /api/v1/status endpoint"
```

---

## Task 5: Admin Settings Handler

**Files:**
- Create: `backend/internal/handler/admin/status_probe_settings_handler.go`
- Modify: `backend/internal/handler/handler.go` — add to `AdminHandlers`
- Modify: `backend/internal/handler/wire.go` — register provider
- Modify: `backend/internal/server/routes/admin.go` — register routes

- [ ] **Step 1: Create admin handler**

```go
package admin

import (
    "net/http"

    "github.com/Wei-Shaw/sub2api/internal/service"
    "github.com/gin-gonic/gin"
)

type StatusProbeSettingsHandler struct {
    statusProbeService *service.StatusProbeService
}

func NewStatusProbeSettingsHandler(statusProbeService *service.StatusProbeService) *StatusProbeSettingsHandler {
    return &StatusProbeSettingsHandler{statusProbeService: statusProbeService}
}

func (h *StatusProbeSettingsHandler) GetConfig(c *gin.Context) {
    cfg, err := h.statusProbeService.GetConfig(c.Request.Context())
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"data": cfg})
}

func (h *StatusProbeSettingsHandler) UpdateConfig(c *gin.Context) {
    var cfg service.StatusProbeConfig
    if err := c.ShouldBindJSON(&cfg); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config: " + err.Error()})
        return
    }
    if cfg.IntervalMinutes <= 0 {
        cfg.IntervalMinutes = 5
    }
    if cfg.RetentionDays <= 0 {
        cfg.RetentionDays = 30
    }
    if err := h.statusProbeService.SaveConfig(c.Request.Context(), &cfg); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"data": cfg, "message": "ok"})
}
```

- [ ] **Step 2: Add to AdminHandlers struct and wire**

In `handler.go`, add to `AdminHandlers`:
```go
StatusProbeSettings *admin.StatusProbeSettingsHandler
```

In `handler/wire.go`, add `admin.NewStatusProbeSettingsHandler` to `ProviderSet`, and add the field to `ProvideAdminHandlers`.

- [ ] **Step 3: Register admin routes**

In `admin.go`, add a new registration function and call it from `RegisterAdminRoutes`:

```go
func registerStatusProbeSettingsRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
    group := admin.Group("/settings/status-probe")
    {
        group.GET("", h.Admin.StatusProbeSettings.GetConfig)
        group.PUT("", h.Admin.StatusProbeSettings.UpdateConfig)
    }
}
```

Call `registerStatusProbeSettingsRoutes(admin, h)` inside `RegisterAdminRoutes`.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/handler/admin/status_probe_settings_handler.go \
       backend/internal/handler/handler.go backend/internal/handler/wire.go \
       backend/internal/server/routes/admin.go
git commit -m "feat: add admin API for status probe configuration"
```

---

## Task 6: Wire Generation and Cleanup Registration

**Files:**
- Modify: `backend/cmd/server/wire.go` — add cleanup
- Regenerate: `backend/cmd/server/wire_gen.go`

- [ ] **Step 1: Add StatusProbeService to cleanup**

In `backend/cmd/server/wire.go`, find the `provideCleanup` function:
1. Add `statusProbeService *service.StatusProbeService` as a new **parameter** to the function signature
2. Add `statusProbeService.Stop()` inside the cleanup function body

- [ ] **Step 2: Regenerate wire**

```bash
cd backend && go generate ./cmd/server/...
```

- [ ] **Step 3: Verify build**

```bash
cd backend && go build ./cmd/server/...
```

- [ ] **Step 4: Commit**

```bash
git add backend/cmd/server/wire.go backend/cmd/server/wire_gen.go
git commit -m "feat: wire StatusProbeService with DI and graceful shutdown"
```

---

## Task 7: Frontend — API Client and Types

**Files:**
- Create: `frontend/src/api/status.ts`

- [ ] **Step 1: Create status API module**

```ts
import { apiClient } from './client'

// === Types ===

export interface ProbeFailure {
  time: string
  error: string
}

export interface HourlyStat {
  hour: string
  success: number
  total: number
  avg_latency_ms: number
  failures?: ProbeFailure[]
}

export interface ModelStatus {
  model: string
  display_name: string
  current_status: 'operational' | 'degraded' | 'outage'
  uptime_percentage: number
  hourly_stats: HourlyStat[]
}

export interface ServiceStatusResponse {
  overall_status: 'operational' | 'degraded' | 'major_outage' | 'unknown'
  models: ModelStatus[]
  last_updated: string | null
}

// === API ===

async function getStatus(): Promise<ServiceStatusResponse> {
  const { data } = await apiClient.get<ServiceStatusResponse>('/status')
  return data
}

export const statusAPI = { getStatus }
export default statusAPI
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/api/status.ts
git commit -m "feat: add status API client"
```

---

## Task 8: Frontend — ServiceStatusBar Component

**Files:**
- Create: `frontend/src/components/status/ServiceStatusBar.vue`

- [ ] **Step 1: Create the component**

This is the core component rendering a single model's availability bar. Key behaviors:
- Props: `modelStatus: ModelStatus`, `compact: boolean` (default false)
- Renders: status dot + model name + uptime percentage + 30-day hourly bar
- Each bar segment is a `<div>` with `flex:1`, colored green/yellow/red based on failure ratio
- Hover tooltip shows: time range, probe counts, failure details with exact timestamps
- Duration display: single failure = `< 5 分钟`, consecutive failures = exact range
- Fully responsive via flexbox

Implementation: Create as a standard `<script setup lang="ts">` component with Tailwind classes. Generate 720 slots (30 days × 24 hours), fill with data from `hourly_stats`, gaps = green (no data = operational). Tooltip uses absolute-positioned div on hover.

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/status/ServiceStatusBar.vue
git commit -m "feat: add ServiceStatusBar component with hover tooltips"
```

---

## Task 9: Frontend — ServiceStatusOverview Component

**Files:**
- Create: `frontend/src/components/status/ServiceStatusOverview.vue`

- [ ] **Step 1: Create the container component**

Props: `compact: boolean` (default false)
- Fetches data from `statusAPI.getStatus()` on mount
- Shows overall status indicator (dot + text)
- Renders `ServiceStatusBar` for each model
- In compact mode: no date labels, clickable → navigates to `/status`
- Loading state: skeleton placeholder
- No data / probe disabled: hidden (renders nothing)

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/status/ServiceStatusOverview.vue
git commit -m "feat: add ServiceStatusOverview container component"
```

---

## Task 10: Frontend — Standalone Status Page and Route

**Files:**
- Create: `frontend/src/views/user/StatusView.vue`
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`

- [ ] **Step 1: Create StatusView**

Simple page wrapping `ServiceStatusOverview` in `AppLayout`:

```vue
<template>
  <AppLayout>
    <ServiceStatusOverview />
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import ServiceStatusOverview from '@/components/status/ServiceStatusOverview.vue'
</script>
```

- [ ] **Step 2: Add route**

In `router/index.ts`, add after the `/profile` route in the user section:
```ts
{
  path: '/status',
  name: 'ServiceStatus',
  component: () => import('@/views/user/StatusView.vue'),
  meta: {
    requiresAuth: true,
    requiresAdmin: false,
    title: 'Service Status',
    titleKey: 'status.title',
    descriptionKey: 'status.description'
  }
}
```

- [ ] **Step 3: Add sidebar nav item**

In `AppSidebar.vue`, add a `StatusIcon` render object (use a signal/pulse SVG icon), then add to `userNavItems` array before the profile item:
```ts
{ path: '/status', label: t('nav.serviceStatus'), icon: StatusIcon },
```
Do NOT set `hideInSimpleMode: true` — show in all modes.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/views/user/StatusView.vue frontend/src/router/index.ts \
       frontend/src/components/layout/AppSidebar.vue
git commit -m "feat: add standalone /status page with sidebar navigation"
```

---

## Task 11: Frontend — Dashboard and Homepage Integration

**Files:**
- Modify: `frontend/src/views/user/DashboardView.vue`
- Modify: `frontend/src/views/HomeView.vue`

- [ ] **Step 1: Embed in Dashboard**

In `DashboardView.vue`, add `ServiceStatusOverview` with `compact` prop between `UserDashboardStats` and `UserDashboardCharts`:

```html
<ServiceStatusOverview compact />
```

Import the component in the script section.

- [ ] **Step 2: Add to HomeView**

In `HomeView.vue`, add a status section inside `<main>` after the features grid (around line 283). Wrap in the same `mx-auto max-w-6xl` container used by other sections:

```html
<section class="mt-16 px-6">
  <div class="mx-auto max-w-6xl">
    <ServiceStatusOverview />
  </div>
</section>
```

Import the component. Since this is a public page with no auth, the API itself is public so it works for unauthenticated users.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/views/user/DashboardView.vue frontend/src/views/HomeView.vue
git commit -m "feat: embed status overview in dashboard and homepage"
```

---

## Task 12: i18n Translations

**Files:**
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/i18n/locales/zh.ts`

- [ ] **Step 1: Add English translations**

Add to `en.ts`:
```ts
status: {
  title: 'Service Status',
  description: 'Real-time availability of all services',
  allOperational: 'All services are operational',
  degraded: 'Some services are affected',
  majorOutage: 'Service outage',
  operational: 'Operational',
  degradedStatus: 'Degraded',
  outage: 'Outage',
  uptime: 'uptime',
  daysAgo: '30 days ago',
  today: 'Today',
  probes: 'probes',
  success: 'successful',
  failed: 'Failed',
  avgLatency: 'Avg latency',
  duration: 'Duration',
  lessThan: 'Less than',
  minutes: 'minutes',
  collecting: 'Collecting monitoring data...',
  lastUpdated: 'Last updated',
},
nav: {
  // ... existing keys ...
  serviceStatus: 'Service Status',
}
```

- [ ] **Step 2: Add Chinese translations**

Add to `zh.ts`:
```ts
status: {
  title: '服务状态',
  description: '所有服务的实时可用性',
  allOperational: '所有服务运行正常',
  degraded: '部分服务受影响',
  majorOutage: '服务异常',
  operational: '正常运行',
  degradedStatus: '部分受影响',
  outage: '服务异常',
  uptime: '可用',
  daysAgo: '30 天前',
  today: '今天',
  probes: '次探测',
  success: '次成功',
  failed: '失败',
  avgLatency: '平均延迟',
  duration: '持续时间',
  lessThan: '小于',
  minutes: '分钟',
  collecting: '监控数据收集中...',
  lastUpdated: '最近更新',
},
nav: {
  // ... existing keys ...
  serviceStatus: '服务状态',
}
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/i18n/locales/en.ts frontend/src/i18n/locales/zh.ts
git commit -m "feat: add i18n translations for service status"
```

---

## Task 13: Frontend — Admin Probe Configuration Panel

**Files:**
- Create: `frontend/src/components/admin/settings/StatusProbeSettings.vue`
- Modify: Admin settings page to include this component

- [ ] **Step 1: Create admin config component**

Component with `<script setup lang="ts">`:
- On mount, fetch config via `GET /api/v1/admin/settings/status-probe`
- Enable/disable toggle (boolean)
- Interval dropdown: options `[1, 2, 3, 5, 10, 15]` minutes
- Retention days input: number, default 30
- Model list: editable table with columns:
  - Model ID (text input, e.g. `claude-opus-4-6`)
  - Display Name (text input, e.g. `Claude Opus 4`)
  - Sort Order (number input)
  - Enabled (toggle)
  - Delete button
- "Add Model" button at bottom of table
- Save button: `PUT /api/v1/admin/settings/status-probe` with the full config JSON
- Follow existing admin settings component patterns (card layout, Tailwind classes)

- [ ] **Step 2: Integrate into admin settings page**

Find the existing admin settings page/view and add the `StatusProbeSettings` component as a new section/card. Follow the existing pattern for how other settings cards (stream timeout, rectifier, overload cooldown) are included.

- [ ] **Step 3: Add i18n keys for admin panel**

Add to `en.ts` and `zh.ts` under an `adminStatus` section:
```ts
adminStatus: {
  title: 'Service Status Probe',          // '服务状态探针'
  enabled: 'Enable Probe',                // '启用探针'
  interval: 'Probe Interval (minutes)',   // '探测间隔（分钟）'
  retention: 'Data Retention (days)',      // '数据保留天数'
  models: 'Monitored Models',             // '监控模型列表'
  modelId: 'Model ID',                    // '模型 ID'
  displayName: 'Display Name',            // '显示名称'
  sortOrder: 'Sort Order',                // '排序'
  addModel: 'Add Model',                  // '添加模型'
  save: 'Save',                           // '保存'
  saved: 'Configuration saved',           // '配置已保存'
}
```

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/admin/settings/StatusProbeSettings.vue \
       frontend/src/i18n/locales/en.ts frontend/src/i18n/locales/zh.ts
git commit -m "feat: add admin UI for status probe configuration"
```

---

## Task 14: Build Verification

- [ ] **Step 1: Backend build**

```bash
cd backend && go build ./...
```

- [ ] **Step 2: Frontend build**

```bash
cd frontend && npm run build
```

- [ ] **Step 3: Fix any issues found, commit**
