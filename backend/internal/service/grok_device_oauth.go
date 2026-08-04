package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const (
	grokDeviceDefaultPollInterval = 5 * time.Second
	grokDeviceMaxPollInterval     = 30 * time.Second
	grokDeviceMaxSessions         = 1024
)

type GrokDeviceAuthorizationStartResult struct {
	SessionID               string `json:"session_id"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	IntervalSeconds         int    `json:"interval_seconds"`
	ExpiresAt               int64  `json:"expires_at"`
}

type GrokDeviceAuthorizationPollResult struct {
	Status            string `json:"status"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

// GrokDeviceReauthorizationRepository atomically replaces the exact Grok
// credential generation that was present when device authorization started.
type GrokDeviceReauthorizationRepository interface {
	ReauthorizeGrokOAuthIfCredentialsUnchanged(
		ctx context.Context,
		id int64,
		expectedCredentials map[string]any,
		expectedProxyID *int64,
		credentials map[string]any,
		extra map[string]any,
	) (bool, error)
}

// GrokDeviceReauthorizationAdminService is intentionally narrower than
// AdminService so gateway/test repositories are not forced to implement an
// admin-only credential mutation.
type GrokDeviceReauthorizationAdminService interface {
	ReauthorizeGrokOAuthAccountIfUnchanged(
		ctx context.Context,
		id int64,
		expectedCredentials map[string]any,
		expectedProxyID *int64,
		credentials map[string]any,
		extra map[string]any,
	) (bool, error)
}

type grokDeviceSession struct {
	mu           sync.Mutex
	deviceCode   string
	clientID     string
	proxyURL     string
	proxyID      *int64
	accountID    *int64
	credentials  map[string]any
	createdAt    time.Time
	expiresAt    time.Time
	nextPollAt   time.Time
	pollInterval time.Duration
	tokenInfo    *GrokTokenInfo
	completed    bool
	completedID  int64
}

type grokDeviceSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*grokDeviceSession
	stopOnce sync.Once
	stopCh   chan struct{}
}

func newGrokDeviceSessionStore() *grokDeviceSessionStore {
	store := &grokDeviceSessionStore{
		sessions: make(map[string]*grokDeviceSession),
		stopCh:   make(chan struct{}),
	}
	go store.cleanupLoop()
	return store
}

func (s *grokDeviceSessionStore) Set(id string, session *grokDeviceSession) {
	if s == nil || id == "" || session == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, candidate := range s.sessions {
		if candidate == nil || !now.Before(candidate.expiresAt) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= grokDeviceMaxSessions {
		oldestID := ""
		var oldest time.Time
		for key, candidate := range s.sessions {
			if candidate != nil && (oldestID == "" || candidate.createdAt.Before(oldest)) {
				oldestID, oldest = key, candidate.createdAt
			}
		}
		delete(s.sessions, oldestID)
	}
	s.sessions[id] = session
}

func (s *grokDeviceSessionStore) Get(id string) (*grokDeviceSession, bool) {
	if s == nil || strings.TrimSpace(id) == "" {
		return nil, false
	}
	s.mu.RLock()
	session, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok || session == nil || !time.Now().Before(session.expiresAt) {
		if ok {
			s.Delete(id)
		}
		return nil, false
	}
	return session, true
}

func (s *grokDeviceSessionStore) Delete(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func (s *grokDeviceSessionStore) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *grokDeviceSessionStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			s.mu.Lock()
			for id, session := range s.sessions {
				if session == nil || !now.Before(session.expiresAt) {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *GrokOAuthService) StartDeviceAuthorization(
	ctx context.Context,
	proxyID, accountID *int64,
	credentials map[string]any,
) (*GrokDeviceAuthorizationStartResult, error) {
	if s == nil || s.oauthClient == nil || s.deviceStore == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "GROK_DEVICE_NOT_CONFIGURED", "Grok device authorization is not configured")
	}
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	clientID := xai.EffectiveClientID()
	device, err := s.oauthClient.StartDeviceAuthorization(ctx, proxyURL, clientID, xai.EffectiveScope())
	if err != nil {
		return nil, err
	}
	sessionID, err := xai.GenerateSessionID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "GROK_DEVICE_SESSION_FAILED", "failed to generate session ID: %v", err)
	}
	now := time.Now()
	interval := time.Duration(device.Interval) * time.Second
	if interval < time.Second {
		interval = grokDeviceDefaultPollInterval
	}
	if interval > grokDeviceMaxPollInterval {
		interval = grokDeviceMaxPollInterval
	}
	expiresIn := time.Duration(device.ExpiresIn) * time.Second
	if expiresIn <= 0 || expiresIn > xai.SessionTTL {
		expiresIn = xai.SessionTTL
	}
	expiresAt := now.Add(expiresIn)
	s.deviceStore.Set(sessionID, &grokDeviceSession{
		deviceCode:   strings.TrimSpace(device.DeviceCode),
		clientID:     clientID,
		proxyURL:     proxyURL,
		proxyID:      cloneGrokProxyID(proxyID),
		accountID:    cloneGrokProxyID(accountID),
		credentials:  cloneCredentials(credentials),
		createdAt:    now,
		expiresAt:    expiresAt,
		nextPollAt:   now.Add(interval),
		pollInterval: interval,
	})
	return &GrokDeviceAuthorizationStartResult{
		SessionID:               sessionID,
		UserCode:                strings.TrimSpace(device.UserCode),
		VerificationURI:         strings.TrimSpace(device.VerificationURI),
		VerificationURIComplete: strings.TrimSpace(device.VerificationURIComplete),
		IntervalSeconds:         int(interval / time.Second),
		ExpiresAt:               expiresAt.Unix(),
	}, nil
}

func (s *GrokOAuthService) PollDeviceAuthorization(ctx context.Context, sessionID string) (*GrokDeviceAuthorizationPollResult, error) {
	if s == nil || s.oauthClient == nil || s.deviceStore == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "GROK_DEVICE_NOT_CONFIGURED", "Grok device authorization is not configured")
	}
	sessionID = strings.TrimSpace(sessionID)
	session, ok := s.deviceStore.Get(sessionID)
	if !ok {
		return nil, infraerrors.New(http.StatusGone, "GROK_DEVICE_SESSION_EXPIRED", "device authorization session not found or expired")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.tokenInfo != nil || session.completed {
		return &GrokDeviceAuthorizationPollResult{Status: "authorized"}, nil
	}
	now := time.Now()
	if !now.Before(session.expiresAt) {
		s.deviceStore.Delete(sessionID)
		return nil, infraerrors.New(http.StatusGone, "GROK_DEVICE_SESSION_EXPIRED", "device authorization session expired")
	}
	if now.Before(session.nextPollAt) {
		return &GrokDeviceAuthorizationPollResult{
			Status:            "pending",
			RetryAfterSeconds: durationCeilSeconds(time.Until(session.nextPollAt)),
		}, nil
	}

	tokenResp, err := s.oauthClient.PollDeviceAuthorization(ctx, session.deviceCode, session.proxyURL, session.clientID)
	if err != nil {
		var deviceErr *xai.DeviceAuthorizationError
		if errors.As(err, &deviceErr) {
			switch deviceErr.Code {
			case "authorization_pending":
				session.nextPollAt = now.Add(session.pollInterval)
				return &GrokDeviceAuthorizationPollResult{Status: "pending", RetryAfterSeconds: durationCeilSeconds(session.pollInterval)}, nil
			case "slow_down":
				session.pollInterval += 5 * time.Second
				if session.pollInterval > grokDeviceMaxPollInterval {
					session.pollInterval = grokDeviceMaxPollInterval
				}
				if deviceErr.RetryAfter > session.pollInterval {
					session.pollInterval = min(deviceErr.RetryAfter, grokDeviceMaxPollInterval)
				}
				session.nextPollAt = now.Add(session.pollInterval)
				return &GrokDeviceAuthorizationPollResult{Status: "pending", RetryAfterSeconds: durationCeilSeconds(session.pollInterval)}, nil
			case "access_denied":
				s.deviceStore.Delete(sessionID)
				return nil, infraerrors.New(http.StatusForbidden, "GROK_DEVICE_ACCESS_DENIED", "xAI device authorization was denied")
			case "expired_token":
				s.deviceStore.Delete(sessionID)
				return nil, infraerrors.New(http.StatusGone, "GROK_DEVICE_SESSION_EXPIRED", "xAI device authorization expired")
			}
		}
		session.nextPollAt = now.Add(session.pollInterval)
		return nil, err
	}
	session.tokenInfo = s.tokenInfoFromResponse(tokenResp, session.clientID, nil)
	return &GrokDeviceAuthorizationPollResult{Status: "authorized"}, nil
}

func (s *GrokOAuthService) CompleteDeviceAuthorization(
	sessionID string,
	expectedProxyID *int64,
	expectedAccountID *int64,
	complete func(*GrokTokenInfo, map[string]any) (int64, error),
) (int64, error) {
	if s == nil || s.deviceStore == nil || complete == nil {
		return 0, infraerrors.New(http.StatusServiceUnavailable, "GROK_DEVICE_NOT_CONFIGURED", "Grok device authorization is not configured")
	}
	sessionID = strings.TrimSpace(sessionID)
	session, ok := s.deviceStore.Get(sessionID)
	if !ok {
		return 0, infraerrors.New(http.StatusGone, "GROK_DEVICE_SESSION_EXPIRED", "device authorization session not found or expired")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if !grokCredentialProxyIDsEqual(session.proxyID, expectedProxyID) {
		return 0, infraerrors.New(http.StatusConflict, "GROK_DEVICE_PROXY_CHANGED", "account proxy changed during device authorization; start authorization again")
	}
	if !grokCredentialProxyIDsEqual(session.accountID, expectedAccountID) {
		return 0, infraerrors.New(http.StatusConflict, "GROK_DEVICE_ACCOUNT_CHANGED", "device authorization belongs to a different account operation")
	}
	if session.completed {
		return session.completedID, nil
	}
	if session.tokenInfo == nil {
		return 0, infraerrors.New(http.StatusConflict, "GROK_DEVICE_NOT_AUTHORIZED", "device authorization is not complete")
	}
	tokenCopy := *session.tokenInfo
	completedID, err := complete(&tokenCopy, cloneCredentials(session.credentials))
	if err != nil {
		return 0, err
	}
	if completedID <= 0 {
		return 0, infraerrors.New(http.StatusInternalServerError, "GROK_DEVICE_COMPLETION_INVALID", "device authorization completion returned an invalid account ID")
	}
	session.tokenInfo = nil
	session.completed = true
	session.completedID = completedID
	return completedID, nil
}

func durationCeilSeconds(duration time.Duration) int {
	if duration <= 0 {
		return 1
	}
	return int((duration + time.Second - 1) / time.Second)
}
