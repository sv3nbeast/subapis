package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestGatewayHandlerCountTokens_AnthropicStableIdentityNonClaudeClientFallsBack(t *testing.T) {
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

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Greater(t, body.reads, 0, "non-Claude traffic must continue through the ordinary handler")
	require.Zero(t, accountRepo.getCalls)
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

func TestGatewayHandlerMessages_AnthropicStableIdentityRejectsUnreviewedClaudeVersionBeforeBodyRead(t *testing.T) {
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
	c.Request.Header.Set("User-Agent", "claude-cli/2.8.4 (external, cli)")
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "not approved")
	require.Zero(t, body.reads, "an unreviewed Claude version must fail before any body or credential is consumed")
	require.Zero(t, accountRepo.getCalls)
}

func TestGatewayHandlerCountTokens_AnthropicStableIdentityRejectsUnreviewedClaudeVersionBeforeBodyRead(t *testing.T) {
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
	c.Request.Header.Set("User-Agent", "claude-cli/2.8.4 (external, cli)")
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.CountTokens(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "not approved")
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
