package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// retryBillingHandlerCache is deliberately a small in-memory implementation
// of the production GatewayCache plus the optional retry ledger. It lets this
// test exercise the real Messages entrypoint without requiring Redis.
type retryBillingHandlerCache struct {
	mu      sync.Mutex
	markers map[string]string
}

type retryBillingHandlerSettingRepo struct{}

type retryBillingUsageLogProbe struct {
	service.UsageLogRepository
	calls int
}

func (p *retryBillingUsageLogProbe) Create(context.Context, *service.UsageLog) (bool, error) {
	p.calls++
	return true, nil
}

func (retryBillingHandlerSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (retryBillingHandlerSettingRepo) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}
func (retryBillingHandlerSettingRepo) Set(context.Context, string, string) error { return nil }
func (retryBillingHandlerSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	return make(map[string]string, len(keys)), nil
}
func (retryBillingHandlerSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (retryBillingHandlerSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (retryBillingHandlerSettingRepo) Delete(context.Context, string) error { return nil }

func (c *retryBillingHandlerCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, service.ErrStickySessionNotFound
}
func (c *retryBillingHandlerCache) SetAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (c *retryBillingHandlerCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (c *retryBillingHandlerCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (c *retryBillingHandlerCache) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (c *retryBillingHandlerCache) MarkPreSemanticFailure(_ context.Context, fingerprint, logicalRequestID string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.markers == nil {
		c.markers = make(map[string]string)
	}
	if _, exists := c.markers[fingerprint]; !exists {
		c.markers[fingerprint] = logicalRequestID
	}
	return nil
}

func (c *retryBillingHandlerCache) GetPreSemanticFailure(_ context.Context, fingerprint string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.markers[fingerprint]
	return value, ok, nil
}
func (c *retryBillingHandlerCache) ClearPreSemanticFailure(_ context.Context, fingerprint string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.markers, fingerprint)
	return nil
}

type retryBillingSequenceUpstream struct {
	mu        sync.Mutex
	calls     int
	responses []*http.Response
}

type retryBillingPartialBody struct {
	payload []byte
	sent    bool
}

func (b *retryBillingPartialBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, b.payload), nil
}

func (b *retryBillingPartialBody) Close() error { return nil }

type retryBillingPartialUpstream struct {
	payload []byte
}

func (u *retryBillingPartialUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.DoWithTLS(req, "", 0, 0, nil)
}

func (u *retryBillingPartialUpstream) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       &retryBillingPartialBody{payload: u.payload},
	}, nil
}

func (u *retryBillingSequenceUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.DoWithTLS(req, "", 0, 0, nil)
}

func (u *retryBillingSequenceUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls++
	index := u.calls - 1
	if index >= len(u.responses) {
		index = len(u.responses) - 1
	}
	if req != nil && req.Body != nil {
		_, _ = io.ReadAll(req.Body)
	}
	return u.responses[index], nil
}

func newRetryBillingHandler(t *testing.T, group *service.Group, account *service.Account, cache service.GatewayCache, upstream service.HTTPUpstream) (*GatewayHandler, func(), *retryBillingUsageLogProbe) {
	t.Helper()
	schedulerCache := &fakeSchedulerCache{accounts: []*service.Account{account}}
	schedulerSnapshot := service.NewSchedulerSnapshotService(schedulerCache, nil, nil, nil, nil)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	settingSvc := service.NewSettingService(retryBillingHandlerSettingRepo{}, &config.Config{})
	usageProbe := &retryBillingUsageLogProbe{}
	gatewaySvc := service.NewGatewayService(
		nil,                          // accountRepo
		&fakeGroupRepo{group: group}, // groupRepo
		usageProbe,
		nil, // usageBillingRepo
		nil, // userRepo
		nil, // userSubRepo
		nil, // userGroupRateRepo
		cache,
		cfg,
		schedulerSnapshot,
		nil, // concurrencyService
		service.NewBillingService(cfg, nil),
		service.NewRateLimitService(nil, nil, cfg, nil, nil),
		billingCacheSvc,
		nil, // identityService
		upstream,
		service.NewDeferredService(nil, nil, 0),
		nil, // claudeTokenProvider
		nil, // kiroTokenProvider
		nil, // droidTokenProvider
		nil, // kiroCooldownStore
		nil, // sessionLimitCache
		nil, // rpmCache
		nil, // modelCapacityCooldownCache
		nil, // digestStore
		settingSvc,
		nil, // tlsFPProfileService
		nil, // channelService
	)
	concurrencySvc := service.NewConcurrencyService(&fakeConcurrencyCache{})
	h := &GatewayHandler{
		gatewayService:           gatewaySvc,
		billingCacheService:      billingCacheSvc,
		concurrencyHelper:        NewConcurrencyHelper(concurrencySvc, SSEPingFormatClaude, 0),
		maxAccountSwitches:       0,
		maxAccountSwitchesGemini: 1,
		cfg:                      cfg,
		settingService:           settingSvc,
	}
	return h, func() { billingCacheSvc.Stop() }, usageProbe
}

func retryBillingRequest(group *service.Group, apiKey *service.APIKey, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.220 (external, cli)")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	c.Request = req
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})
	return c, rec
}

func TestGatewayHandlerMessages_PreSemanticRetryLinksOneLogicalBillingRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID, accountID, userID, apiKeyID := int64(9010), int64(8010), int64(7010), int64(6010)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive}
	account := &service.Account{
		ID: accountID, Name: "retry-billing-handler", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeSetupToken, Credentials: map[string]any{"access_token": "setup-token"},
		Concurrency: 1, Priority: 1, Status: service.StatusActive, Schedulable: true,
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	failureBody := []byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)
	successSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_retry\",\"usage\":{\"input_tokens\":5}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	upstream := &retryBillingSequenceUpstream{responses: []*http.Response{
		{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(failureBody))},
		{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(failureBody))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(bytes.NewReader([]byte(successSSE)))},
	}}
	cache := &retryBillingHandlerCache{}
	h, cleanup, usageProbe := newRetryBillingHandler(t, group, account, cache, upstream)
	defer cleanup()
	apiKey := &service.APIKey{ID: apiKeyID, UserID: userID, GroupID: &groupID, Status: service.StatusActive, User: &service.User{ID: userID, Concurrency: 10, Balance: 100}, Group: group}
	body := []byte(`{"model":"claude-opus-5","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"retry me"}]}`)

	first, firstRec := retryBillingRequest(group, apiKey, body)
	h.Messages(first)
	require.NotEqual(t, http.StatusOK, firstRec.Code)
	require.Zero(t, usageProbe.calls, "a pre-semantic failure must not create a usage record")
	require.Len(t, cache.markers, 1, "exhausted pre-semantic failover must leave a retry marker")

	second, secondRec := retryBillingRequest(group, apiKey, body)
	h.Messages(second)
	require.NotEqual(t, http.StatusOK, secondRec.Code)
	require.Zero(t, usageProbe.calls, "repeated pre-semantic failures must remain unbilled")
	require.Len(t, cache.markers, 1, "a repeated pre-semantic failure must re-arm the same logical retry marker")

	third, thirdRec := retryBillingRequest(group, apiKey, body)
	h.Messages(third)
	require.Equal(t, http.StatusOK, thirdRec.Code)
	require.Contains(t, thirdRec.Body.String(), "event: message_stop")
	require.Equal(t, 1, usageProbe.calls, "only the successful logical request should create one usage record")
	require.Equal(t, 3, upstream.calls)
	require.Empty(t, cache.markers, "the retry marker is cleared only after successful usage settlement")
}

func TestGatewayHandlerMessages_DoesNotMarkRetryAfterClientVisibleStreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID, accountID, userID, apiKeyID := int64(9011), int64(8011), int64(7011), int64(6011)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive}
	account := &service.Account{
		ID: accountID, Name: "retry-billing-partial", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeOAuth, Credentials: map[string]any{"access_token": "oauth-token"},
		Concurrency: 1, Priority: 1, Status: service.StatusActive, Schedulable: true,
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	partialSSE := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_partial\",\"usage\":{\"input_tokens\":9}}}\n\n")
	cache := &retryBillingHandlerCache{}
	h, cleanup, usageProbe := newRetryBillingHandler(t, group, account, cache, &retryBillingPartialUpstream{payload: partialSSE})
	defer cleanup()
	apiKey := &service.APIKey{ID: apiKeyID, UserID: userID, GroupID: &groupID, Status: service.StatusActive, User: &service.User{ID: userID, Concurrency: 10, Balance: 100}, Group: group}

	c, rec := retryBillingRequest(group, apiKey, []byte(`{"model":"claude-opus-5","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"partial"}]}`))
	h.Messages(c)

	require.NotEmpty(t, rec.Body.String(), "the already-started stream must remain visible to the client")
	require.Empty(t, cache.markers, "a post-output read error must not create a pre-semantic retry marker")
	require.Equal(t, 1, usageProbe.calls, "partial upstream usage must remain billable exactly once")
}
