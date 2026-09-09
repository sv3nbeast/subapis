package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type runtimeGateExecutor struct {
	trackingAgentExecutor
	entered   chan struct{}
	release   chan struct{}
	available atomic.Bool
}

func (e *runtimeGateExecutor) Available() bool { return e.available.Load() }
func (e *runtimeGateExecutor) WaitReady(ctx context.Context) error {
	close(e.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.release:
		e.available.Store(true)
		return nil
	}
}
func TestWebAgentDoesNotClaimJobsBeforeReadiness(t *testing.T) {
	repo := agentWorkerFixture()
	executor := &runtimeGateExecutor{entered: make(chan struct{}), release: make(chan struct{})}
	worker := NewWebAgentService(repo, agentTestChat(), executor)
	worker.Start()
	defer worker.Stop()
	<-executor.entered
	require.False(t, worker.Ready(context.Background()))
	repo.mu.Lock()
	require.False(t, repo.claimed)
	repo.mu.Unlock()
	worker.Stop()
	require.False(t, worker.Ready(context.Background()))
	repo.mu.Lock()
	require.False(t, repo.claimed)
	repo.mu.Unlock()
}
func TestWebAgentRuntimeDoesNotProbeBusyRenderer(t *testing.T) {
	calls := 0
	r := &webAgentRuntime{busy: true, probe: func(context.Context) error { calls++; return errors.New("busy worker must not be probed") }}
	r.available.Store(true)
	require.NoError(t, r.check(context.Background()))
	require.Zero(t, calls)
	require.True(t, r.Available())
	r.busy = false
	require.Error(t, r.check(context.Background()))
	require.False(t, r.Available())
}
func TestWebAgentReadinessRequiresAuthenticationAndProtocol(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"status":"ok","protocol_version":1,"kinds":["document","slides","spreadsheet"]}`))
	}))
	defer s.Close()
	require.Error(t, probeAgentHealth(context.Background(), s.Client(), s.URL+"/health", "wrong-token"))
	require.NoError(t, probeAgentHealth(context.Background(), s.Client(), s.URL+"/health", "valid-token"))
}

type runtimeConfigurationRepo struct {
	agentChatStub
	*agentTaskStub
	WebAgentStorageRepository
	mu       sync.Mutex
	identity string
}

func (r *runtimeConfigurationRepo) RegisterArtifactStore(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.identity != "" && r.identity != id {
		return ErrWebAgentStorageIdentity
	}
	r.identity = id
	return nil
}
func (r *runtimeConfigurationRepo) CollectArtifactStorage(context.Context, func(*WebAgentBlobStage) error) (int, error) {
	return 0, nil
}
func TestWebAgentApplicationConfigurationStartsAndStopsRuntime(t *testing.T) {
	var healthy atomic.Bool
	token := strings.Repeat("x", 32)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(503)
			return
		}
		if r.Header.Get("Authorization") == "Bearer "+token {
			w.Write([]byte(`{"status":"ok","protocol_version":1,"kinds":["document","slides","spreadsheet"]}`))
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer s.Close()
	u, err := url.Parse(s.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	repo := &runtimeConfigurationRepo{agentTaskStub: &agentTaskStub{}}
	chat := NewWebChatService(repo, agentPlannerKeyRepo{}, &agentPlannerKeys{}, agentCatalogStub{}, agentRuntimeStub{})
	err = chat.ConfigureAgent(config.WebAgentConfig{Enabled: true, RendererURL: s.URL, RendererToken: token, StoragePath: filepath.Join(t.TempDir(), "private-store")}, port)
	require.NoError(t, err)
	defer chat.StopAgent()
	require.False(t, chat.Agent().Ready(context.Background()))
	healthy.Store(true)
	require.Eventually(t, func() bool { return chat.Agent().Ready(context.Background()) }, 3*time.Second, 10*time.Millisecond)
	require.NotNil(t, chat.Artifacts().store)
	chat.StopAgent()
	require.False(t, chat.Agent().Ready(context.Background()))
}
func TestWebAgentInvalidConfigurationDoesNotDisableOrdinaryChat(t *testing.T) {
	cfg := &config.Config{WebAgent: config.WebAgentConfig{Enabled: true}}
	chat := ProvideWebChatService(agentChatStub{}, agentPlannerKeyRepo{}, &agentPlannerKeys{}, agentCatalogStub{}, agentRuntimeStub{}, nil, cfg)
	require.True(t, chat.FeatureEnabled(context.Background()))
	require.False(t, chat.Agent().Ready(context.Background()))
}
