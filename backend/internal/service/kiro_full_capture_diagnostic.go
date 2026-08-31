package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Temporary production diagnostic for incident 175e6e7a. This branch is not
// intended for permanent release: it stores unredacted request/response bytes.
var (
	kiroFullCaptureRoot              = "/app/data/kiro-full-capture-175e6e7a"
	kiroFullCaptureMaxSessions int64 = 32
	kiroFullCaptureSessions    atomic.Int64
)

type kiroFullCaptureContextKey struct{}

type kiroFullCaptureSession struct {
	dir      string
	attempts atomic.Int64
}

type kiroFullCaptureAttempt struct {
	dir string
}

type kiroFullCaptureResponseBody struct {
	source io.ReadCloser
	file   *os.File
	once   sync.Once
}

type kiroFullCaptureMirrorWriter struct {
	primary io.Writer
	capture io.Writer
}

func maybeStartKiroFullCapture(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, body []byte) context.Context {
	if parsed == nil || parsed.SessionContext == nil || parsed.SessionContext.UserID != 1 ||
		parsed.GroupID == nil || *parsed.GroupID != 29 || !strings.EqualFold(strings.TrimSpace(parsed.Model), "claude-opus-5") {
		return ctx
	}
	sequence := kiroFullCaptureSessions.Add(1)
	if sequence > kiroFullCaptureMaxSessions {
		return ctx
	}
	if err := os.MkdirAll(kiroFullCaptureRoot, 0o700); err != nil {
		return ctx
	}
	_ = os.Chmod(kiroFullCaptureRoot, 0o700)
	accountID := int64(0)
	accountName := ""
	if account != nil {
		accountID = account.ID
		accountName = account.Name
	}
	dir := filepath.Join(kiroFullCaptureRoot, fmt.Sprintf(
		"%s_%02d_%s_account-%d",
		time.Now().UTC().Format("20060102T150405.000000000Z"), sequence, uuid.NewString(), accountID,
	))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return ctx
	}
	session := &kiroFullCaptureSession{dir: dir}
	metadata := map[string]any{
		"captured_at":    time.Now().UTC().Format(time.RFC3339Nano),
		"sequence":       sequence,
		"user_id":        parsed.SessionContext.UserID,
		"api_key_id":     parsed.SessionContext.APIKeyID,
		"group_id":       *parsed.GroupID,
		"model":          parsed.Model,
		"stream":         parsed.Stream,
		"account_id":     accountID,
		"account_name":   accountName,
		"gateway_path":   requestPath(c),
		"gateway_method": requestMethod(c),
	}
	_ = writeKiroFullCaptureJSON(filepath.Join(dir, "00_metadata.json"), metadata)
	if c != nil && c.Request != nil {
		_ = writeKiroFullCaptureJSON(filepath.Join(dir, "01_client_request_headers.json"), c.Request.Header)
	}
	_ = writeKiroFullCaptureFile(filepath.Join(dir, "02_client_request_body.json"), body)
	return context.WithValue(ctx, kiroFullCaptureContextKey{}, session)
}

func requestPath(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	return c.Request.URL.RequestURI()
}

func requestMethod(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return c.Request.Method
}

func kiroFullCaptureFromContext(ctx context.Context) *kiroFullCaptureSession {
	if ctx == nil {
		return nil
	}
	session, _ := ctx.Value(kiroFullCaptureContextKey{}).(*kiroFullCaptureSession)
	return session
}

func (s *kiroFullCaptureSession) beginAttempt(account *Account, endpointName string, req *http.Request, payload []byte) *kiroFullCaptureAttempt {
	if s == nil {
		return nil
	}
	number := s.attempts.Add(1)
	dir := filepath.Join(s.dir, fmt.Sprintf("attempt-%02d", number))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	metadata := map[string]any{
		"captured_at":   time.Now().UTC().Format(time.RFC3339Nano),
		"attempt":       number,
		"account_id":    accountID,
		"endpoint_name": endpointName,
	}
	if req != nil {
		metadata["method"] = req.Method
		if req.URL != nil {
			metadata["url"] = req.URL.String()
		}
		_ = writeKiroFullCaptureJSON(filepath.Join(dir, "01_upstream_request_headers.json"), req.Header)
	}
	_ = writeKiroFullCaptureJSON(filepath.Join(dir, "00_attempt_metadata.json"), metadata)
	_ = writeKiroFullCaptureFile(filepath.Join(dir, "02_upstream_request_body.json"), payload)
	return &kiroFullCaptureAttempt{dir: dir}
}

func (a *kiroFullCaptureAttempt) recordTransportError(err error) {
	if a == nil || err == nil {
		return
	}
	_ = writeKiroFullCaptureFile(filepath.Join(a.dir, "03_transport_error.txt"), []byte(err.Error()))
}

func (a *kiroFullCaptureAttempt) wrapResponse(resp *http.Response) {
	if a == nil || resp == nil {
		return
	}
	metadata := map[string]any{
		"captured_at":       time.Now().UTC().Format(time.RFC3339Nano),
		"status":            resp.Status,
		"status_code":       resp.StatusCode,
		"content_length":    resp.ContentLength,
		"transfer_encoding": resp.TransferEncoding,
		"headers":           resp.Header,
	}
	_ = writeKiroFullCaptureJSON(filepath.Join(a.dir, "03_upstream_response.json"), metadata)
	if resp.Body == nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(a.dir, "04_upstream_response.eventstream"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	resp.Body = &kiroFullCaptureResponseBody{source: resp.Body, file: f}
}

func (b *kiroFullCaptureResponseBody) Read(p []byte) (int, error) {
	n, err := b.source.Read(p)
	if n > 0 {
		_, _ = b.file.Write(p[:n])
	}
	return n, err
}

func (b *kiroFullCaptureResponseBody) Close() error {
	var sourceErr error
	b.once.Do(func() {
		sourceErr = b.source.Close()
		_ = b.file.Sync()
		_ = b.file.Close()
	})
	return sourceErr
}

func (s *kiroFullCaptureSession) writeClientResponseHeaders(headers http.Header) {
	if s == nil {
		return
	}
	_ = writeKiroFullCaptureJSON(filepath.Join(s.dir, "03_client_response_headers.json"), headers)
}

func (s *kiroFullCaptureSession) writeClientResponseBody(body []byte, extension string) {
	if s == nil {
		return
	}
	extension = strings.TrimSpace(extension)
	if extension == "" {
		extension = "bin"
	}
	_ = writeKiroFullCaptureFile(filepath.Join(s.dir, "04_client_response."+extension), body)
}

func (s *kiroFullCaptureSession) newClientResponseMirror(primary io.Writer) (io.Writer, io.Closer) {
	if s == nil || primary == nil {
		return primary, nil
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "04_client_response.sse"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return primary, nil
	}
	return &kiroFullCaptureMirrorWriter{primary: primary, capture: f}, f
}

func (w *kiroFullCaptureMirrorWriter) Write(p []byte) (int, error) {
	n, err := w.primary.Write(p)
	if n > 0 {
		_, _ = w.capture.Write(p[:n])
	}
	return n, err
}

func writeKiroFullCaptureJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return writeKiroFullCaptureFile(path, body)
}

func writeKiroFullCaptureFile(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (s *kiroFullCaptureSession) String() string {
	if s == nil {
		return ""
	}
	return s.dir + "#" + strconv.FormatInt(s.attempts.Load(), 10)
}
