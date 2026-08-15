package handler

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const stableCanaryHandlerBeta = "interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24,structured-outputs-2025-12-15"

type stableCanaryHandlerAccountRepo struct {
	service.AccountRepository
	account          *service.Account
	getCalls         int
	listByGroup      int
	sessionOwner     int64
	sessionClaims    int
	sessionErr       error
	sessionSupported bool
}

func (r *stableCanaryHandlerAccountRepo) ResolveAnthropicStableIdentitySessionRoute(
	_ context.Context,
	candidate service.AnthropicStableIdentitySessionRouteBinding,
) (*service.AnthropicStableIdentitySessionRouteBinding, error) {
	copyBinding := candidate
	return &copyBinding, nil
}

func (r *stableCanaryHandlerAccountRepo) AcquireAnthropicStableCanaryLease(context.Context, int64) (func() error, error) {
	return func() error { return nil }, nil
}

func (r *stableCanaryHandlerAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	r.getCalls++
	if r.account == nil || r.account.ID != id {
		return nil, service.ErrAccountNotFound
	}
	return r.account, nil
}

func (r *stableCanaryHandlerAccountRepo) ListByGroup(_ context.Context, groupID int64) ([]service.Account, error) {
	r.listByGroup++
	if r.account == nil || len(r.account.GroupIDs) != 1 || r.account.GroupIDs[0] != groupID {
		return nil, nil
	}
	return []service.Account{*r.account}, nil
}

func (r *stableCanaryHandlerAccountRepo) FindByExtraField(_ context.Context, key string, value any) ([]service.Account, error) {
	if key != service.AnthropicStableIdentityEnabledExtraKey || value != true || r.account == nil {
		return nil, nil
	}
	return []service.Account{*r.account}, nil
}

func (r *stableCanaryHandlerAccountRepo) ClaimAnthropicStableCanarySession(
	_ context.Context,
	groupID, accountID, generation, ownerUserID int64,
	sessionHash, keyFingerprint, policyFingerprint string,
) error {
	_ = groupID
	_ = accountID
	_ = generation
	_ = sessionHash
	_ = keyFingerprint
	_ = policyFingerprint
	r.sessionClaims++
	r.sessionOwner = ownerUserID
	if !r.sessionSupported {
		return service.ErrAnthropicStableCanarySessionBindingUnavailable
	}
	return r.sessionErr
}

type stableCanaryHandlerGroupRepo struct {
	service.GroupRepository
	group    *service.Group
	fallback *service.Group
	getCalls int
}

func (r *stableCanaryHandlerGroupRepo) GetByID(_ context.Context, id int64) (*service.Group, error) {
	r.getCalls++
	if r.group != nil && r.group.ID == id {
		return r.group, nil
	}
	if r.fallback != nil && r.fallback.ID == id {
		return r.fallback, nil
	}
	return nil, service.ErrGroupNotFound
}

func (r *stableCanaryHandlerGroupRepo) GetByIDLite(ctx context.Context, id int64) (*service.Group, error) {
	return r.GetByID(ctx, id)
}

type stableCanaryUnreadBody struct{ reads int }

func (r *stableCanaryUnreadBody) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

func (*stableCanaryUnreadBody) Close() error { return nil }

func newStableCanaryHandlerFixture(t *testing.T) (*GatewayHandler, *stableCanaryHandlerAccountRepo, *stableCanaryHandlerGroupRepo, *service.APIKey, middleware.AuthSubject, []byte) {
	t.Helper()
	const groupID = int64(7101)
	const accountID = int64(8101)
	const ownerUserID = int64(9101)
	const apiKeyID = int64(10101)
	accountDevice := strings.Repeat("a", 64)
	clientDevice := accountDevice
	group := &service.Group{
		ID: groupID, Name: "stable-canary-handler", Platform: service.PlatformAnthropic,
		Status: service.StatusActive, Hydrated: true, IsExclusive: true,
		ClaudeCodeOnly: true, RequireOAuthOnly: true, AccountCount: 1,
		RateMultiplier: 1,
	}
	account := &service.Account{
		ID: accountID, Name: "stable-canary-handler", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: false,
		Concurrency: 1, GroupIDs: []int64{groupID},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
		Credentials:   map[string]any{"access_token": "sk-ant-oat-stable-upstream-token", "refresh_token": "stable-refresh-token"},
		Extra: map[string]any{
			service.AnthropicStableCanaryEnabledExtraKey:             true,
			service.AnthropicStableCanaryReservedExtraKey:            true,
			service.AnthropicStableCanaryPreviousSchedulableExtraKey: true,
			service.AnthropicStableCanaryDeviceIDExtraKey:            accountDevice,
			service.AnthropicStableCanaryProfileExtraKey:             service.AnthropicStableIngressProfileCLI211222V1,
		},
	}
	accountRepo := &stableCanaryHandlerAccountRepo{account: account}
	groupRepo := &stableCanaryHandlerGroupRepo{group: group}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.AnthropicStableCanary = config.GatewayAnthropicStableCanaryConfig{
		Enabled: true, GroupID: groupID, AccountID: accountID, OwnerUserID: ownerUserID, APIKeyID: apiKeyID,
		MaxBodyBytes: 64 << 20,
	}
	gatewayService := service.NewGatewayService(
		accountRepo, groupRepo, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheService.Stop)
	handler := &GatewayHandler{gatewayService: gatewayService, billingCacheService: billingCacheService, cfg: cfg}
	user := &service.User{ID: ownerUserID, Status: service.StatusActive, Balance: 100, Concurrency: 10}
	apiKey := &service.APIKey{
		ID: apiKeyID, UserID: ownerUserID, GroupID: ptrInt64StableCanaryHandler(groupID),
		Status: service.StatusActive, User: user, Group: group,
	}
	subject := middleware.AuthSubject{UserID: ownerUserID, Concurrency: 10}
	body := []byte(`{"model":"claude-opus-5","max_tokens":4096,"messages":[{"role":"user","content":"handler path"}],"metadata":{"user_id":"{\"device_id\":\"` + clientDevice + `\",\"account_uuid\":\"\",\"session_id\":\"11111111-1111-4111-8111-111111111111\"}"},"stream":true}`)
	return handler, accountRepo, groupRepo, apiKey, subject, body
}

func ptrInt64StableCanaryHandler(value int64) *int64 { return &value }

func setStableCanaryHandlerContext(c *gin.Context, apiKey *service.APIKey, subject middleware.AuthSubject) {
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), subject)
}

func setStableCanaryHandlerProfile(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "identity")
	req.Header.Set("User-Agent", "claude-cli/2.1.222 (external, cli)")
	req.Header.Set("x-app", "cli")
	req.Header.Set("X-Claude-Code-Session-Id", "11111111-1111-4111-8111-111111111111")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", stableCanaryHandlerBeta)
}

func TestGatewayHandlerMessages_AnthropicStableCanaryUsesStrictEntry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var upstreamCalls int
	var upstreamBody []byte
	var upstreamHeader http.Header
	var upstreamPath, upstreamQuery string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		upstreamHeader = r.Header.Clone()
		upstreamPath, upstreamQuery = r.URL.Path, r.URL.RawQuery
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "stable-handler-upstream")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"permission_error","message":"fixture"}}`)
	}))
	defer upstream.Close()

	originalTransport := http.DefaultTransport
	targetAddress := strings.TrimPrefix(upstream.URL, "https://")
	dialer := &net.Dialer{}
	transport := originalTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ForceAttemptHTTP2 = false
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test-only capture server
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, targetAddress)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		http.DefaultTransport = originalTransport
	})

	h, accountRepo, groupRepo, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", bytes.NewReader(body))
	setStableCanaryHandlerProfile(c.Request)
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Equal(t, 1, upstreamCalls, "strict handler entry must produce exactly one physical request for a non-401 response")
	require.Equal(t, "/v1/messages", upstreamPath)
	require.Empty(t, upstreamQuery, "the inbound beta query is profile evidence only and must not reach Anthropic")
	require.Equal(t, "Bearer sk-ant-oat-stable-upstream-token", upstreamHeader.Get("Authorization"))
	require.Equal(t, "Go-http-client/1.1", upstreamHeader.Get("User-Agent"))
	require.Equal(t, stableCanaryHandlerBeta+",oauth-2025-04-20", upstreamHeader.Get("anthropic-beta"))
	require.Empty(t, upstreamHeader.Get("x-app"))
	require.Empty(t, upstreamHeader.Get("X-Claude-Code-Session-Id"))
	require.Equal(t, body, upstreamBody, "D1 must preserve every inbound JSON byte")
	require.Equal(t, 2, accountRepo.getCalls, "the queued strict executor must reload the account before egress")
	require.Equal(t, 2, accountRepo.listByGroup)
	require.Equal(t, 2, groupRepo.getCalls)
	mode, _ := c.Get("anthropic_passthrough_mode")
	require.Equal(t, "canary", mode)
}

func TestGatewayHandlerMessages_AnthropicStableCanaryEmptyStreamReturnsBadGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamCalls := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	originalTransport := http.DefaultTransport
	targetAddress := strings.TrimPrefix(upstream.URL, "https://")
	dialer := &net.Dialer{}
	transport := originalTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ForceAttemptHTTP2 = false
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test-only capture server
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, targetAddress)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		http.DefaultTransport = originalTransport
	})

	h, _, _, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", bytes.NewReader(body))
	setStableCanaryHandlerProfile(c.Request)
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, 1, upstreamCalls)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), "upstream_error")
	require.Contains(t, recorder.Body.String(), "response was interrupted")
}

func TestGatewayHandlerMessages_AnthropicStableCanaryRejectsOtherOwnerKeyBeforeBodyRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, groupRepo, apiKey, subject, _ := newStableCanaryHandlerFixture(t)
	apiKey.ID++
	body := &stableCanaryUnreadBody{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", nil)
	c.Request.Body = body
	c.Request.ContentLength = -1
	setStableCanaryHandlerProfile(c.Request)
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Zero(t, body.reads)
	require.Zero(t, accountRepo.getCalls)
	require.Zero(t, accountRepo.listByGroup)
	require.Zero(t, groupRepo.getCalls)
}

func TestGatewayHandlerMessages_AnthropicStableSharedBindingUnavailableBeforeEgress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, groupRepo, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	canary := &h.cfg.Gateway.AnthropicStableCanary
	canary.OwnerUserID = 0
	canary.APIKeyID = 0
	canary.SharedUsers = true
	canary.SharedAPIKeyIDs = []int64{apiKey.ID}
	canary.SessionGeneration = 1
	canary.SessionHMACKey = strings.Repeat("h", 32)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", bytes.NewReader(body))
	setStableCanaryHandlerProfile(c.Request)
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "temporarily unavailable")
	require.NotContains(t, recorder.Body.String(), "session binding")
	require.GreaterOrEqual(t, accountRepo.getCalls, 1)
	require.GreaterOrEqual(t, groupRepo.getCalls, 1)
}

func TestGatewayHandlerMessages_AnthropicStableSharedModePreservesIdentityAndBindsUserBeforeEgress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var upstreamCalls int
	var upstreamBody []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"shared-handler\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()

	originalTransport := http.DefaultTransport
	targetAddress := strings.TrimPrefix(upstream.URL, "https://")
	dialer := &net.Dialer{}
	transport := originalTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ForceAttemptHTTP2 = false
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test-only capture server
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, targetAddress)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		http.DefaultTransport = originalTransport
	})

	h, accountRepo, _, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	accountRepo.sessionSupported = true
	clientDevice := strings.Repeat("b", 64)
	body = []byte(strings.Replace(string(body), strings.Repeat("a", 64), clientDevice, 1))
	body = []byte(strings.Replace(string(body), `account_uuid\":\"\"`, `account_uuid\":\"44444444-4444-4444-8444-444444444444\"`, 1))
	canary := &h.cfg.Gateway.AnthropicStableCanary
	canary.OwnerUserID = 0
	canary.APIKeyID = 0
	canary.SharedUsers = true
	canary.SharedAPIKeyIDs = []int64{apiKey.ID}
	canary.SessionGeneration = 1
	canary.SessionHMACKey = strings.Repeat("h", 32)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", bytes.NewReader(body))
	setStableCanaryHandlerProfile(c.Request)
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "keep-alive", recorder.Header().Get("Connection"))
	require.Equal(t, "no", recorder.Header().Get("X-Accel-Buffering"))
	require.Equal(t, 1, upstreamCalls)
	require.Equal(t, body, upstreamBody)
	require.Equal(t, 1, accountRepo.sessionClaims)
	require.Equal(t, subject.UserID, accountRepo.sessionOwner)
	require.NotContains(t, recorder.Body.String(), "session binding")
}

func TestGatewayHandlerMessages_AnthropicStableCanaryRejectsProfileBeforeAccountRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, groupRepo, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", bytes.NewReader(body))
	setStableCanaryHandlerProfile(c.Request)
	c.Request.Header.Set("User-Agent", "claude-cli/2.8.4 (external, cli)")
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, accountRepo.getCalls, "an unknown profile must not load Canary credentials")
	require.Zero(t, accountRepo.listByGroup)
	require.Zero(t, groupRepo.getCalls)
}

func TestGatewayHandlerMessages_AnthropicStableCanarySecurityAuditBlocksBeforeAccountRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, groupRepo, apiKey, subject, body := newStableCanaryHandlerFixture(t)
	engine := blockingHandlerPromptEngine()
	h.securityAuditCoordinator = securityaudit.NewCoordinator(nil, engine)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", bytes.NewReader(body))
	setStableCanaryHandlerProfile(c.Request)
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.Messages(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), securityaudit.ErrorCodeBlocked)
	require.Zero(t, accountRepo.getCalls, "a blocked request must not load stable account credentials")
	require.Zero(t, accountRepo.listByGroup)
	require.Zero(t, groupRepo.getCalls)
	evaluated, _, requests := engine.snapshot()
	require.Equal(t, 1, evaluated)
	require.Len(t, requests, 1)
	require.Equal(t, body, requests[0].Body, "security audit may inspect but must not rewrite the stable wire body")
}

func TestGatewayHandlerCountTokens_AnthropicStableCanaryReturnsBeforeBodyOrAccountRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, accountRepo, groupRepo, apiKey, subject, _ := newStableCanaryHandlerFixture(t)
	body := &stableCanaryUnreadBody{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Body = body
	c.Request.ContentLength = -1
	setStableCanaryHandlerContext(c, apiKey, subject)

	h.CountTokens(c)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Zero(t, body.reads, "the local 404 must not inspect the request body")
	require.Zero(t, accountRepo.getCalls)
	require.Zero(t, accountRepo.listByGroup)
	require.Zero(t, groupRepo.getCalls)
}

func TestShouldRecordAnthropicStableCanaryUsageRequiresCompletionOrEvidence(t *testing.T) {
	semanticAt := 12
	tests := []struct {
		name       string
		result     *service.ForwardResult
		forwardErr error
		want       bool
	}{
		{name: "nil result", forwardErr: io.ErrUnexpectedEOF},
		{name: "successful zero usage", result: &service.ForwardResult{}, want: true},
		{name: "truncated without evidence", result: &service.ForwardResult{}, forwardErr: io.ErrUnexpectedEOF},
		{name: "truncated after semantic output", result: &service.ForwardResult{FirstTokenMs: &semanticAt}, forwardErr: io.ErrUnexpectedEOF, want: true},
		{name: "truncated with upstream usage", result: &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 9}}, forwardErr: io.ErrUnexpectedEOF, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldRecordAnthropicStableCanaryUsage(tt.result, tt.forwardErr))
		})
	}
}

func TestAnthropicStableCanaryUserSlotWaitNeverWritesPing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &helperConcurrencyCacheStub{
		userSeq:     []bool{false, true},
		waitAllowed: true,
	}
	h := &GatewayHandler{
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, time.Millisecond),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", nil)

	startedAt := time.Now()
	release, err := h.acquireAnthropicStableCanaryUserSlot(c, 9101, 1)

	require.NoError(t, err)
	require.NotNil(t, release)
	require.GreaterOrEqual(t, time.Since(startedAt), initialBackoff,
		"the fixture must exercise the wait branch rather than the immediate path")
	require.Empty(t, recorder.Body.String(), "stable wait must not prepend a gateway SSE frame")
	require.Empty(t, recorder.Header().Get("Content-Type"), "stable wait must not commit SSE headers")
	require.False(t, service.IsResponseCommitted(c))
	release()
}
