package service

import (
	"errors"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKiroNetworkTraceTracksResponseHeaderPhases(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://runtime.us-east-1.kiro.dev/generateAssistantResponse", nil)
	require.NoError(t, err)

	observation := newKiroNetworkTrace()
	request = observation.attach(request)
	trace := httptrace.ContextClientTrace(request.Context())
	require.NotNil(t, trace)

	trace.GetConn(request.URL.Host)
	trace.GotConn(httptrace.GotConnInfo{Reused: true, WasIdle: true})
	trace.WroteHeaders()
	trace.WroteRequest(httptrace.WroteRequestInfo{})

	waiting := observation.snapshot()
	require.Equal(t, kiroNetworkPhaseResponseHeaderWait, waiting.Phase)
	require.True(t, waiting.ConnectionObserved)
	require.True(t, waiting.ConnectionReused)
	require.True(t, waiting.RequestWritten)
	require.False(t, waiting.RequestWriteError)
	require.False(t, waiting.ResponseHeaderReceived)
	require.GreaterOrEqual(t, waiting.ClientAcquire, time.Duration(0))
	require.GreaterOrEqual(t, waiting.ConnectionWait, time.Duration(0))
	require.GreaterOrEqual(t, waiting.RequestWrite, time.Duration(0))
	require.GreaterOrEqual(t, waiting.RequestUpload, time.Duration(0))
	require.GreaterOrEqual(t, waiting.ResponseHeaderWait, time.Duration(0))

	trace.GotFirstResponseByte()
	partial := observation.snapshot()
	require.Equal(t, kiroNetworkPhaseResponseHeaderRead, partial.Phase)
	require.True(t, partial.FirstResponseByteReceived)
	require.False(t, partial.ResponseHeaderReceived)
	require.GreaterOrEqual(t, partial.ResponseHeaderRead, time.Duration(0))

	observation.markResponseHeader()
	completed := observation.snapshot()
	require.Equal(t, kiroNetworkPhaseResponseHeaderReady, completed.Phase)
	require.True(t, completed.FirstResponseByteReceived)
	require.True(t, completed.ResponseHeaderReceived)
}

func TestKiroNetworkTracePreservesExistingWriteObserver(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://runtime.us-east-1.kiro.dev/generateAssistantResponse", nil)
	require.NoError(t, err)
	existingCalled := false
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) {
			existingCalled = true
		},
	}))

	observation := newKiroNetworkTrace()
	request = observation.attach(request)
	httptrace.ContextClientTrace(request.Context()).WroteRequest(httptrace.WroteRequestInfo{})

	require.True(t, existingCalled, "network observation must preserve duplicate-prevention write tracking")
	require.True(t, observation.snapshot().RequestWritten)
}

func TestKiroNetworkTraceClassifiesRequestWriteFailure(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://runtime.us-east-1.kiro.dev/generateAssistantResponse", nil)
	require.NoError(t, err)
	observation := newKiroNetworkTrace()
	request = observation.attach(request)

	httptrace.ContextClientTrace(request.Context()).WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("write failed")})
	snapshot := observation.snapshot()

	require.Equal(t, kiroNetworkPhaseRequestWrite, snapshot.Phase)
	require.True(t, snapshot.RequestWritten)
	require.True(t, snapshot.RequestWriteError)
}

func TestKiroNetworkTraceIdentifiesActiveAndFailedProxyConnect(t *testing.T) {
	observation := newKiroNetworkTrace()
	observation.phaseStart(kiroNetworkPhaseProxyConnect)

	active := observation.snapshot()
	require.Equal(t, kiroNetworkPhaseProxyConnect, active.Phase)
	require.GreaterOrEqual(t, active.ProxyConnect, time.Duration(0))

	observation.phaseDone(kiroNetworkPhaseProxyConnect, errors.New("proxy unavailable"))
	failed := observation.snapshot()
	require.Equal(t, kiroNetworkPhaseProxyConnect, failed.Phase)
	require.GreaterOrEqual(t, failed.ProxyConnect, time.Duration(0))
}

func TestKiroNetworkTraceCallbacksAreConcurrentSafe(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://runtime.us-east-1.kiro.dev/generateAssistantResponse", nil)
	require.NoError(t, err)
	observation := newKiroNetworkTrace()
	request = observation.attach(request)
	trace := httptrace.ContextClientTrace(request.Context())

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			trace.ConnectStart("tcp", request.URL.Host)
			trace.ConnectDone("tcp", request.URL.Host, nil)
			trace.WroteRequest(httptrace.WroteRequestInfo{})
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = observation.snapshot()
			}
		}()
	}
	wg.Wait()
	require.True(t, observation.snapshot().RequestWritten)
}

func TestKiroHeaderUnresponsiveScopeDoesNotContainProxyCredentials(t *testing.T) {
	proxyID := int64(68)
	account := &Account{
		ID:       2004,
		Platform: PlatformKiro,
		ProxyID:  &proxyID,
		Proxy: &Proxy{
			ID:       proxyID,
			Protocol: "http",
			Host:     "proxy.example",
			Port:     8080,
			Username: "sensitive-user",
			Password: "sensitive-password",
		},
		Credentials: map[string]any{"refresh_token": "refresh-secret"},
	}

	scope := kiroHeaderUnresponsiveScope(account, kiroEndpointConfig{
		Name: "KiroRuntime",
		URL:  "https://runtime.us-east-1.kiro.dev/generateAssistantResponse",
	})

	require.Contains(t, scope, "host:runtime.us-east-1.kiro.dev")
	require.Contains(t, scope, "proxy_id:68")
	require.Contains(t, scope, "proxy_protocol:http")
	for _, secret := range []string{"sensitive-user", "sensitive-password", "refresh-secret", "proxy.example"} {
		require.False(t, strings.Contains(scope, secret), "scope leaked %q: %s", secret, scope)
	}
}
