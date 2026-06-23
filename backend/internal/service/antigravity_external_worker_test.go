package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAntigravityExternalWorkerBinaryPath_PrefersBoringcrypto(t *testing.T) {
	dataDir := t.TempDir()
	binDir := filepath.Join(dataDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	normal := filepath.Join(binDir, antigravityExternalWorkerBinaryName)
	boring := filepath.Join(binDir, antigravityExternalWorkerBoringBinaryName)
	require.NoError(t, os.WriteFile(normal, []byte("x"), 0o755))
	require.NoError(t, os.WriteFile(boring, []byte("x"), 0o755))

	t.Setenv("DATA_DIR", dataDir)
	t.Setenv(antigravityExternalWorkerBinEnv, "")
	t.Setenv(antigravityExternalWorkerBoringBinEnv, "")
	t.Setenv(antigravityExternalWorkerPreferBoringEnv, "true")

	require.Equal(t, boring, antigravityExternalWorkerBinaryPath())
}

func TestAntigravityExternalWorkerBinaryPath_FallsBackToNormalWhenBoringDisabled(t *testing.T) {
	dataDir := t.TempDir()
	binDir := filepath.Join(dataDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	normal := filepath.Join(binDir, antigravityExternalWorkerBinaryName)
	boring := filepath.Join(binDir, antigravityExternalWorkerBoringBinaryName)
	require.NoError(t, os.WriteFile(normal, []byte("x"), 0o755))
	require.NoError(t, os.WriteFile(boring, []byte("x"), 0o755))

	t.Setenv("DATA_DIR", dataDir)
	t.Setenv(antigravityExternalWorkerBinEnv, "")
	t.Setenv(antigravityExternalWorkerBoringBinEnv, "")
	t.Setenv(antigravityExternalWorkerPreferBoringEnv, "false")

	require.Equal(t, normal, antigravityExternalWorkerBinaryPath())
}

func TestAntigravityExternalWorkerExecutor_PingLocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	executor := &antigravityExternalWorkerExecutor{
		addr:   strings.TrimPrefix(server.URL, "http://"),
		client: &http.Client{},
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	require.NoError(t, executor.pingLocked())
}

func TestAntigravityExternalWorkerExecutor_NormalizesSOCKS5ProxyPayload(t *testing.T) {
	payloadCh := make(chan antigravityExternalExecuteRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		if r.URL.Path == "/execute" {
			var payload antigravityExternalExecuteRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			payloadCh <- payload
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cmd := exec.Command("sleep", "30")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	executor := &antigravityExternalWorkerExecutor{
		addr:   strings.TrimPrefix(server.URL, "http://"),
		client: &http.Client{},
		cmd:    cmd,
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", strings.NewReader(`{"x":1}`))
	require.NoError(t, err)

	resp, err := executor.do(req, "socks5://proxy.example.com:1080", &Account{Concurrency: 3}, nil, 30)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))

	select {
	case payload := <-payloadCh:
		require.Equal(t, "socks5h://proxy.example.com:1080", payload.ProxyURL)
		require.Equal(t, 3, payload.AccountConcurrency)
	default:
		require.Fail(t, "expected execute payload")
	}
}

func TestAntigravityExternalWorkerExecutor_InvalidProxyFailsBeforeStart(t *testing.T) {
	executor := &antigravityExternalWorkerExecutor{}
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", strings.NewReader(`{"x":1}`))
	require.NoError(t, err)

	resp, err := executor.do(req, "://bad-proxy-url", &Account{Concurrency: 3}, nil, 30)
	require.Nil(t, resp)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid proxy URL")
	require.Empty(t, executor.addr)
	require.Nil(t, executor.client)
	require.Nil(t, executor.cmd)
}
