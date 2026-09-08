package service

// Opt-in, expiring incident capture. No headers or credentials are collected.
// Raw bodies are explicitly authorized diagnostic data, kept in a private file;
// do not print them into application/ops logs. Decode AWS frames offline so the
// gateway parser, chunk boundaries, timing and retry decisions remain unchanged.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type kiroWireCaptureConfig struct {
	UserID    int64     `json:"user_id"`
	KeyID     int64     `json:"key_id"`
	GroupID   int64     `json:"group_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type kiroWireCaptureRecord struct {
	TraceID   string    `json:"trace_id"`
	ClientID  string    `json:"client_request_id,omitempty"`
	AccountID int64     `json:"account_id"`
	Stage     string    `json:"stage"`
	Attempt   int64     `json:"attempt"`
	Sequence  int64     `json:"sequence"`
	At        time.Time `json:"at"`
	Data      []byte    `json:"data,omitempty"` // JSON encodes binary as base64; hash is computed off-path.
	SHA256    string    `json:"sha256,omitempty"`
	Bytes     int       `json:"bytes"`
	Outcome   string    `json:"outcome,omitempty"`
	Dropped   int64     `json:"dropped"`
}

type kiroWireCapture struct {
	config   kiroWireCaptureConfig
	queue    chan kiroWireCaptureRecord
	bytes    atomic.Int64
	requests atomic.Int64
	dropped  atomic.Int64
	disabled atomic.Bool
}

type kiroWireTrace struct {
	capture      *kiroWireCapture
	id, clientID string
	accountID    int64
	seq          atomic.Int64
	attempt      atomic.Int64
}
type kiroWireTraceKey struct{}

var activeKiroWireCapture = loadKiroWireCapture("/app/data/kiro-wire-capture.json")

func validKiroWireCaptureConfig(c kiroWireCaptureConfig, now time.Time) bool {
	return c.UserID > 0 && c.KeyID > 0 && c.GroupID > 0 &&
		c.ExpiresAt.After(now) && !c.ExpiresAt.After(now.Add(2*time.Hour))
}

func loadKiroWireCapture(path string) *kiroWireCapture {
	// Configuration is read once at process startup, never on a request hot path.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg kiroWireCaptureConfig
	if json.Unmarshal(raw, &cfg) != nil || !validKiroWireCaptureConfig(cfg, time.Now()) {
		return nil
	}
	dir := filepath.Join(filepath.Dir(path), "kiro-wire-captures")
	if os.MkdirAll(dir, 0700) != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, uuid.NewString()+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil
	}
	c := &kiroWireCapture{config: cfg, queue: make(chan kiroWireCaptureRecord, 128)}
	go func() {
		defer f.Close()
		timer := time.NewTimer(time.Until(cfg.ExpiresAt))
		defer timer.Stop()
		encoder := json.NewEncoder(f)
		write := func(record kiroWireCaptureRecord) {
			if len(record.Data) > 0 {
				hash := sha256.Sum256(record.Data)
				record.SHA256 = fmt.Sprintf("%x", hash)
			}
			if encoder.Encode(record) != nil {
				c.disabled.Store(true)
			}
		}
		for {
			select {
			case record := <-c.queue:
				write(record)
			case <-timer.C:
				c.disabled.Store(true)
				// Bounded drain, no producer ever waits for diagnostic I/O.
				for i := 0; i < cap(c.queue); i++ {
					select {
					case record := <-c.queue:
						write(record)
					default:
						return
					}
				}
				return
			}
		}
	}()
	return c
}

func (c *kiroWireCapture) matches(key *APIKey, groupID *int64) bool {
	return c != nil && !c.disabled.Load() && time.Now().Before(c.config.ExpiresAt) &&
		key != nil && groupID != nil && key.GroupID != nil &&
		key.UserID == c.config.UserID && key.ID == c.config.KeyID &&
		*groupID == c.config.GroupID && *key.GroupID == c.config.GroupID
}

func (t *kiroWireTrace) record(stage string, attempt int64, data []byte, outcome string) {
	if t == nil || t.capture.disabled.Load() || !time.Now().Before(t.capture.config.ExpiresAt) {
		return
	}
	c := t.capture
	// Hard lifetime bound: <=64 MiB copied across every request and stage.
	// Queue saturation/budget exhaustion loses diagnostics, never client output.
	if c.bytes.Add(int64(len(data))) > 64<<20 || len(data) > 8<<20 {
		c.dropped.Add(1)
		return
	}
	record := kiroWireCaptureRecord{TraceID: t.id, ClientID: t.clientID, AccountID: t.accountID,
		Stage: stage, Attempt: attempt, Sequence: t.seq.Add(1), At: time.Now().UTC(),
		Bytes: len(data), Outcome: outcome, Dropped: c.dropped.Load()}
	record.Data = append([]byte(nil), data...)
	select {
	case c.queue <- record:
	default:
		c.dropped.Add(1)
	}
}

func kiroWireTraceFromContext(ctx context.Context) *kiroWireTrace {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(kiroWireTraceKey{}).(*kiroWireTrace)
	return t
}

func beginKiroWireCapture(ctx context.Context, c *gin.Context, parsed *ParsedRequest, account *Account) (context.Context, func()) {
	if activeKiroWireCapture == nil || c == nil || parsed == nil || account == nil {
		return ctx, func() {}
	}
	value, _ := c.Get(ginContextKeyAPIKey)
	key, _ := value.(*APIKey)
	if !activeKiroWireCapture.matches(key, parsed.GroupID) || activeKiroWireCapture.requests.Add(1) > 20 {
		return ctx, func() {}
	}
	t := &kiroWireTrace{capture: activeKiroWireCapture, id: uuid.NewString(), clientID: c.Request.Header.Get("X-Client-Request-Id"), accountID: account.ID}
	body := parsed.OriginalBody
	if len(body) == 0 && parsed.Body != nil {
		body = parsed.Body.Bytes()
	}
	t.record("client_request", 0, body, "")
	original := c.Writer
	c.Writer = &kiroWireResponseWriter{ResponseWriter: original, trace: t}
	return context.WithValue(ctx, kiroWireTraceKey{}, t), func() {
		t.record("client_end", 0, nil, fmt.Sprintf("http_%d", c.Writer.Status()))
		c.Writer = original
	}
}

type kiroWireReadCloser struct {
	io.ReadCloser
	trace   *kiroWireTrace
	stage   string
	attempt int64
}

func captureKiroTranslatedBody(resp *http.Response, trace *kiroWireTrace) {
	// Keep the outer lifecycle wrapper visible to finishKiroStreamResponse.
	// Replacing it would bypass producer joining and cache finalization.
	if tracked := trackedKiroStreamBody(resp); tracked != nil {
		tracked.ReadCloser = &kiroWireReadCloser{ReadCloser: tracked.ReadCloser, trace: trace, stage: "translated_sse"}
	} else if resp != nil && resp.Body != nil {
		resp.Body = &kiroWireReadCloser{ReadCloser: resp.Body, trace: trace, stage: "translated_sse"}
	}
}
func (r *kiroWireReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		r.trace.record(r.stage, r.attempt, p[:n], "")
	}
	if err != nil {
		outcome := "read_error"
		if err == io.EOF {
			outcome = "eof"
		}
		r.trace.record(r.stage+"_end", r.attempt, nil, outcome)
	}
	return n, err
}

type kiroWireResponseWriter struct {
	gin.ResponseWriter
	trace *kiroWireTrace
}

func (w *kiroWireResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.trace.record("client_sse", 0, p[:n], "")
	if err != nil {
		w.trace.record("client_write_error", 0, nil, "write_error")
	}
	return n, err
}
func (w *kiroWireResponseWriter) WriteString(s string) (int, error) {
	// Preserve the underlying WriteString implementation rather than rerouting.
	n, err := w.ResponseWriter.WriteString(s)
	w.trace.record("client_sse", 0, []byte(s[:n]), "")
	if err != nil {
		w.trace.record("client_write_error", 0, nil, "write_error")
	}
	return n, err
}

func (w *kiroWireResponseWriter) Flush() {
	w.ResponseWriter.Flush()
	w.trace.record("client_flush", 0, nil, "")
}
