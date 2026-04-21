package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	antigravityBootstrapProbeTTL          = 15 * time.Minute
	antigravityBootstrapAsyncTimeout      = 5 * time.Second
	antigravityBootstrapSyncTimeout       = 1500 * time.Millisecond
	antigravityBootstrapCleanupInterval   = time.Hour
	antigravityBootstrapProbeRetentionTTL = 24 * time.Hour
)

var errAntigravityBootstrapNoDefaultTier = fmt.Errorf("loadCodeAssist 未返回可用的默认 tier")

type antigravityBootstrapClient interface {
	LoadCodeAssist(ctx context.Context, accessToken string) (*antigravity.LoadCodeAssistResponse, map[string]any, error)
	FetchAvailableModels(ctx context.Context, accessToken, projectID string) (*antigravity.FetchAvailableModelsResponse, map[string]any, error)
	FetchUserInfo(ctx context.Context, accessToken, projectID string) (*antigravity.FetchUserInfoResponse, error)
	OnboardUser(ctx context.Context, accessToken, tierID string) (string, error)
}

type antigravityBootstrapClientFactory func(proxyURL string) (antigravityBootstrapClient, error)

type antigravityBootstrapSnapshot struct {
	ProjectID  string
	Tier       string
	RegionCode string
	IsPrivate  bool
	ModelCount int
	UpdatedAt  time.Time
	LastError  string
}

type antigravityBootstrapCacheEntry struct {
	Snapshot *antigravityBootstrapSnapshot
	Inflight bool
}

type antigravityBootstrapCache struct {
	mu            sync.Mutex
	entries       map[int64]*antigravityBootstrapCacheEntry
	lastCleanedAt time.Time
}

func newAntigravityBootstrapCache() *antigravityBootstrapCache {
	return &antigravityBootstrapCache{
		entries: make(map[int64]*antigravityBootstrapCacheEntry),
	}
}

func (c *antigravityBootstrapCache) getSnapshot(accountID int64, now time.Time) (*antigravityBootstrapSnapshot, bool) {
	if c == nil {
		return nil, false
	}
	if now.IsZero() {
		now = time.Now()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupIfNeededLocked(now)

	entry := c.entries[accountID]
	if entry == nil || entry.Snapshot == nil {
		return nil, false
	}

	snapshot := *entry.Snapshot
	return &snapshot, now.Sub(snapshot.UpdatedAt) < antigravityBootstrapProbeTTL
}

func (c *antigravityBootstrapCache) tryStart(accountID int64, now time.Time) bool {
	if c == nil {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupIfNeededLocked(now)

	entry := c.entries[accountID]
	if entry == nil {
		entry = &antigravityBootstrapCacheEntry{}
		c.entries[accountID] = entry
	}
	if entry.Inflight {
		return false
	}
	entry.Inflight = true
	return true
}

func (c *antigravityBootstrapCache) finish(accountID int64, snapshot *antigravityBootstrapSnapshot, now time.Time) {
	if c == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.entries[accountID]
	if entry == nil {
		entry = &antigravityBootstrapCacheEntry{}
		c.entries[accountID] = entry
	}
	entry.Inflight = false
	if snapshot != nil {
		snapshot.UpdatedAt = now
		entry.Snapshot = snapshot
	}
}

func (c *antigravityBootstrapCache) cleanupIfNeededLocked(now time.Time) {
	if c.lastCleanedAt.IsZero() || now.Sub(c.lastCleanedAt) >= antigravityBootstrapCleanupInterval {
		expireBefore := now.Add(-antigravityBootstrapProbeRetentionTTL)
		for accountID, entry := range c.entries {
			if entry == nil {
				delete(c.entries, accountID)
				continue
			}
			if entry.Inflight {
				continue
			}
			if entry.Snapshot == nil || entry.Snapshot.UpdatedAt.Before(expireBefore) {
				delete(c.entries, accountID)
			}
		}
		c.lastCleanedAt = now
	}
}

type antigravityClientAdapter struct {
	inner *antigravity.Client
}

func (a *antigravityClientAdapter) LoadCodeAssist(ctx context.Context, accessToken string) (*antigravity.LoadCodeAssistResponse, map[string]any, error) {
	return a.inner.LoadCodeAssist(ctx, accessToken)
}

func (a *antigravityClientAdapter) FetchAvailableModels(ctx context.Context, accessToken, projectID string) (*antigravity.FetchAvailableModelsResponse, map[string]any, error) {
	return a.inner.FetchAvailableModels(ctx, accessToken, projectID)
}

func (a *antigravityClientAdapter) FetchUserInfo(ctx context.Context, accessToken, projectID string) (*antigravity.FetchUserInfoResponse, error) {
	return a.inner.FetchUserInfo(ctx, accessToken, projectID)
}

func (a *antigravityClientAdapter) OnboardUser(ctx context.Context, accessToken, tierID string) (string, error) {
	return a.inner.OnboardUser(ctx, accessToken, tierID)
}

func defaultAntigravityBootstrapClientFactory(proxyURL string) (antigravityBootstrapClient, error) {
	client, err := antigravity.NewClient(proxyURL)
	if err != nil {
		return nil, err
	}
	return &antigravityClientAdapter{inner: client}, nil
}

func (s *AntigravityGatewayService) ensureAntigravityBootstrapProbe(ctx context.Context, account *Account, accessToken, proxyURL string) string {
	if s == nil || account == nil || strings.TrimSpace(accessToken) == "" {
		return ""
	}

	worker := s.antigravityWorker(account)
	projectID := strings.TrimSpace(account.GetCredential("project_id"))
	now := time.Now()
	if worker != nil {
		if snapshot, fresh := worker.bootstrapSnapshot(now); fresh {
			if projectID == "" && strings.TrimSpace(snapshot.ProjectID) != "" {
				projectID = s.persistBootstrapProjectID(ctx, account, strings.TrimSpace(snapshot.ProjectID))
			}
			return projectID
		}
	} else if snapshot, fresh := s.bootstrapProbeCache.getSnapshot(account.ID, now); fresh {
		if projectID == "" && strings.TrimSpace(snapshot.ProjectID) != "" {
			projectID = s.persistBootstrapProjectID(ctx, account, strings.TrimSpace(snapshot.ProjectID))
		}
		return projectID
	}

	if worker != nil {
		if !worker.tryStartBootstrap(now) {
			return projectID
		}
	} else if !s.bootstrapProbeCache.tryStart(account.ID, now) {
		return projectID
	}

	if projectID == "" {
		syncCtx, cancel := context.WithTimeout(ctx, antigravityBootstrapSyncTimeout)
		defer cancel()
		snapshot := s.runAntigravityBootstrapProbe(syncCtx, account, accessToken, proxyURL, projectID)
		if worker != nil {
			worker.finishBootstrap(snapshot, time.Now())
		} else {
			s.bootstrapProbeCache.finish(account.ID, snapshot, time.Now())
		}
		if snapshot != nil && strings.TrimSpace(snapshot.ProjectID) != "" {
			return strings.TrimSpace(snapshot.ProjectID)
		}
		return strings.TrimSpace(account.GetCredential("project_id"))
	}

	go func(accountRef *Account, token, proxy, currentProjectID string, workerRef *antigravityWorkerState) {
		bgCtx, cancel := context.WithTimeout(context.Background(), antigravityBootstrapAsyncTimeout)
		defer cancel()
		snapshot := s.runAntigravityBootstrapProbe(bgCtx, accountRef, token, proxy, currentProjectID)
		if workerRef != nil {
			workerRef.finishBootstrap(snapshot, time.Now())
		} else {
			s.bootstrapProbeCache.finish(accountRef.ID, snapshot, time.Now())
		}
	}(account, accessToken, proxyURL, projectID, worker)

	return projectID
}

func (s *AntigravityGatewayService) runAntigravityBootstrapProbe(ctx context.Context, account *Account, accessToken, proxyURL, currentProjectID string) *antigravityBootstrapSnapshot {
	if s == nil || account == nil {
		return nil
	}

	worker := s.antigravityWorker(account)
	factory := s.newAntigravityClient
	if factory == nil {
		factory = defaultAntigravityBootstrapClientFactory
	}
	var client antigravityBootstrapClient
	var err error
	if worker != nil {
		client, err = worker.bootstrapClientFor(factory, proxyURL)
	} else {
		client, err = factory(proxyURL)
	}
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "antigravity bootstrap client init failed: account=%d err=%v", account.ID, err)
		return &antigravityBootstrapSnapshot{ProjectID: currentProjectID, LastError: err.Error()}
	}

	snapshot := &antigravityBootstrapSnapshot{
		ProjectID: strings.TrimSpace(currentProjectID),
	}

	loadResp, loadRaw, loadErr := client.LoadCodeAssist(ctx, accessToken)
	if loadErr != nil {
		snapshot.LastError = loadErr.Error()
		logger.LegacyPrintf("service.antigravity_gateway", "antigravity bootstrap loadCodeAssist failed: account=%d err=%v", account.ID, loadErr)
		return snapshot
	}
	if loadResp != nil {
		snapshot.Tier = strings.TrimSpace(loadResp.GetTier())
		if snapshot.ProjectID == "" {
			snapshot.ProjectID = strings.TrimSpace(loadResp.CloudAICompanionProject)
		}
	}

	if snapshot.ProjectID == "" && len(loadRaw) > 0 {
		if onboardProjectID, onboardErr := tryOnboardProjectIDWithBootstrapClient(ctx, client, accessToken, loadRaw); onboardErr == nil && strings.TrimSpace(onboardProjectID) != "" {
			snapshot.ProjectID = strings.TrimSpace(onboardProjectID)
		} else if onboardErr != nil {
			snapshot.LastError = onboardErr.Error()
			logger.LegacyPrintf("service.antigravity_gateway", "antigravity bootstrap onboardUser failed: account=%d err=%v", account.ID, onboardErr)
		}
	}

	if snapshot.ProjectID != "" {
		snapshot.ProjectID = s.persistBootstrapProjectID(ctx, account, snapshot.ProjectID)
	}
	if snapshot.ProjectID == "" {
		return snapshot
	}

	modelsResp, _, modelsErr := client.FetchAvailableModels(ctx, accessToken, snapshot.ProjectID)
	if modelsErr != nil {
		snapshot.LastError = modelsErr.Error()
		logger.LegacyPrintf("service.antigravity_gateway", "antigravity bootstrap fetchAvailableModels failed: account=%d err=%v", account.ID, modelsErr)
	} else if modelsResp != nil {
		snapshot.ModelCount = len(modelsResp.Models)
	}

	userInfoResp, userInfoErr := client.FetchUserInfo(ctx, accessToken, snapshot.ProjectID)
	if userInfoErr != nil {
		if snapshot.LastError == "" {
			snapshot.LastError = userInfoErr.Error()
		}
		logger.LegacyPrintf("service.antigravity_gateway", "antigravity bootstrap fetchUserInfo failed: account=%d err=%v", account.ID, userInfoErr)
	} else if userInfoResp != nil {
		snapshot.RegionCode = strings.TrimSpace(userInfoResp.RegionCode)
		snapshot.IsPrivate = userInfoResp.IsPrivate()
	}

	return snapshot
}

func (s *AntigravityGatewayService) persistBootstrapProjectID(ctx context.Context, account *Account, projectID string) string {
	projectID = strings.TrimSpace(projectID)
	if s == nil || account == nil || projectID == "" {
		return projectID
	}
	if strings.TrimSpace(account.GetCredential("project_id")) == projectID {
		return projectID
	}

	s.requestIdentityMu.Lock()
	defer s.requestIdentityMu.Unlock()

	if strings.TrimSpace(account.GetCredential("project_id")) == projectID {
		return projectID
	}

	credentials := cloneCredentials(account.Credentials)
	credentials["project_id"] = projectID
	account.Credentials = credentials
	if s.accountRepo == nil {
		return projectID
	}
	if err := persistAccountCredentials(ctx, s.accountRepo, account, credentials); err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "persist antigravity bootstrap project_id failed: account=%d err=%v", account.ID, err)
		return strings.TrimSpace(account.GetCredential("project_id"))
	}
	return projectID
}

func tryOnboardProjectIDWithBootstrapClient(ctx context.Context, client antigravityBootstrapClient, accessToken string, loadRaw map[string]any) (string, error) {
	tierID := resolveDefaultTierID(loadRaw)
	if tierID == "" {
		return "", errAntigravityBootstrapNoDefaultTier
	}
	projectID, err := client.OnboardUser(ctx, accessToken, tierID)
	if err != nil {
		return "", err
	}
	return projectID, nil
}
