package service

import (
	"net/http"
	"net/http/httptest"
	"os"
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
