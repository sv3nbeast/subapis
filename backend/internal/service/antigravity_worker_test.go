package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/stretchr/testify/require"
)

func TestAntigravityWorkerManager_GetOrCreate_IsPerAccount(t *testing.T) {
	manager := newAntigravityWorkerManager()

	now := time.Now()
	worker1a := manager.getOrCreate(101, now)
	worker1b := manager.getOrCreate(101, now)
	worker2 := manager.getOrCreate(202, now)

	require.Same(t, worker1a, worker1b)
	require.NotSame(t, worker1a, worker2)
}

func TestAntigravityWorkerState_BootstrapClientReusedPerAccount(t *testing.T) {
	worker := newAntigravityWorkerState(101)
	factoryCalls := 0
	factory := func(proxyURL string) (antigravityBootstrapClient, error) {
		factoryCalls++
		return &antigravityBootstrapClientStub{
			loadResp: &antigravity.LoadCodeAssistResponse{},
		}, nil
	}

	client1, err := worker.bootstrapClientFor(factory, "http://proxy-a:8080")
	require.NoError(t, err)
	client2, err := worker.bootstrapClientFor(factory, "http://proxy-a:8080")
	require.NoError(t, err)
	client3, err := worker.bootstrapClientFor(factory, "http://proxy-b:8080")
	require.NoError(t, err)

	require.Same(t, client1, client2)
	require.NotSame(t, client1, client3)
	require.Equal(t, 2, factoryCalls)
}

func TestAntigravityGatewayService_DoAntigravityUpstreamRequest_UsesWorkerTransport(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`ok`))
	}))
	defer upstream.Close()

	req, err := http.NewRequest(http.MethodPost, upstream.URL, bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)

	svc := &AntigravityGatewayService{
		httpUpstream:                  &httpUpstreamStub{err: io.EOF},
		workerManager:                 newAntigravityWorkerManager(),
		useAntigravityWorkerTransport: true,
	}
	account := &Account{
		ID:          1,
		Platform:    PlatformAntigravity,
		Concurrency: 2,
	}

	resp, err := svc.doAntigravityUpstreamRequest(req, "", account, nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))

	worker := svc.antigravityWorker(account)
	require.NotNil(t, worker)
	executor := worker.requestExecutor()
	require.NotNil(t, executor)
	require.NotNil(t, executor.client)

	req2, err := http.NewRequest(http.MethodPost, upstream.URL, bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	resp2, err := svc.doAntigravityUpstreamRequest(req2, "", account, nil)
	require.NoError(t, err)
	require.NotNil(t, resp2)
	defer func() { _ = resp2.Body.Close() }()
	require.Same(t, executor.client, worker.requestExecutor().client)
}

func TestAntigravityWorkerManager_StopClearsWorkers(t *testing.T) {
	manager := newAntigravityWorkerManager()
	now := time.Now()
	_ = manager.getOrCreate(101, now)
	_ = manager.getOrCreate(202, now)
	require.Len(t, manager.workers, 2)

	manager.stop()
	require.Len(t, manager.workers, 0)
}
