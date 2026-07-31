package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"go.uber.org/zap"
)

const (
	kiroNetworkPhaseClientAcquire       = "client_acquire"
	kiroNetworkPhaseConnectionAcquire   = "connection_acquire"
	kiroNetworkPhaseDNS                 = "dns"
	kiroNetworkPhaseTCPConnect          = "tcp_connect"
	kiroNetworkPhaseProxyConnect        = "proxy_connect"
	kiroNetworkPhaseTLSHandshake        = "tls_handshake"
	kiroNetworkPhaseRequestWrite        = "request_write"
	kiroNetworkPhaseRequestUpload       = "request_upload"
	kiroNetworkPhaseResponseHeaderWait  = "response_header_wait"
	kiroNetworkPhaseResponseHeaderRead  = "response_header_read"
	kiroNetworkPhaseResponseHeaderReady = "response_header_ready"
)

type kiroNetworkPhaseTiming struct {
	seen        bool
	active      int
	activeSince time.Time
	total       time.Duration
	lastStarted time.Time
	lastDone    time.Time
}

func (p *kiroNetworkPhaseTiming) start(now time.Time) {
	p.seen = true
	p.lastStarted = now
	if p.active == 0 {
		p.activeSince = now
	}
	p.active++
}

func (p *kiroNetworkPhaseTiming) done(now time.Time) {
	if !p.seen {
		p.seen = true
		p.lastStarted = now
	}
	p.lastDone = now
	if p.active <= 0 {
		return
	}
	p.active--
	if p.active == 0 {
		p.total += now.Sub(p.activeSince)
		p.activeSince = time.Time{}
	}
}

func (p *kiroNetworkPhaseTiming) duration(now time.Time) time.Duration {
	if p == nil || !p.seen {
		return -1
	}
	total := p.total
	if p.active > 0 {
		total += now.Sub(p.activeSince)
	}
	return total
}

type kiroNetworkTrace struct {
	mu sync.Mutex

	started             time.Time
	getConnAt           time.Time
	gotConnAt           time.Time
	wroteHeadersAt      time.Time
	wroteRequestAt      time.Time
	firstResponseByteAt time.Time
	responseHeaderAt    time.Time

	connectionObserved bool
	connectionReused   bool
	wroteRequest       bool
	wroteRequestError  bool
	lastFailedPhase    string
	dns                kiroNetworkPhaseTiming
	tcpConnect         kiroNetworkPhaseTiming
	proxyConnect       kiroNetworkPhaseTiming
	tlsHandshake       kiroNetworkPhaseTiming
}

type kiroNetworkTraceSnapshot struct {
	Phase                     string
	PhaseElapsed              time.Duration
	TotalElapsed              time.Duration
	ClientAcquire             time.Duration
	ConnectionWait            time.Duration
	DNS                       time.Duration
	TCPConnect                time.Duration
	ProxyConnect              time.Duration
	TLSHandshake              time.Duration
	RequestWrite              time.Duration
	RequestUpload             time.Duration
	ResponseHeaderWait        time.Duration
	ResponseHeaderRead        time.Duration
	ConnectionObserved        bool
	ConnectionReused          bool
	RequestWritten            bool
	RequestWriteError         bool
	FirstResponseByteReceived bool
	ResponseHeaderReceived    bool
}

func newKiroNetworkTrace() *kiroNetworkTrace {
	return &kiroNetworkTrace{started: time.Now()}
}

func (t *kiroNetworkTrace) attach(req *http.Request) *http.Request {
	if t == nil || req == nil {
		return req
	}
	ctx := tlsfingerprint.WithDialPhaseTrace(req.Context(), &tlsfingerprint.DialPhaseTrace{
		Start: func(phase tlsfingerprint.DialPhase) {
			t.phaseStart(string(phase))
		},
		Done: func(phase tlsfingerprint.DialPhase, err error) {
			t.phaseDone(string(phase), err)
		},
	})
	trace := &httptrace.ClientTrace{
		GetConn: func(string) {
			t.mu.Lock()
			if t.getConnAt.IsZero() {
				t.getConnAt = time.Now()
			}
			t.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			if t.gotConnAt.IsZero() {
				t.gotConnAt = time.Now()
			}
			t.connectionObserved = true
			t.connectionReused = info.Reused
			t.mu.Unlock()
		},
		DNSStart: func(httptrace.DNSStartInfo) {
			t.phaseStart(kiroNetworkPhaseDNS)
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			t.phaseDone(kiroNetworkPhaseDNS, info.Err)
		},
		ConnectStart: func(_, _ string) {
			t.phaseStart(kiroNetworkPhaseTCPConnect)
		},
		ConnectDone: func(_, _ string, err error) {
			t.phaseDone(kiroNetworkPhaseTCPConnect, err)
		},
		TLSHandshakeStart: func() {
			t.phaseStart(kiroNetworkPhaseTLSHandshake)
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			t.phaseDone(kiroNetworkPhaseTLSHandshake, err)
		},
		WroteHeaders: func() {
			t.mu.Lock()
			if t.wroteHeadersAt.IsZero() {
				t.wroteHeadersAt = time.Now()
			}
			t.mu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			t.mu.Lock()
			if t.wroteRequestAt.IsZero() {
				t.wroteRequestAt = time.Now()
			}
			t.wroteRequest = true
			t.wroteRequestError = t.wroteRequestError || info.Err != nil
			if info.Err != nil {
				t.lastFailedPhase = kiroNetworkPhaseRequestWrite
			}
			t.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			t.markFirstResponseByte()
		},
	}
	return req.WithContext(httptrace.WithClientTrace(ctx, trace))
}

func (t *kiroNetworkTrace) phaseStart(phase string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if timing := t.phaseTiming(phase); timing != nil {
		timing.start(time.Now())
	}
	t.mu.Unlock()
}

func (t *kiroNetworkTrace) phaseDone(phase string, err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if timing := t.phaseTiming(phase); timing != nil {
		timing.done(time.Now())
		if err != nil {
			t.lastFailedPhase = phase
		}
	}
	t.mu.Unlock()
}

func (t *kiroNetworkTrace) phaseTiming(phase string) *kiroNetworkPhaseTiming {
	switch phase {
	case kiroNetworkPhaseDNS:
		return &t.dns
	case kiroNetworkPhaseTCPConnect:
		return &t.tcpConnect
	case kiroNetworkPhaseProxyConnect:
		return &t.proxyConnect
	case kiroNetworkPhaseTLSHandshake:
		return &t.tlsHandshake
	default:
		return nil
	}
}

func (t *kiroNetworkTrace) markFirstResponseByte() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.firstResponseByteAt.IsZero() {
		t.firstResponseByteAt = time.Now()
	}
	t.mu.Unlock()
}

func (t *kiroNetworkTrace) markResponseHeader() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.responseHeaderAt.IsZero() {
		t.responseHeaderAt = time.Now()
	}
	t.mu.Unlock()
}

func (t *kiroNetworkTrace) snapshot() kiroNetworkTraceSnapshot {
	if t == nil {
		return kiroNetworkTraceSnapshot{}
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	phase, phaseElapsed := t.currentPhaseLocked(now)
	return kiroNetworkTraceSnapshot{
		Phase:                     phase,
		PhaseElapsed:              phaseElapsed,
		TotalElapsed:              now.Sub(t.started),
		ClientAcquire:             durationUntil(t.started, t.getConnAt, now),
		ConnectionWait:            durationUntil(t.getConnAt, t.gotConnAt, now),
		DNS:                       t.dns.duration(now),
		TCPConnect:                t.tcpConnect.duration(now),
		ProxyConnect:              t.proxyConnect.duration(now),
		TLSHandshake:              t.tlsHandshake.duration(now),
		RequestWrite:              durationUntil(t.gotConnAt, firstNonZeroTime(t.wroteHeadersAt, t.wroteRequestAt), now),
		RequestUpload:             durationUntil(t.wroteHeadersAt, t.wroteRequestAt, now),
		ResponseHeaderWait:        durationUntil(t.wroteRequestAt, firstNonZeroTime(t.firstResponseByteAt, t.responseHeaderAt), now),
		ResponseHeaderRead:        durationUntil(t.firstResponseByteAt, t.responseHeaderAt, now),
		ConnectionObserved:        t.connectionObserved,
		ConnectionReused:          t.connectionReused,
		RequestWritten:            t.wroteRequest,
		RequestWriteError:         t.wroteRequestError,
		FirstResponseByteReceived: !t.firstResponseByteAt.IsZero(),
		ResponseHeaderReceived:    !t.responseHeaderAt.IsZero(),
	}
}

func (t *kiroNetworkTrace) currentPhaseLocked(now time.Time) (string, time.Duration) {
	if !t.responseHeaderAt.IsZero() {
		return kiroNetworkPhaseResponseHeaderReady, 0
	}
	if !t.firstResponseByteAt.IsZero() {
		return kiroNetworkPhaseResponseHeaderRead, now.Sub(t.firstResponseByteAt)
	}
	if t.wroteRequestError {
		return kiroNetworkPhaseRequestWrite, now.Sub(t.wroteRequestAt)
	}
	if !t.wroteRequestAt.IsZero() {
		return kiroNetworkPhaseResponseHeaderWait, now.Sub(t.wroteRequestAt)
	}
	if !t.wroteHeadersAt.IsZero() {
		return kiroNetworkPhaseRequestUpload, now.Sub(t.wroteHeadersAt)
	}
	if !t.gotConnAt.IsZero() {
		return kiroNetworkPhaseRequestWrite, now.Sub(t.gotConnAt)
	}
	for _, phase := range []string{
		kiroNetworkPhaseTLSHandshake,
		kiroNetworkPhaseProxyConnect,
		kiroNetworkPhaseTCPConnect,
		kiroNetworkPhaseDNS,
	} {
		if timing := t.phaseTiming(phase); timing != nil && timing.active > 0 {
			return phase, now.Sub(timing.activeSince)
		}
	}
	if t.lastFailedPhase != "" {
		timing := t.phaseTiming(t.lastFailedPhase)
		if timing != nil && !timing.lastStarted.IsZero() {
			end := timing.lastDone
			if end.IsZero() {
				end = now
			}
			return t.lastFailedPhase, end.Sub(timing.lastStarted)
		}
		return t.lastFailedPhase, 0
	}
	if !t.getConnAt.IsZero() {
		return kiroNetworkPhaseConnectionAcquire, now.Sub(t.getConnAt)
	}
	return kiroNetworkPhaseClientAcquire, now.Sub(t.started)
}

func durationUntil(start, end, now time.Time) time.Duration {
	if start.IsZero() {
		return -1
	}
	if end.IsZero() {
		end = now
	}
	return end.Sub(start)
}

func firstNonZeroTime(times ...time.Time) time.Time {
	for _, candidate := range times {
		if !candidate.IsZero() {
			return candidate
		}
	}
	return time.Time{}
}

func (s kiroNetworkTraceSnapshot) zapFields() []zap.Field {
	fields := []zap.Field{
		zap.String("network_phase", s.Phase),
		zap.Int64("network_phase_ms", s.PhaseElapsed.Milliseconds()),
		zap.Int64("network_total_ms", s.TotalElapsed.Milliseconds()),
		zap.Bool("network_connection_observed", s.ConnectionObserved),
		zap.Bool("network_connection_reused", s.ConnectionReused),
		zap.Bool("network_request_written", s.RequestWritten),
		zap.Bool("network_request_write_error", s.RequestWriteError),
		zap.Bool("network_first_response_byte_received", s.FirstResponseByteReceived),
		zap.Bool("network_response_header_received", s.ResponseHeaderReceived),
	}
	return appendKiroNetworkDurationZapFields(fields, s)
}

func appendKiroNetworkDurationZapFields(fields []zap.Field, s kiroNetworkTraceSnapshot) []zap.Field {
	for _, item := range []struct {
		name     string
		duration time.Duration
	}{
		{"network_client_acquire_ms", s.ClientAcquire},
		{"network_connection_wait_ms", s.ConnectionWait},
		{"network_dns_ms", s.DNS},
		{"network_tcp_connect_ms", s.TCPConnect},
		{"network_proxy_connect_ms", s.ProxyConnect},
		{"network_tls_handshake_ms", s.TLSHandshake},
		{"network_request_write_ms", s.RequestWrite},
		{"network_request_upload_ms", s.RequestUpload},
		{"network_response_header_wait_ms", s.ResponseHeaderWait},
		{"network_response_header_read_ms", s.ResponseHeaderRead},
	} {
		if item.duration >= 0 {
			fields = append(fields, zap.Int64(item.name, item.duration.Milliseconds()))
		}
	}
	return fields
}

func (s kiroNetworkTraceSnapshot) slogArgs() []any {
	args := []any{
		"network_phase", s.Phase,
		"network_phase_ms", s.PhaseElapsed.Milliseconds(),
		"network_total_ms", s.TotalElapsed.Milliseconds(),
		"network_connection_observed", s.ConnectionObserved,
		"network_connection_reused", s.ConnectionReused,
		"network_request_written", s.RequestWritten,
		"network_request_write_error", s.RequestWriteError,
		"network_first_response_byte_received", s.FirstResponseByteReceived,
		"network_response_header_received", s.ResponseHeaderReceived,
	}
	for _, item := range []struct {
		name     string
		duration time.Duration
	}{
		{"network_client_acquire_ms", s.ClientAcquire},
		{"network_connection_wait_ms", s.ConnectionWait},
		{"network_dns_ms", s.DNS},
		{"network_tcp_connect_ms", s.TCPConnect},
		{"network_proxy_connect_ms", s.ProxyConnect},
		{"network_tls_handshake_ms", s.TLSHandshake},
		{"network_request_write_ms", s.RequestWrite},
		{"network_request_upload_ms", s.RequestUpload},
		{"network_response_header_wait_ms", s.ResponseHeaderWait},
		{"network_response_header_read_ms", s.ResponseHeaderRead},
	} {
		if item.duration >= 0 {
			args = append(args, item.name, item.duration.Milliseconds())
		}
	}
	return args
}

func kiroNetworkAttemptOutcome(err error, proxyEnabled bool) (string, string) {
	if err == nil {
		return "response_header_received", ""
	}
	var headerTimeout *kiroResponseHeaderTimeoutError
	if errors.As(err, &headerTimeout) {
		return "response_header_timeout", "response_header_timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled", "canceled"
	}
	return "transport_error", classifyKiroEndpointNetworkError(err, proxyEnabled)
}
