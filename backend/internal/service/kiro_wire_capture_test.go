package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func wireCaptureForTest() *kiroWireCapture {
	return &kiroWireCapture{config: kiroWireCaptureConfig{UserID: 3, KeyID: 832, GroupID: 29, ExpiresAt: time.Now().Add(time.Hour)}, queue: make(chan kiroWireCaptureRecord, 128)}
}
func TestKiroWireCaptureScopeAndExpiry(t *testing.T) {
	c := wireCaptureForTest()
	group := int64(29)
	key := &APIKey{ID: 832, UserID: 3, GroupID: &group}
	require.True(t, c.matches(key, &group))
	key.ID++
	require.False(t, c.matches(key, &group))
	key.ID--
	key.UserID++
	require.False(t, c.matches(key, &group))
	key.UserID--
	other := int64(23)
	require.False(t, c.matches(key, &other))
	c.config.ExpiresAt = time.Now().Add(-time.Second)
	require.False(t, c.matches(key, &group))
	require.False(t, validKiroWireCaptureConfig(c.config, time.Now()))
	// A rare-failure hunt needs a window measured in hours, bounded at one day.
	c.config.ExpiresAt = time.Now().Add(12 * time.Hour)
	require.True(t, validKiroWireCaptureConfig(c.config, time.Now()))
	c.config.ExpiresAt = time.Now().Add(kiroWireCaptureMaxTTL + time.Minute)
	require.False(t, validKiroWireCaptureConfig(c.config, time.Now()))
	c.config.ExpiresAt = time.Now().Add(time.Hour)

	// Budget overrides are accepted only inside the hard ceilings.
	require.True(t, validKiroWireCaptureConfig(c.config, time.Now()))
	for _, bad := range []kiroWireCaptureConfig{
		{UserID: 3, KeyID: 832, GroupID: 29, ExpiresAt: c.config.ExpiresAt, MaxRequests: kiroWireCaptureMaxRequestsCeiling + 1},
		{UserID: 3, KeyID: 832, GroupID: 29, ExpiresAt: c.config.ExpiresAt, MaxBytes: kiroWireCaptureMaxBytesCeiling + 1},
		{UserID: 3, KeyID: 832, GroupID: 29, ExpiresAt: c.config.ExpiresAt, MaxRequests: -1},
		{UserID: 3, KeyID: 832, GroupID: 29, ExpiresAt: c.config.ExpiresAt, MaxBytes: -1},
	} {
		require.False(t, validKiroWireCaptureConfig(bad, time.Now()))
	}
	require.True(t, validKiroWireCaptureConfig(kiroWireCaptureConfig{
		UserID: 3, KeyID: 832, GroupID: 29, ExpiresAt: c.config.ExpiresAt,
		MaxRequests: kiroWireCaptureMaxRequestsCeiling, MaxBytes: kiroWireCaptureMaxBytesCeiling,
	}, time.Now()))
}

// An unconfigured capture keeps the small default budget; a configured one is
// honored verbatim. Both are enforced on the same byte/request counters that
// bound the capture at runtime.
func TestKiroWireCaptureLimitsDefaultAndOverride(t *testing.T) {
	c := wireCaptureForTest()
	require.Equal(t, int64(kiroWireCaptureDefaultMaxRequests), c.config.maxRequests())
	require.Equal(t, int64(kiroWireCaptureDefaultMaxBytes), c.config.maxBytes())

	c.config.MaxRequests = 1000
	c.config.MaxBytes = 2 << 30
	require.Equal(t, int64(1000), c.config.maxRequests())
	require.Equal(t, int64(2<<30), c.config.maxBytes())

	// The raised byte budget keeps recording where the default would have dropped.
	trace := &kiroWireTrace{capture: c, id: "test"}
	c.bytes.Store(kiroWireCaptureDefaultMaxBytes)
	trace.record("aws_bytes", 1, []byte("still recorded"), "")
	require.Equal(t, "still recorded", string((<-c.queue).Data))
	require.Equal(t, int64(0), c.dropped.Load())

	// The per-record cap is not configurable and still rejects an oversized frame.
	c.bytes.Store(0)
	trace.record("aws_bytes", 1, make([]byte, kiroWireCaptureMaxRecordBytes+1), "")
	require.Empty(t, c.queue)
	require.Equal(t, int64(1), c.dropped.Load())
}

// The request cap comes from the same config, so a raised allowance keeps
// arming traces past the default cut-off.
func TestKiroWireCaptureRequestCapFollowsConfig(t *testing.T) {
	old := activeKiroWireCapture
	activeKiroWireCapture = wireCaptureForTest()
	activeKiroWireCapture.config.MaxRequests = kiroWireCaptureDefaultMaxRequests + 2
	defer func() { activeKiroWireCapture = old }()

	group := int64(29)
	newRequest := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		c.Set(ginContextKeyAPIKey, &APIKey{ID: 832, UserID: 3, GroupID: &group})
		return c
	}
	parsed := &ParsedRequest{GroupID: &group, OriginalBody: []byte(`{"model":"test","messages":[]}`)}

	// One past the old hardcoded cap still captures.
	activeKiroWireCapture.requests.Store(kiroWireCaptureDefaultMaxRequests)
	ctx, finish := beginKiroWireCapture(context.Background(), newRequest(), parsed, &Account{ID: 42})
	require.NotNil(t, kiroWireTraceFromContext(ctx))
	finish()

	// The configured cap still stops the capture.
	activeKiroWireCapture.requests.Store(activeKiroWireCapture.config.maxRequests())
	ctx, finish = beginKiroWireCapture(context.Background(), newRequest(), parsed, &Account{ID: 42})
	require.Nil(t, kiroWireTraceFromContext(ctx))
	finish()
}
func TestKiroWireCaptureReadersAndWritersPreserveBytes(t *testing.T) {
	c := wireCaptureForTest()
	trace := &kiroWireTrace{capture: c, id: "test"}
	data := []byte("event: content_block_delta\ndata: {\"delta\":{\"type\":\"text_delta\",\"text\":\"course.\\n\\ncourse.\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	r := &kiroWireReadCloser{ReadCloser: io.NopCloser(bytes.NewReader(data)), trace: trace, stage: "aws_bytes", attempt: 1}
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, data, got)
	record := <-c.queue
	require.Equal(t, data, record.Data)
	require.Equal(t, "eof", (<-c.queue).Outcome)
	rec := httptest.NewRecorder()
	g, _ := gin.CreateTestContext(rec)
	w := &kiroWireResponseWriter{ResponseWriter: g.Writer, trace: trace}
	n, err := w.Write(data[:20])
	require.NoError(t, err)
	require.Equal(t, 20, n)
	_, err = w.WriteString(string(data[20:]))
	require.NoError(t, err)
	w.Flush()
	require.True(t, rec.Flushed)
	require.Equal(t, data, rec.Body.Bytes())
	a, b := <-c.queue, <-c.queue
	require.Equal(t, data, append(a.Data, b.Data...))
	require.Less(t, a.Sequence, b.Sequence)
	require.Equal(t, "client_flush", (<-c.queue).Stage)
	// Saturated diagnostics do not block or change downstream output.
	for len(c.queue) < cap(c.queue) {
		c.queue <- kiroWireCaptureRecord{}
	}
	_, err = w.Write([]byte("tail"))
	require.NoError(t, err)
	require.Equal(t, int64(1), c.dropped.Load())
	require.True(t, strings.HasSuffix(rec.Body.String(), "tail"))
}

type captureErrorReader struct{}

func (captureErrorReader) Read(p []byte) (int, error) { copy(p, "x"); return 1, io.ErrUnexpectedEOF }
func (captureErrorReader) Close() error               { return nil }
func TestKiroWireCaptureFailureAndBudgetAreDiagnosticOnly(t *testing.T) {
	c := wireCaptureForTest()
	tr := &kiroWireTrace{capture: c, id: "test"}
	r := &kiroWireReadCloser{ReadCloser: captureErrorReader{}, trace: tr, stage: "aws_bytes"}
	n, err := r.Read(make([]byte, 8))
	require.Equal(t, 1, n)
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	require.Equal(t, "x", string((<-c.queue).Data))
	require.Equal(t, "read_error", (<-c.queue).Outcome)
	c.bytes.Store(64 << 20)
	tr.record("client_sse", 0, []byte("preserved on wire"), "")
	require.Empty(t, c.queue)
	require.Equal(t, int64(1), c.dropped.Load())
	require.Nil(t, kiroWireTraceFromContext(context.Background()))
	ctx := context.WithValue(context.Background(), kiroWireTraceKey{}, tr)
	require.Same(t, tr, kiroWireTraceFromContext(context.WithoutCancel(ctx)))
}

func TestKiroWireCapturePreservesTrackedLifecycle(t *testing.T) {
	c := wireCaptureForTest()
	trace := &kiroWireTrace{capture: c, id: "test"}
	body := &kiroTrackedStreamBody{ReadCloser: io.NopCloser(strings.NewReader("terminal"))}
	resp := &http.Response{Body: body}
	captureKiroTranslatedBody(resp, trace)
	require.Same(t, body, trackedKiroStreamBody(resp))
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "terminal", string(got))
}

func TestKiroWireCaptureAuthenticatedEntryAndRestoration(t *testing.T) {
	old := activeKiroWireCapture
	activeKiroWireCapture = wireCaptureForTest()
	defer func() { activeKiroWireCapture = old }()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set("X-Client-Request-Id", "client-test")
	group := int64(29)
	c.Set(ginContextKeyAPIKey, &APIKey{ID: 832, UserID: 3, GroupID: &group})
	parsed := &ParsedRequest{GroupID: &group, OriginalBody: []byte(`{"model":"test","messages":[]}`)}
	original := c.Writer
	ctx, finish := beginKiroWireCapture(context.Background(), c, parsed, &Account{ID: 42})
	require.NotNil(t, kiroWireTraceFromContext(ctx))
	require.NotSame(t, original, c.Writer)
	finish()
	require.Same(t, original, c.Writer)
	first := <-activeKiroWireCapture.queue
	require.Equal(t, "client_request", first.Stage)
	require.Equal(t, parsed.OriginalBody, first.Data)
	require.Equal(t, "client-test", first.ClientID)
	require.Equal(t, "client_end", (<-activeKiroWireCapture.queue).Stage)
	activeKiroWireCapture.requests.Store(kiroWireCaptureDefaultMaxRequests)
	ctx, finish = beginKiroWireCapture(context.Background(), c, parsed, &Account{ID: 42})
	finish()
	require.Nil(t, kiroWireTraceFromContext(ctx))
	require.Same(t, original, c.Writer)
}
