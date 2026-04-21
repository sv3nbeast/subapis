package service

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	antigravityWorkerIdleTTL             = 24 * time.Hour
	antigravityWorkerCleanupInterval     = time.Hour
	antigravityWorkerSignatureFilePrefix = "antigravity_tool_signatures_account_"
)

type antigravityWorkerState struct {
	accountID int64

	mu sync.Mutex

	sessionID            string
	requestLineage       *antigravityRequestLineageStore
	toolSignatureCache   *antigravityToolSignatureCache
	bootstrapProbeCache  *antigravityBootstrapCache
	bootstrapClient      antigravityBootstrapClient
	bootstrapClientProxy string
	requestExecutorImpl  *antigravityWorkerHTTPExecutor
	externalExecutor     *antigravityExternalWorkerExecutor
	lastUsedAt           time.Time
}

func newAntigravityWorkerState(accountID int64) *antigravityWorkerState {
	return &antigravityWorkerState{
		accountID:           accountID,
		requestLineage:      newAntigravityRequestLineageStore(),
		toolSignatureCache:  newAntigravityToolSignatureCacheWithPath(resolveAntigravityWorkerSignatureCachePath(accountID)),
		bootstrapProbeCache: newAntigravityBootstrapCache(),
		lastUsedAt:          time.Now(),
	}
}

func (w *antigravityWorkerState) touch(now time.Time) {
	if w == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	w.mu.Lock()
	w.lastUsedAt = now
	w.mu.Unlock()
}

func (w *antigravityWorkerState) getSessionID() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(w.sessionID)
}

func (w *antigravityWorkerState) setSessionID(sessionID string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.sessionID = strings.TrimSpace(sessionID)
	w.lastUsedAt = time.Now()
	w.mu.Unlock()
}

func (w *antigravityWorkerState) nextRequestID(conversationKey string, now time.Time) string {
	if w == nil {
		return fmt.Sprintf("agent/%d/%s/%d", now.UnixMilli(), antigravityLineageFallbackKey, 1)
	}
	w.touch(now)
	return w.requestLineage.nextRequestID(w.accountID, conversationKey, now)
}

func (w *antigravityWorkerState) toolSignatures(conversationKey string, now time.Time) map[string]string {
	if w == nil {
		return nil
	}
	w.touch(now)
	return w.toolSignatureCache.snapshot(w.accountID, conversationKey, now)
}

func (w *antigravityWorkerState) rememberToolSignatures(conversationKey string, signatures map[string]string, now time.Time) {
	if w == nil {
		return
	}
	w.touch(now)
	w.toolSignatureCache.remember(w.accountID, conversationKey, signatures, now)
}

func (w *antigravityWorkerState) bootstrapSnapshot(now time.Time) (*antigravityBootstrapSnapshot, bool) {
	if w == nil {
		return nil, false
	}
	w.touch(now)
	return w.bootstrapProbeCache.getSnapshot(w.accountID, now)
}

func (w *antigravityWorkerState) tryStartBootstrap(now time.Time) bool {
	if w == nil {
		return false
	}
	w.touch(now)
	return w.bootstrapProbeCache.tryStart(w.accountID, now)
}

func (w *antigravityWorkerState) finishBootstrap(snapshot *antigravityBootstrapSnapshot, now time.Time) {
	if w == nil {
		return
	}
	w.touch(now)
	w.bootstrapProbeCache.finish(w.accountID, snapshot, now)
}

func (w *antigravityWorkerState) bootstrapClientFor(factory antigravityBootstrapClientFactory, proxyURL string) (antigravityBootstrapClient, error) {
	if w == nil {
		return nil, fmt.Errorf("worker is nil")
	}
	if factory == nil {
		factory = defaultAntigravityBootstrapClientFactory
	}

	proxyURL = strings.TrimSpace(proxyURL)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastUsedAt = time.Now()

	if w.bootstrapClient != nil && w.bootstrapClientProxy == proxyURL {
		return w.bootstrapClient, nil
	}

	client, err := factory(proxyURL)
	if err != nil {
		return nil, err
	}
	w.bootstrapClient = client
	w.bootstrapClientProxy = proxyURL
	return client, nil
}

func (w *antigravityWorkerState) shutdown() {
	if w == nil {
		return
	}

	w.mu.Lock()
	executor := w.requestExecutorImpl
	external := w.externalExecutor
	w.requestExecutorImpl = nil
	w.externalExecutor = nil
	w.bootstrapClient = nil
	w.bootstrapClientProxy = ""
	w.mu.Unlock()

	if executor != nil && executor.client != nil {
		executor.client.CloseIdleConnections()
	}
	if external != nil {
		external.reset()
	}
}

type antigravityWorkerManager struct {
	mu            sync.Mutex
	workers       map[int64]*antigravityWorkerState
	lastCleanedAt time.Time
}

func newAntigravityWorkerManager() *antigravityWorkerManager {
	return &antigravityWorkerManager{
		workers: make(map[int64]*antigravityWorkerState),
	}
}

func (m *antigravityWorkerManager) getOrCreate(accountID int64, now time.Time) *antigravityWorkerState {
	if m == nil {
		return newAntigravityWorkerState(accountID)
	}
	if now.IsZero() {
		now = time.Now()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lastCleanedAt.IsZero() || now.Sub(m.lastCleanedAt) >= antigravityWorkerCleanupInterval {
		expireBefore := now.Add(-antigravityWorkerIdleTTL)
		for id, worker := range m.workers {
			if worker == nil {
				delete(m.workers, id)
				continue
			}
			worker.mu.Lock()
			lastUsedAt := worker.lastUsedAt
			worker.mu.Unlock()
			if lastUsedAt.Before(expireBefore) {
				worker.shutdown()
				delete(m.workers, id)
			}
		}
		m.lastCleanedAt = now
	}

	worker := m.workers[accountID]
	if worker == nil {
		worker = newAntigravityWorkerState(accountID)
		m.workers[accountID] = worker
	}
	worker.touch(now)
	return worker
}

func (m *antigravityWorkerManager) stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, worker := range m.workers {
		if worker != nil {
			worker.shutdown()
		}
		delete(m.workers, id)
	}
}

func resolveAntigravityWorkerSignatureCachePath(accountID int64) string {
	base := resolveAntigravityToolSignatureCachePath()
	dir := filepath.Dir(base)
	return filepath.Join(dir, fmt.Sprintf("%s%d.json", antigravityWorkerSignatureFilePrefix, accountID))
}

func (s *AntigravityGatewayService) antigravityWorker(account *Account) *antigravityWorkerState {
	if s == nil {
		return nil
	}

	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}

	s.requestIdentityMu.Lock()
	if s.workerManager == nil {
		s.workerManager = newAntigravityWorkerManager()
	}
	manager := s.workerManager
	s.requestIdentityMu.Unlock()
	return manager.getOrCreate(accountID, time.Now())
}

func (s *AntigravityGatewayService) Stop() {
	if s == nil {
		return
	}
	s.requestIdentityMu.Lock()
	manager := s.workerManager
	s.workerManager = nil
	s.requestIdentityMu.Unlock()
	if manager != nil {
		manager.stop()
	}
}
