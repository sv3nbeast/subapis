package tlsfingerprint

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestHTTPProxyDialerReportsTCPAndProxyConnectPhases(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		request, readErr := http.ReadRequest(bufio.NewReader(conn))
		if readErr != nil {
			serverErr <- readErr
			return
		}
		_ = request.Body.Close()
		_, writeErr := fmt.Fprint(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		serverErr <- writeErr
	}()

	proxyURL := &url.URL{Scheme: "http", Host: listener.Addr().String()}
	dialer := NewHTTPProxyDialer(&Profile{Name: "phase-trace-test"}, proxyURL)
	type phaseEvent struct {
		kind   string
		phase  DialPhase
		failed bool
	}
	var mu sync.Mutex
	events := make([]phaseEvent, 0, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = WithDialPhaseTrace(ctx, &DialPhaseTrace{
		Start: func(phase DialPhase) {
			mu.Lock()
			events = append(events, phaseEvent{kind: "start", phase: phase})
			mu.Unlock()
		},
		Done: func(phase DialPhase, phaseErr error) {
			mu.Lock()
			events = append(events, phaseEvent{kind: "done", phase: phase, failed: phaseErr != nil})
			mu.Unlock()
		},
	})

	conn, err := dialer.DialTLSContext(ctx, "tcp", "runtime.us-east-1.kiro.dev:443")
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("expected proxy CONNECT failure")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("proxy server: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []phaseEvent{
		{kind: "start", phase: DialPhaseTCPConnect},
		{kind: "done", phase: DialPhaseTCPConnect},
		{kind: "start", phase: DialPhaseProxyConnect},
		{kind: "done", phase: DialPhaseProxyConnect, failed: true},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = %#v, want %#v", i, events[i], want[i])
		}
	}
}

func TestTLSHandshakeReportsPhaseFailure(t *testing.T) {
	client, server := net.Pipe()
	_ = server.Close()

	type phaseEvent struct {
		kind   string
		phase  DialPhase
		failed bool
	}
	events := make([]phaseEvent, 0, 2)
	ctx := WithDialPhaseTrace(context.Background(), &DialPhaseTrace{
		Start: func(phase DialPhase) {
			events = append(events, phaseEvent{kind: "start", phase: phase})
		},
		Done: func(phase DialPhase, phaseErr error) {
			events = append(events, phaseEvent{kind: "done", phase: phase, failed: phaseErr != nil})
		},
	})

	conn, err := performTLSHandshake(ctx, client, &Profile{Name: "phase-trace-test"}, "runtime.us-east-1.kiro.dev:443")
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("expected TLS handshake failure")
	}
	want := []phaseEvent{
		{kind: "start", phase: DialPhaseTLSHandshake},
		{kind: "done", phase: DialPhaseTLSHandshake, failed: true},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = %#v, want %#v", i, events[i], want[i])
		}
	}
}
