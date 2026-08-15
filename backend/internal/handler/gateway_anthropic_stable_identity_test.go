package handler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func convertStableCanaryHandlerFixtureToIdentity(
	t *testing.T,
	h *GatewayHandler,
	repo *stableCanaryHandlerAccountRepo,
	apiKey *service.APIKey,
) {
	t.Helper()
	require.NotNil(t, h)
	require.NotNil(t, h.cfg)
	require.NotNil(t, repo)
	require.NotNil(t, repo.account)
	require.NotNil(t, apiKey)
	require.NotNil(t, apiKey.GroupID)

	deviceID := repo.account.AnthropicStableCanaryDeviceID()
	h.cfg.Gateway.AnthropicStableCanary.Enabled = false
	h.cfg.JWT.Secret = strings.Repeat("j", 48)
	repo.account.Extra = map[string]any{
		service.AnthropicStableIdentityEnabledExtraKey:             true,
		service.AnthropicStableIdentityStateExtraKey:               service.AnthropicStableIdentityStateActive,
		service.AnthropicStableIdentityDeviceIDExtraKey:            deviceID,
		service.AnthropicStableIdentityPreviousSchedulableExtraKey: true,
		service.AnthropicStableIdentityPreviousConcurrencyExtraKey: 1,
		service.AnthropicStableIdentityProfileExtraKey:             service.AnthropicStableIngressProfileCLI211222V1,
		service.AnthropicStableIdentityGenerationExtraKey:          int64(1),
		service.AnthropicStableIdentityCreatedAtExtraKey:           "2026-08-14T00:00:00Z",
		service.AnthropicStableIdentityUpdatedAtExtraKey:           "2026-08-14T00:00:00Z",
		service.AnthropicStableIdentityBlockedExtraKey:             false,
		service.AnthropicStableIdentityBlockedReasonExtraKey:       "",
	}
	repo.account.Schedulable = false
	repo.account.Concurrency = 1
	h.gatewayService.InvalidateAnthropicStableIdentityRoutes()
}

func TestGatewayHandlerCountTokens_AnthropicStableIdentityReturnsLocal404BeforeBodyRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, _, apiKey, subject, _ := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	body := &stableCanaryUnreadBody{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Body = body
	c.Request.ContentLength = -1
	setStableCanaryHandlerProfile(c.Request)
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.CountTokens(c)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Body.String(), "not_found_error")
	require.Zero(t, body.reads, "stable count_tokens must not consume or forward the request body")
	require.Zero(t, accountRepo.getCalls, "the route directory should not select or refresh an account for count_tokens")
}

func TestGatewayHandlerCountTokens_AnthropicStableIdentityHonorsClaudeCodeOnlyRestriction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, _, apiKey, subject, _ := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	body := &stableCanaryUnreadBody{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Body = body
	c.Request.ContentLength = -1
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "ordinary-anthropic-sdk/1.0")
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.CountTokens(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "This group only allows Claude Code clients")
	require.Zero(t, body.reads, "the group restriction should reject before reading a non-Desktop count_tokens body")
	require.Zero(t, accountRepo.getCalls)
}

func TestGatewayHandlerMessages_AnthropicStableIdentityAcceptsDesktopShapeWhenGroupAllows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, _, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	apiKey.Group.ClaudeCodeOnly = false
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	// Desktop/SDK traffic may omit the CLI query, x-app, session header and
	// feature headers, and may use a non-stream request. The stable route still
	// has the body session/device envelope needed for independent routing.
	desktopBody := []byte(strings.Replace(string(body), `,"stream":true`, `,"stream":false`, 1))
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(desktopBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "Mozilla/5.0 Claude/1.12603.1 Electron/36.3.1")
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "session is temporarily unavailable")
	require.Equal(t, 1, accountRepo.sessionClaims, "Desktop-shaped traffic should reach stable session routing")
	require.Equal(t, 2, accountRepo.getCalls)
}

func TestGatewayHandlerMessages_AnthropicStableIdentityKeepsClaudeProbeAllowedByGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, _, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	probeBody := []byte(strings.Replace(strings.Replace(string(body), `"max_tokens":4096`, `"max_tokens":1`, 1), `,"stream":true`, `,"stream":false`, 1))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(probeBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.223")
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "session is temporarily unavailable")
	require.Equal(t, 1, accountRepo.sessionClaims)
}

func TestGatewayHandlerMessages_AnthropicStableIdentityRejectsNonClaudeWhenGroupRestricted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, _, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "anthropic-sdk-go/1.0")
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "This group only allows Claude Code clients")
	require.Zero(t, accountRepo.sessionClaims)
	require.Zero(t, accountRepo.getCalls)
}

func TestGatewayHandlerMessages_AnthropicStableIdentityRestoresBodyForClaudeFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, groupRepo, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	fallbackID := int64(7102)
	fallback := &service.Group{ID: fallbackID, Name: "stable-fallback", Platform: service.PlatformAnthropic, Status: service.StatusActive, Hydrated: true}
	groupRepo.fallback = fallback
	apiKey.Group.FallbackGroupID = &fallbackID
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "anthropic-sdk-go/1.0")
	setStableCanaryHandlerContext(c, apiKey, subject)

	require.False(t, h.tryAnthropicStableIdentityMessages(c, apiKey, subject, time.Time{}))
	restored, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, body, restored, "fallback must receive the exact original body")
	require.Zero(t, accountRepo.sessionClaims)
}

func TestGatewayHandlerMessages_AnthropicStableIdentityRestoresBodyForOpaqueMetadataUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, _, apiKey, subject, _ := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	apiKey.Group.ClaudeCodeOnly = false
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"ordinary-user"},"stream":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "anthropic-sdk-go/1.0")
	setStableCanaryHandlerContext(c, apiKey, subject)

	require.False(t, h.tryAnthropicStableIdentityMessages(c, apiKey, subject, time.Time{}))
	restored, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, body, restored)
	require.Zero(t, accountRepo.sessionClaims)
}

func TestGatewayHandlerMessages_AnthropicStableIdentityUsesCurrentGroupPool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, _, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	// No session repository is enabled in this fixture. Reaching that stable-only
	// admission check proves the request bypassed generic account scheduling while
	// still using the same existing group/API-key pair.
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", bytes.NewReader(body))
	setStableCanaryHandlerProfile(c.Request)
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "session is temporarily unavailable")
	require.Equal(t, 1, accountRepo.sessionClaims)
	require.Equal(t, subject.UserID, accountRepo.sessionOwner)
	require.Equal(t, 2, accountRepo.getCalls, "stable entry reloads once before and once under its account lease")
}

func TestGatewayHandlerMessages_AnthropicStableIdentityAcceptsNativeVersionAndBetaVariants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		userAgent string
		beta      string
	}{
		{
			name:      "same version with a new feature cohort",
			userAgent: "claude-cli/2.1.222 (external, cli)",
			beta:      "target-request-new-beta,reordered-client-beta",
		},
		{
			name:      "native client version upgrade",
			userAgent: "claude-cli/2.1.223 (external, cli)",
			beta:      "new-client-beta,reordered-client-beta",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, accountRepo, _, apiKey, subject, body := newStableCanaryHandlerFixture(t)
			convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", bytes.NewReader(body))
			setStableCanaryHandlerProfile(c.Request)
			c.Request.Header.Set("User-Agent", tc.userAgent)
			c.Request.Header.Set("anthropic-beta", tc.beta)
			setStableCanaryHandlerContext(c, apiKey, subject)

			h.Messages(c)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Contains(t, recorder.Body.String(), "session is temporarily unavailable")
			require.Equal(t, 1, accountRepo.sessionClaims,
				"native Claude Code traffic must reach stable session routing instead of a finite profile allow-list")
			require.Equal(t, 2, accountRepo.getCalls)
		})
	}
}

func TestGatewayHandlerCountTokens_AnthropicStableIdentityAcceptsNativeVersionWithoutReadingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, _, apiKey, subject, _ := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	body := &stableCanaryUnreadBody{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Body = body
	c.Request.ContentLength = -1
	setStableCanaryHandlerProfile(c.Request)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.223 (external, sdk-cli)")
	c.Request.Header.Set("anthropic-beta", "new-client-beta")
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.CountTokens(c)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Body.String(), "not_found_error")
	require.Zero(t, body.reads)
	require.Zero(t, accountRepo.getCalls)
}

func TestGatewayHandlerMessages_AnthropicStableIdentityFailsManagedGroupClosedAfterPlatformDrift(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, _, apiKey, subject, _ := newStableCanaryHandlerFixture(t)
	convertStableCanaryHandlerFixtureToIdentity(t, h, accountRepo, apiKey)
	body := &stableCanaryUnreadBody{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", nil)
	c.Request.Body = body
	c.Request.ContentLength = -1
	setStableCanaryHandlerProfile(c.Request)
	apiKey.Group.Platform = service.PlatformKiro
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "group is unavailable")
	require.Zero(t, body.reads)
	require.Zero(t, accountRepo.getCalls)
}
