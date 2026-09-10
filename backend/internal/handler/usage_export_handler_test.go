package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// usageExportRepoStub captures the filters both endpoints hand to the repository and
// feeds the export either fixed rows or a generator (for large / blocking streams).
type usageExportRepoStub struct {
	service.UsageLogRepository

	mu            sync.Mutex
	listFilters   usagestats.UsageLogFilters
	streamFilters usagestats.UsageLogFilters
	streamOpts    usagestats.UsageLogStreamOptions
	streamCalls   int
	batchesSent   int

	rows      []service.UsageLog
	streamErr error
	generate  func(ctx context.Context, opts usagestats.UsageLogStreamOptions, emit func([]service.UsageLog) error) error
}

func (s *usageExportRepoStub) ListWithFilters(_ context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listFilters = filters
	return nil, &pagination.PaginationResult{Page: params.Page, PageSize: params.PageSize}, nil
}

func (s *usageExportRepoStub) StreamWithFilters(ctx context.Context, filters usagestats.UsageLogFilters, opts usagestats.UsageLogStreamOptions, fn func([]service.UsageLog) error) error {
	s.mu.Lock()
	s.streamFilters = filters
	s.streamOpts = opts
	s.streamCalls++
	s.mu.Unlock()
	if s.streamErr != nil {
		return s.streamErr
	}
	emit := func(batch []service.UsageLog) error {
		s.mu.Lock()
		s.batchesSent++
		s.mu.Unlock()
		return fn(batch)
	}
	if s.generate != nil {
		return s.generate(ctx, opts, emit)
	}
	size := opts.BatchSize
	if size <= 0 {
		size = usageExportBatchSize
	}
	for start := 0; start < len(s.rows); start += size {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + size
		if end > len(s.rows) {
			end = len(s.rows)
		}
		if err := emit(s.rows[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *usageExportRepoStub) snapshot() (calls, batches int, filters usagestats.UsageLogFilters, opts usagestats.UsageLogStreamOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamCalls, s.batchesSent, s.streamFilters, s.streamOpts
}

// usageExportAPIKeyRepoStub answers GetByID with a key owned by ownerID.
type usageExportAPIKeyRepoStub struct {
	service.APIKeyRepository
	ownerID int64
}

func (s *usageExportAPIKeyRepoStub) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	return &service.APIKey{ID: id, UserID: s.ownerID, Name: "demo-key"}, nil
}

// flushRecorder counts response flushes so tests can prove batches reach the client
// incrementally, and lets a test simulate a client disconnect after N flushes.
type flushRecorder struct {
	*httptest.ResponseRecorder
	mu      sync.Mutex
	flushes int
	onFlush func(count int)
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *flushRecorder) Flush() {
	r.ResponseRecorder.Flush()
	r.mu.Lock()
	r.flushes++
	count := r.flushes
	cb := r.onFlush
	r.mu.Unlock()
	if cb != nil {
		cb(count)
	}
}

func (r *flushRecorder) flushCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushes
}

func newUsageExportTestRouter(repo service.UsageLogRepository, apiKeySvc *service.APIKeyService, subject *middleware2.AuthSubject) *gin.Engine {
	gin.SetMode(gin.TestMode)
	usageSvc := service.NewUsageService(repo, nil, nil, nil)
	h := NewUsageHandler(usageSvc, apiKeySvc, nil, nil)
	router := gin.New()
	if subject != nil {
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware2.ContextKeyUser), *subject)
			c.Next()
		})
	}
	router.GET("/usage", h.List)
	router.GET("/usage/export", h.Export)
	return router
}

func exportSubject() *middleware2.AuthSubject {
	return &middleware2.AuthSubject{UserID: 42}
}

func exportSampleLog(i int) service.UsageLog {
	effort := "xhigh"
	endpoint := "/v1/chat/completions"
	ip := "203.0.113.10"
	firstToken := 12
	duration := 345
	mode := string(service.BillingModeToken)
	return service.UsageLog{
		ID:                       int64(i + 1),
		UserID:                   42,
		APIKeyID:                 7,
		RequestID:                fmt.Sprintf("req-%d", i),
		Model:                    "gpt-5.4",
		RequestedModel:           "gpt-5.4",
		RequestedReasoningEffort: &effort,
		InboundEndpoint:          &endpoint,
		IPAddress:                &ip,
		InputTokens:              4057,
		OutputTokens:             101,
		CacheReadTokens:          278272,
		CacheCreationTokens:      4,
		RateMultiplier:           1,
		TotalCost:                0.092883,
		ActualCost:               0.092883,
		FirstTokenMs:             &firstToken,
		DurationMs:               &duration,
		BillingMode:              &mode,
		RequestType:              service.RequestTypeSync,
		CreatedAt:                time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC),
		APIKey:                   &service.APIKey{ID: 7, Name: "demo-key"},
	}
}

func exportSampleRows(n int) []service.UsageLog {
	rows := make([]service.UsageLog, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, exportSampleLog(i))
	}
	return rows
}

const (
	exportExpectedHeaderEN = "Time (UTC),API Key,Model,Reasoning Effort,Inbound Endpoint,IP,Type,Billing Mode,Input Tokens,Output Tokens,Cache Read Tokens,Cache Creation Tokens,Rate,User billed,Original,First Token (ms),Duration (ms)"
	exportExpectedRowUTC   = "2026-03-08 00:00:00,demo-key,gpt-5.4,XHigh,/v1/chat/completions,203.0.113.10,Sync,Token,4057,101,278272,4,1,0.09288300,0.09288300,12,345"
)

func exportCSVLines(t *testing.T, body []byte) []string {
	t.Helper()
	require.True(t, bytes.HasPrefix(body, usageExportUTF8BOM), "csv must start with UTF-8 BOM")
	text := strings.TrimSuffix(string(body[len(usageExportUTF8BOM):]), "\n")
	return strings.Split(text, "\n")
}

func TestUserUsageExport_StreamsLargeVolumeInBatches(t *testing.T) {
	const totalRows = 120_000
	repo := &usageExportRepoStub{
		generate: func(ctx context.Context, opts usagestats.UsageLogStreamOptions, emit func([]service.UsageLog) error) error {
			require.Equal(t, usageExportBatchSize, opts.BatchSize)
			for start := 0; start < totalRows; start += opts.BatchSize {
				if err := ctx.Err(); err != nil {
					return err
				}
				end := start + opts.BatchSize
				if end > totalRows {
					end = totalRows
				}
				batch := make([]service.UsageLog, 0, end-start)
				for i := start; i < end; i++ {
					batch = append(batch, exportSampleLog(i))
				}
				if err := emit(batch); err != nil {
					return err
				}
			}
			return nil
		},
	}
	router := newUsageExportTestRouter(repo, nil, exportSubject())

	req := httptest.NewRequest(http.MethodGet, "/usage/export?start_date=2026-03-01&end_date=2026-03-08&timezone=UTC", nil)
	rec := newFlushRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Equal(t, "attachment; filename=usage_2026-03-01_to_2026-03-08.csv", rec.Header().Get("Content-Disposition"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "500000", rec.Header().Get(usageExportRowLimitHeader))

	lines := exportCSVLines(t, rec.Body.Bytes())
	require.Len(t, lines, totalRows+1, "header + one line per record")
	require.Equal(t, exportExpectedHeaderEN, lines[0])
	require.Equal(t, exportExpectedRowUTC, lines[1])
	require.Equal(t, exportExpectedRowUTC, lines[totalRows])

	// One flush per batch (plus the final flush) proves rows were pushed incrementally
	// instead of being buffered until the end.
	_, batches, _, _ := repo.snapshot()
	require.Equal(t, totalRows/usageExportBatchSize, batches)
	require.GreaterOrEqual(t, rec.flushCount(), batches)
}

func TestUserUsageExport_UsesSameFiltersAsListEndpoint(t *testing.T) {
	repo := &usageExportRepoStub{rows: exportSampleRows(1)}
	router := newUsageExportTestRouter(repo, nil, exportSubject())
	query := "group_id=3&model=gpt-5.4&request_type=stream&native_compaction_v2=true&billing_type=1&billing_mode=token" +
		"&start_date=2026-03-01&end_date=2026-03-08&timezone=Asia/Shanghai&sort_by=created_at&sort_order=asc&user_id=7"

	listReq := httptest.NewRequest(http.MethodGet, "/usage?page=1&page_size=100&"+query, nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	exportReq := httptest.NewRequest(http.MethodGet, "/usage/export?"+query, nil)
	exportRec := httptest.NewRecorder()
	router.ServeHTTP(exportRec, exportReq)
	require.Equal(t, http.StatusOK, exportRec.Code)

	_, _, streamFilters, opts := repo.snapshot()
	require.Equal(t, repo.listFilters, streamFilters, "export must select exactly the rows the list shows")
	require.Equal(t, int64(42), streamFilters.UserID, "user_id query must not override the authenticated user")
	require.Equal(t, int64(3), streamFilters.GroupID)
	require.NotNil(t, streamFilters.RequestType)
	require.Equal(t, int16(service.RequestTypeStream), *streamFilters.RequestType)
	require.NotNil(t, streamFilters.StartTime)
	require.NotNil(t, streamFilters.EndTime)
	require.Equal(t, "created_at", opts.SortBy)
	require.Equal(t, "asc", opts.SortOrder)
	require.Equal(t, usageExportMaxRows, opts.MaxRows)
	require.Equal(t, usageExportBatchSize, opts.BatchSize)
}

func TestUserUsageExport_LocalizesHeadersAndTimezone(t *testing.T) {
	repo := &usageExportRepoStub{rows: exportSampleRows(1)}
	router := newUsageExportTestRouter(repo, nil, exportSubject())

	req := httptest.NewRequest(http.MethodGet, "/usage/export?timezone=Asia/Shanghai&start_date=2026-03-01&end_date=2026-03-08", nil)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	lines := exportCSVLines(t, rec.Body.Bytes())
	require.Len(t, lines, 2)
	require.Equal(t, "时间 (Asia/Shanghai),API 密钥,模型,推理强度,入站端点,IP,类型,计费模式,输入 Token,输出 Token,缓存读取 Token,缓存创建 Token,倍率,用户扣费,原始,首 Token (ms),耗时 (ms)", lines[0])
	require.Equal(t, "2026-03-08 08:00:00,demo-key,gpt-5.4,XHigh,/v1/chat/completions,203.0.113.10,同步,按量,4057,101,278272,4,1,0.09288300,0.09288300,12,345", lines[1])
}

func TestUserUsageExport_RejectsOversizedExportBeforeStreaming(t *testing.T) {
	repo := &usageExportRepoStub{streamErr: usagestats.ErrUsageLogStreamTooLarge}
	router := newUsageExportTestRouter(repo, nil, exportSubject())

	req := httptest.NewRequest(http.MethodGet, "/usage/export?start_date=2026-01-01&end_date=2026-03-08", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.Empty(t, rec.Header().Get("Content-Disposition"))
	var body struct {
		Code     int               `json:"code"`
		Reason   string            `json:"reason"`
		Message  string            `json:"message"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, http.StatusBadRequest, body.Code)
	require.Equal(t, service.UsageExportTooLargeReason, body.Reason)
	require.Equal(t, "500000", body.Metadata["limit"])
	require.NotEmpty(t, body.Message)
}

func TestUserUsageExport_RequiresAuthentication(t *testing.T) {
	repo := &usageExportRepoStub{rows: exportSampleRows(1)}
	router := newUsageExportTestRouter(repo, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/usage/export", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	calls, _, _, _ := repo.snapshot()
	require.Zero(t, calls, "unauthenticated requests must never reach the repository")
}

func TestUserUsageExport_APIKeyOwnershipIsEnforced(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeSimple}

	t.Run("foreign key is forbidden", func(t *testing.T) {
		repo := &usageExportRepoStub{rows: exportSampleRows(1)}
		apiKeySvc := service.NewAPIKeyService(&usageExportAPIKeyRepoStub{ownerID: 7}, nil, nil, nil, nil, nil, cfg)
		router := newUsageExportTestRouter(repo, apiKeySvc, exportSubject())

		req := httptest.NewRequest(http.MethodGet, "/usage/export?api_key_id=99", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusForbidden, rec.Code)
		calls, _, _, _ := repo.snapshot()
		require.Zero(t, calls)
	})

	t.Run("own key is exported", func(t *testing.T) {
		repo := &usageExportRepoStub{rows: exportSampleRows(1)}
		apiKeySvc := service.NewAPIKeyService(&usageExportAPIKeyRepoStub{ownerID: 42}, nil, nil, nil, nil, nil, cfg)
		router := newUsageExportTestRouter(repo, apiKeySvc, exportSubject())

		req := httptest.NewRequest(http.MethodGet, "/usage/export?api_key_id=99", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		_, _, filters, _ := repo.snapshot()
		require.Equal(t, int64(99), filters.APIKeyID)
		require.Equal(t, int64(42), filters.UserID)
	})
}

func TestUserUsageExport_ScopesToAuthenticatedUser(t *testing.T) {
	repo := &usageExportRepoStub{rows: exportSampleRows(1)}
	router := newUsageExportTestRouter(repo, nil, exportSubject())

	req := httptest.NewRequest(http.MethodGet, "/usage/export?user_id=7&account_id=9", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	_, _, filters, _ := repo.snapshot()
	require.Equal(t, int64(42), filters.UserID)
	require.Zero(t, filters.AccountID, "user exports never accept account scoping")
}

func TestUserUsageExport_StopsWhenClientDisconnects(t *testing.T) {
	repo := &usageExportRepoStub{
		generate: func(ctx context.Context, opts usagestats.UsageLogStreamOptions, emit func([]service.UsageLog) error) error {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := emit(exportSampleRows(opts.BatchSize)); err != nil {
					return err
				}
			}
		},
	}
	router := newUsageExportTestRouter(repo, nil, exportSubject())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/usage/export", nil).WithContext(ctx)
	rec := newFlushRecorder()
	rec.onFlush = func(count int) {
		if count == 3 {
			cancel() // the client went away after receiving three batches
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(rec, req)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("export did not stop after the client disconnected")
	}

	_, batches, _, _ := repo.snapshot()
	require.LessOrEqual(t, batches, 4, "at most one batch may be in flight after cancellation")
	require.GreaterOrEqual(t, batches, 3)

	// The per-user slot must be released so the user can export again.
	repo.generate = nil
	repo.rows = exportSampleRows(2)
	retry := httptest.NewRequest(http.MethodGet, "/usage/export", nil)
	retryRec := httptest.NewRecorder()
	router.ServeHTTP(retryRec, retry)
	require.Equal(t, http.StatusOK, retryRec.Code)
	require.Len(t, exportCSVLines(t, retryRec.Body.Bytes()), 3)
}

func TestUserUsageExport_RejectsConcurrentExportForSameUser(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	repo := &usageExportRepoStub{
		generate: func(ctx context.Context, opts usagestats.UsageLogStreamOptions, emit func([]service.UsageLog) error) error {
			if err := emit(exportSampleRows(1)); err != nil {
				return err
			}
			startOnce.Do(func() { close(started) })
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	router := newUsageExportTestRouter(repo, nil, exportSubject())

	firstRec := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		router.ServeHTTP(firstRec, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("first export never started streaming")
	}

	secondRec := httptest.NewRecorder()
	router.ServeHTTP(secondRec, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	require.Equal(t, http.StatusTooManyRequests, secondRec.Code)
	require.Equal(t, "5", secondRec.Header().Get("Retry-After"))
	var body struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(secondRec.Body.Bytes(), &body))
	require.Equal(t, usageExportInProgressReason, body.Reason)

	// Another user is unaffected by the first user's running export.
	otherRepo := &usageExportRepoStub{rows: exportSampleRows(1)}
	otherRouter := newUsageExportTestRouter(otherRepo, nil, &middleware2.AuthSubject{UserID: 43})
	otherRec := httptest.NewRecorder()
	otherRouter.ServeHTTP(otherRec, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	require.Equal(t, http.StatusOK, otherRec.Code)

	close(release)
	select {
	case <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("first export did not finish after release")
	}
	require.Equal(t, http.StatusOK, firstRec.Code)
	require.Len(t, exportCSVLines(t, firstRec.Body.Bytes()), 2)
}

func TestUserUsageExport_GuardsSpreadsheetFormulaCells(t *testing.T) {
	row := exportSampleLog(0)
	row.APIKey = &service.APIKey{ID: 7, Name: `=HYPERLINK("http://evil")`}
	row.RequestedModel = "-evil-model"
	endpoint := "@cmd"
	row.InboundEndpoint = &endpoint
	ip := "+1234"
	row.IPAddress = &ip
	repo := &usageExportRepoStub{rows: []service.UsageLog{row}}
	router := newUsageExportTestRouter(repo, nil, exportSubject())

	req := httptest.NewRequest(http.MethodGet, "/usage/export?timezone=UTC", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	lines := exportCSVLines(t, rec.Body.Bytes())
	require.Len(t, lines, 2)
	require.Equal(t, `2026-03-08 00:00:00,"'=HYPERLINK(""http://evil"")",'-evil-model,XHigh,'@cmd,'+1234,Sync,Token,4057,101,278272,4,1,0.09288300,0.09288300,12,345`, lines[1])
}

func TestUserUsageExport_ZeroRowsWritesHeaderOnly(t *testing.T) {
	repo := &usageExportRepoStub{}
	router := newUsageExportTestRouter(repo, nil, exportSubject())

	req := httptest.NewRequest(http.MethodGet, "/usage/export?timezone=UTC", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "attachment; filename=usage_all.csv", rec.Header().Get("Content-Disposition"))
	lines := exportCSVLines(t, rec.Body.Bytes())
	require.Equal(t, []string{exportExpectedHeaderEN}, lines)
}

func TestUserUsageExport_MidStreamFailureNeverWritesJSONIntoCSV(t *testing.T) {
	repo := &usageExportRepoStub{
		generate: func(ctx context.Context, opts usagestats.UsageLogStreamOptions, emit func([]service.UsageLog) error) error {
			if err := emit(exportSampleRows(3)); err != nil {
				return err
			}
			return errors.New("database connection lost")
		},
	}
	router := newUsageExportTestRouter(repo, nil, exportSubject())

	req := httptest.NewRequest(http.MethodGet, "/usage/export?timezone=UTC", nil)
	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { router.ServeHTTP(rec, req) })

	require.Equal(t, http.StatusOK, rec.Code, "status is already committed once streaming started")
	body := rec.Body.String()
	require.NotContains(t, body, `"code"`, "JSON error envelopes must never be appended to a CSV body")
	require.Len(t, exportCSVLines(t, rec.Body.Bytes()), 4)
}

func TestUsageExportFilename(t *testing.T) {
	require.Equal(t, "usage_2026-03-01_to_2026-03-08.csv", usageExportFilename("2026-03-01", "2026-03-08"))
	require.Equal(t, "usage_from_2026-03-01.csv", usageExportFilename("2026-03-01", ""))
	require.Equal(t, "usage_until_2026-03-08.csv", usageExportFilename("", "2026-03-08"))
	require.Equal(t, "usage_all.csv", usageExportFilename("", ""))
	require.Equal(t, "usage_all.csv", usageExportFilename(`2026-03-01"; rm -rf`, "../etc"))
}

func TestUsageExportLabelsFor(t *testing.T) {
	require.Equal(t, "时间", usageExportLabelsFor("zh-CN,zh;q=0.9").time)
	require.Equal(t, "时间", usageExportLabelsFor("zh").time)
	require.Equal(t, "Time", usageExportLabelsFor("en-US,en;q=0.9").time)
	require.Equal(t, "Time", usageExportLabelsFor("").time)
	require.Equal(t, "Time", usageExportLabelsFor("fr-FR").time)
}

func TestFormatReasoningEffortLabel(t *testing.T) {
	cases := map[string]string{
		"": "-", "  ": "-", "low": "Low", "MEDIUM": "Medium", "high": "High",
		"xhigh": "XHigh", "extra-high": "XHigh", "extra_high": "XHigh", "max": "Max",
		"none": "-", "minimal": "-", "custom": "Custom", "x": "X",
	}
	for input, want := range cases {
		require.Equal(t, want, formatReasoningEffortLabel(input), "input %q", input)
	}
}

func TestUsageExportGuard(t *testing.T) {
	var guard usageExportGuard
	release, ok := guard.acquire(1)
	require.True(t, ok)
	_, ok = guard.acquire(1)
	require.False(t, ok, "second export for the same user must be rejected")
	otherRelease, ok := guard.acquire(2)
	require.True(t, ok, "other users are independent")
	otherRelease()
	release()
	release() // idempotent
	_, ok = guard.acquire(1)
	require.True(t, ok, "slot must be free after release")
}

// TestUserUsageExport_MidStreamFailureAbortsRealConnection drives the handler through a
// real HTTP/1.1 server: once rows are on the wire a repository failure must surface to the
// client as a broken download (unexpected EOF), never as a complete-looking CSV.
func TestUserUsageExport_MidStreamFailureAbortsRealConnection(t *testing.T) {
	repo := &usageExportRepoStub{
		generate: func(ctx context.Context, opts usagestats.UsageLogStreamOptions, emit func([]service.UsageLog) error) error {
			if err := emit(exportSampleRows(5)); err != nil {
				return err
			}
			return errors.New("database connection lost")
		},
	}
	router := newUsageExportTestRouter(repo, nil, exportSubject())
	server := httptest.NewServer(router)
	defer server.Close()

	resp, err := http.Get(server.URL + "/usage/export?timezone=UTC")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, readErr := io.ReadAll(resp.Body)
	require.Error(t, readErr, "client must observe a transport failure instead of a clean end of body")
	require.True(t, errors.Is(readErr, io.ErrUnexpectedEOF) || strings.Contains(readErr.Error(), "EOF"), "unexpected error: %v", readErr)
	require.NotContains(t, string(body), `"code"`)
}

// TestUserUsageExport_SuccessfulRealConnectionCompletesCleanly is the positive control
// for the abort test: a healthy stream ends with a well-formed body over a real socket.
func TestUserUsageExport_SuccessfulRealConnectionCompletesCleanly(t *testing.T) {
	repo := &usageExportRepoStub{rows: exportSampleRows(2500)}
	router := newUsageExportTestRouter(repo, nil, exportSubject())
	server := httptest.NewServer(router)
	defer server.Close()

	resp, err := http.Get(server.URL + "/usage/export?timezone=UTC")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, exportCSVLines(t, body), 2501)
}
