package handler

import (
	"context"
	"encoding/csv"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

const (
	// usageExportMaxRows caps a single CSV export. Larger ranges must be split by
	// the caller; the limit is advertised in the X-Usage-Export-Row-Limit header.
	usageExportMaxRows int64 = 500_000
	// usageExportBatchSize is the keyset page size; each batch is flushed to the
	// client as soon as it is serialized.
	usageExportBatchSize = 2000
	// usageExportTimeout bounds one export end to end, including slow consumers.
	usageExportTimeout = 10 * time.Minute
	// usageExportInProgressRetryAfter is advertised when a user already has a
	// running export.
	usageExportInProgressRetryAfter = 5 * time.Second

	usageExportInProgressReason = "USAGE_EXPORT_IN_PROGRESS"
	usageExportCanceledReason   = "USAGE_EXPORT_CANCELED"
	usageExportTimeoutReason    = "USAGE_EXPORT_TIMEOUT"

	usageExportRowLimitHeader = "X-Usage-Export-Row-Limit"
	usageExportTimeLayout     = "2006-01-02 15:04:05"
)

var (
	usageExportUTF8BOM     = []byte{0xEF, 0xBB, 0xBF}
	usageExportDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// usageExportGuard allows a single in-flight CSV export per user in this process so
// one account cannot fan out many long-running streaming queries at once.
type usageExportGuard struct {
	mu     sync.Mutex
	active map[int64]struct{}
}

// acquire reserves the export slot for userID. ok is false when an export is already
// running; otherwise the returned release func must be called exactly once.
func (g *usageExportGuard) acquire(userID int64) (release func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == nil {
		g.active = make(map[int64]struct{})
	}
	if _, busy := g.active[userID]; busy {
		return nil, false
	}
	g.active[userID] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.active, userID)
			g.mu.Unlock()
		})
	}, true
}

// Export streams the current user's usage records as a CSV attachment.
// GET /api/v1/usage/export
//
// Filters are parsed by the exact code path used by List, so both endpoints always
// agree on which rows are included. Rows are read in keyset batches and flushed as
// they are serialized: memory stays bounded regardless of the export size and a
// client disconnect cancels the underlying queries between batches.
func (h *UsageHandler) Export(c *gin.Context) {
	parsed, ok := h.parseUserUsageFilters(c, false)
	if !ok {
		return
	}

	release, ok := h.exportGuard.acquire(parsed.Filters.UserID)
	if !ok {
		c.Header("Retry-After", strconv.Itoa(int(usageExportInProgressRetryAfter/time.Second)))
		response.ErrorWithDetails(c, http.StatusTooManyRequests,
			"An export is already running for this account; wait for it to finish before starting another",
			usageExportInProgressReason, nil)
		return
	}
	defer release()

	opts := usagestats.UsageLogStreamOptions{
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
		BatchSize: usageExportBatchSize,
		MaxRows:   usageExportMaxRows,
	}
	writer := newUsageCSVWriter(
		c,
		usageExportLabelsFor(c.GetHeader("Accept-Language")),
		usageExportLocation(c.Query("timezone")),
		usageExportFilename(c.Query("start_date"), c.Query("end_date")),
	)

	ctx, cancel := context.WithTimeout(c.Request.Context(), usageExportTimeout)
	defer cancel()

	err := h.usageService.StreamWithFilters(ctx, parsed.Filters, opts, writer.writeBatch)
	if err == nil {
		err = writer.finish()
	}
	if err == nil {
		return
	}

	if !writer.started {
		// Nothing has reached the client yet: answer with the regular JSON envelope.
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			response.ErrorFrom(c, infraerrors.GatewayTimeout(usageExportTimeoutReason, "Export timed out before any rows were produced; narrow the date range or filters"))
		case errors.Is(err, context.Canceled):
			response.ErrorFrom(c, infraerrors.ClientClosed(usageExportCanceledReason, "Export canceled by client"))
		default:
			response.ErrorFrom(c, err)
		}
		return
	}

	// Headers and rows are already on the wire, so a JSON error would corrupt the CSV.
	// Log with context and tear the connection down so clients see a failed download
	// instead of silently keeping a truncated file.
	slog.Warn("usage csv export aborted mid-stream",
		"user_id", parsed.Filters.UserID,
		"rows_written", writer.rows,
		"client_canceled", errors.Is(err, context.Canceled),
		"error", err.Error(),
	)
	abortStreamedResponse(c)
}

// abortStreamedResponse closes the underlying connection of a partially written
// response. Hijacking is only available on HTTP/1.x connections; where it is not
// (HTTP/2, test recorders) the response simply ends early.
func abortStreamedResponse(c *gin.Context) {
	defer func() {
		// gin's ResponseWriter panics when the underlying writer cannot be hijacked.
		_ = recover()
	}()
	conn, _, err := c.Writer.Hijack()
	if err != nil || conn == nil {
		return
	}
	_ = conn.Close()
}

// usageExportLabels holds the localized CSV header and enum labels. Values mirror the
// frontend usage table so the export reads the same as the UI.
type usageExportLabels struct {
	time, apiKey, model, reasoningEffort, inboundEndpoint, ipAddress, requestType string
	billingMode, inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens  string
	rate, userBilled, original, firstToken, duration                              string
	// longContext explains a higher-than-usual unit price in the exported sheet,
	// mirroring the badge the usage table shows for the same row.
	longContext  string
	requestTypes map[string]string
	billingModes map[string]string
}

var usageExportLabelsEN = usageExportLabels{
	time: "Time", apiKey: "API Key", model: "Model", reasoningEffort: "Reasoning Effort",
	inboundEndpoint: "Inbound Endpoint", ipAddress: "IP", requestType: "Type",
	billingMode: "Billing Mode", inputTokens: "Input Tokens", outputTokens: "Output Tokens",
	cacheReadTokens: "Cache Read Tokens", cacheCreationTokens: "Cache Creation Tokens",
	rate: "Rate", userBilled: "User billed", original: "Original",
	firstToken: "First Token (ms)", duration: "Duration (ms)",
	longContext: "Long Context",
	requestTypes: map[string]string{
		"sync": "Sync", "stream": "Stream", "ws_v2": "WS", "cyber": "Cyber", "live": "Live", "unknown": "Unknown",
	},
	billingModes: map[string]string{
		string(service.BillingModeToken):      "Token",
		string(service.BillingModePerRequest): "Per Request",
		string(service.BillingModeImage):      "Image",
		string(service.BillingModeVideo):      "Video",
	},
}

var usageExportLabelsZH = usageExportLabels{
	time: "时间", apiKey: "API 密钥", model: "模型", reasoningEffort: "推理强度",
	inboundEndpoint: "入站端点", ipAddress: "IP", requestType: "类型",
	billingMode: "计费模式", inputTokens: "输入 Token", outputTokens: "输出 Token",
	cacheReadTokens: "缓存读取 Token", cacheCreationTokens: "缓存创建 Token",
	rate: "倍率", userBilled: "用户扣费", original: "原始",
	firstToken: "首 Token (ms)", duration: "耗时 (ms)",
	longContext: "长上下文计价",
	requestTypes: map[string]string{
		"sync": "同步", "stream": "流式", "ws_v2": "WS", "cyber": "安全策略", "live": "Live", "unknown": "未知",
	},
	billingModes: map[string]string{
		string(service.BillingModeToken):      "按量",
		string(service.BillingModePerRequest): "按次",
		string(service.BillingModeImage):      "按次(图片)",
		string(service.BillingModeVideo):      "按次(视频)",
	},
}

// usageExportLabelsFor picks the label set from the first Accept-Language tag; the
// panel only ships zh and en, and en is the fallback.
func usageExportLabelsFor(acceptLanguage string) usageExportLabels {
	first := acceptLanguage
	if idx := strings.IndexAny(first, ",;"); idx >= 0 {
		first = first[:idx]
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(first)), "zh") {
		return usageExportLabelsZH
	}
	return usageExportLabelsEN
}

func (l usageExportLabels) header(loc *time.Location) []string {
	return []string{
		l.time + " (" + loc.String() + ")",
		l.apiKey, l.model, l.reasoningEffort, l.inboundEndpoint, l.ipAddress, l.requestType,
		l.billingMode, l.inputTokens, l.outputTokens, l.cacheReadTokens, l.cacheCreationTokens,
		l.rate, l.userBilled, l.original, l.firstToken, l.duration, l.longContext,
	}
}

func (l usageExportLabels) requestTypeLabel(requestType string) string {
	if label, ok := l.requestTypes[requestType]; ok {
		return label
	}
	return l.requestTypes["unknown"]
}

func (l usageExportLabels) billingModeLabel(mode string) string {
	if label, ok := l.billingModes[mode]; ok {
		return label
	}
	return l.billingModes[string(service.BillingModeToken)]
}

// usageExportLocation resolves the IANA zone sent by the panel, falling back to the
// server timezone exactly like the date-range filters do.
func usageExportLocation(userTZ string) *time.Location {
	if tz := strings.TrimSpace(userTZ); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return timezone.Location()
}

// usageExportFilename mirrors the browser-side naming (usage_<start>_to_<end>.csv);
// only validated YYYY-MM-DD values are embedded so the header stays plain ASCII.
func usageExportFilename(startDate, endDate string) string {
	start := strings.TrimSpace(startDate)
	end := strings.TrimSpace(endDate)
	if !usageExportDatePattern.MatchString(start) {
		start = ""
	}
	if !usageExportDatePattern.MatchString(end) {
		end = ""
	}
	switch {
	case start != "" && end != "":
		return "usage_" + start + "_to_" + end + ".csv"
	case start != "":
		return "usage_from_" + start + ".csv"
	case end != "":
		return "usage_until_" + end + ".csv"
	default:
		return "usage_all.csv"
	}
}

// usageCSVWriter serializes usage log batches straight into the response body.
type usageCSVWriter struct {
	c        *gin.Context
	csv      *csv.Writer
	labels   usageExportLabels
	loc      *time.Location
	filename string
	started  bool
	rows     int64
}

func newUsageCSVWriter(c *gin.Context, labels usageExportLabels, loc *time.Location, filename string) *usageCSVWriter {
	return &usageCSVWriter{
		c:        c,
		csv:      csv.NewWriter(c.Writer),
		labels:   labels,
		loc:      loc,
		filename: filename,
	}
}

// start writes the response headers, the UTF-8 BOM (Excel compatibility) and the
// header row. It runs lazily on the first batch so pre-flight failures can still
// answer with JSON.
func (w *usageCSVWriter) start() error {
	if w.started {
		return nil
	}
	w.started = true
	header := w.c.Writer.Header()
	header.Set("Content-Type", "text/csv; charset=utf-8")
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": w.filename}))
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set(usageExportRowLimitHeader, strconv.FormatInt(usageExportMaxRows, 10))
	w.c.Status(http.StatusOK)
	if _, err := w.c.Writer.Write(usageExportUTF8BOM); err != nil {
		return err
	}
	return w.csv.Write(w.labels.header(w.loc))
}

// writeBatch is the stream callback: it serializes one keyset batch and pushes it to
// the client before the next batch is queried.
func (w *usageCSVWriter) writeBatch(batch []service.UsageLog) error {
	if err := w.start(); err != nil {
		return err
	}
	for i := range batch {
		row := dto.UsageLogFromService(&batch[i])
		if err := w.csv.Write(w.record(row)); err != nil {
			return err
		}
		w.rows++
	}
	return w.flush()
}

// finish guarantees a well-formed (possibly header-only) CSV once the stream ends.
func (w *usageCSVWriter) finish() error {
	if err := w.start(); err != nil {
		return err
	}
	return w.flush()
}

func (w *usageCSVWriter) flush() error {
	w.csv.Flush()
	if err := w.csv.Error(); err != nil {
		return err
	}
	w.c.Writer.Flush()
	return nil
}

// record renders one user-facing usage log (same DTO as GET /usage) as CSV cells in
// the historical browser export column order.
func (w *usageCSVWriter) record(row *dto.UsageLog) []string {
	apiKeyName := ""
	if row.APIKey != nil {
		apiKeyName = row.APIKey.Name
	}
	return []string{
		row.CreatedAt.In(w.loc).Format(usageExportTimeLayout),
		guardCSVCell(apiKeyName),
		guardCSVCell(row.Model),
		guardCSVCell(formatReasoningEffortLabel(derefString(row.ReasoningEffort))),
		guardCSVCell(derefString(row.InboundEndpoint)),
		guardCSVCell(derefString(row.IPAddress)),
		w.labels.requestTypeLabel(row.RequestType),
		w.labels.billingModeLabel(displayBillingMode(row)),
		strconv.Itoa(row.InputTokens),
		strconv.Itoa(row.OutputTokens),
		strconv.Itoa(row.CacheReadTokens),
		strconv.Itoa(row.CacheCreationTokens),
		strconv.FormatFloat(row.RateMultiplier, 'f', -1, 64),
		strconv.FormatFloat(row.ActualCost, 'f', 8, 64),
		strconv.FormatFloat(row.TotalCost, 'f', 8, 64),
		formatOptionalInt(row.FirstTokenMs),
		formatOptionalInt(row.DurationMs),
		formatLongContextMarker(row.LongContextBillingApplied),
	}
}

// displayBillingMode mirrors the UI: explicit video/token modes win, historical image
// rows without a stored mode are inferred from image_count.
func displayBillingMode(row *dto.UsageLog) string {
	mode := derefString(row.BillingMode)
	switch mode {
	case string(service.BillingModeVideo), string(service.BillingModeToken):
		return mode
	}
	if row.ImageCount > 0 && mode == "" {
		return string(service.BillingModeImage)
	}
	return mode
}

// formatReasoningEffortLabel mirrors the UI formatter (Low/Medium/High/XHigh/Max, "-"
// for absent or minimal efforts).
func formatReasoningEffortLabel(effort string) string {
	raw := strings.TrimSpace(effort)
	if raw == "" {
		return "-"
	}
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(raw))
	switch normalized {
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh", "extrahigh":
		return "XHigh"
	case "max":
		return "Max"
	case "none", "minimal":
		return "-"
	}
	if len(raw) > 1 {
		return strings.ToUpper(raw[:1]) + raw[1:]
	}
	return strings.ToUpper(raw)
}

// formatLongContextMarker renders the long-context billing flag as a stable token so
// downstream spreadsheets can filter on it. Empty for ordinary requests keeps the
// common case (the vast majority of rows) visually clean.
func formatLongContextMarker(applied bool) string {
	if !applied {
		return ""
	}
	return "long-context"
}

// guardCSVCell neutralizes spreadsheet formula injection for free-text cells by
// prefixing values that start with a formula trigger character. A lone trigger
// character (e.g. the "-" placeholder for an absent reasoning effort) cannot form
// a formula and is left untouched so the export stays readable.
func guardCSVCell(value string) string {
	if len(value) < 2 {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}

func formatOptionalInt(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
