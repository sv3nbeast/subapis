package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 目标：严格验证“antigravity 账号通过 /v1/messages 提供 Claude 服务时”，
// 当账号 credentials.intercept_warmup_requests=true 且请求为 Warmup 时，
// 后端会在转发上游前直接拦截并返回 mock 响应（不依赖上游）。

type fakeSchedulerCache struct {
	accounts []*service.Account
}

func (f *fakeSchedulerCache) GetSnapshot(_ context.Context, _ service.SchedulerBucket) ([]*service.Account, bool, error) {
	return f.accounts, true, nil
}
func (f *fakeSchedulerCache) CaptureBucketWriteToken(_ context.Context, bucket service.SchedulerBucket) (service.SchedulerBucketWriteToken, error) {
	return service.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}
func (f *fakeSchedulerCache) SetSnapshot(_ context.Context, _ service.SchedulerBucket, _ service.SchedulerBucketWriteToken, _ []service.Account) error {
	return nil
}
func (f *fakeSchedulerCache) RetireBucket(_ context.Context, _ service.SchedulerBucket) error {
	return nil
}
func (f *fakeSchedulerCache) ReopenBucket(_ context.Context, bucket service.SchedulerBucket) (service.SchedulerBucketWriteToken, error) {
	return service.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}
func (f *fakeSchedulerCache) TryAcquireGroupLifecycleLease(_ context.Context, _ int64, _ time.Duration) (service.SchedulerGroupLifecycleLease, bool, error) {
	return service.SchedulerGroupLifecycleLease{}, false, nil
}
func (f *fakeSchedulerCache) ReleaseGroupLifecycleLease(_ context.Context, _ service.SchedulerGroupLifecycleLease) error {
	return nil
}
func (f *fakeSchedulerCache) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	for _, account := range f.accounts {
		if account != nil && account.ID == id {
			return account, nil
		}
	}
	return nil, nil
}
func (f *fakeSchedulerCache) SetAccount(_ context.Context, _ *service.Account) error { return nil }
func (f *fakeSchedulerCache) DeleteAccount(_ context.Context, _ int64) error         { return nil }
func (f *fakeSchedulerCache) UpdateLastUsed(_ context.Context, _ map[int64]time.Time) error {
	return nil
}
func (f *fakeSchedulerCache) TryLockBucket(_ context.Context, _ service.SchedulerBucket, _ time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeSchedulerCache) UnlockBucket(_ context.Context, _ service.SchedulerBucket) error {
	return nil
}
func (f *fakeSchedulerCache) ListBuckets(_ context.Context) ([]service.SchedulerBucket, error) {
	return nil, nil
}
func (f *fakeSchedulerCache) GetOutboxWatermark(_ context.Context) (int64, error) { return 0, nil }
func (f *fakeSchedulerCache) SetOutboxWatermark(_ context.Context, _ int64) error { return nil }

type fakeGroupRepo struct {
	group *service.Group
}

func (f *fakeGroupRepo) Create(context.Context, *service.Group) error { return nil }
func (f *fakeGroupRepo) GetByID(context.Context, int64) (*service.Group, error) {
	return f.group, nil
}
func (f *fakeGroupRepo) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return f.group, nil
}
func (f *fakeGroupRepo) Update(context.Context, *service.Group) error          { return nil }
func (f *fakeGroupRepo) Delete(context.Context, int64) error                   { return nil }
func (f *fakeGroupRepo) DeleteCascade(context.Context, int64) ([]int64, error) { return nil, nil }
func (f *fakeGroupRepo) List(context.Context, pagination.PaginationParams) ([]service.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (f *fakeGroupRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]service.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (f *fakeGroupRepo) ListActive(context.Context) ([]service.Group, error) { return nil, nil }
func (f *fakeGroupRepo) ListActiveByPlatform(context.Context, string) ([]service.Group, error) {
	return nil, nil
}
func (f *fakeGroupRepo) ExistsByName(context.Context, string) (bool, error) { return false, nil }
func (f *fakeGroupRepo) GetAccountCount(context.Context, int64) (int64, int64, error) {
	return 0, 0, nil
}
func (f *fakeGroupRepo) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	return 0, nil
}
func (f *fakeGroupRepo) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	return nil, nil
}
func (f *fakeGroupRepo) BindAccountsToGroup(context.Context, int64, []int64) error { return nil }
func (f *fakeGroupRepo) UpdateSortOrders(context.Context, []service.GroupSortOrderUpdate) error {
	return nil
}

type fakeConcurrencyCache struct{}

func (f *fakeConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (f *fakeConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error { return nil }
func (f *fakeConcurrencyCache) GetAccountConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}
func (f *fakeConcurrencyCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (f *fakeConcurrencyCache) DecrementAccountWaitCount(context.Context, int64) error { return nil }
func (f *fakeConcurrencyCache) GetAccountWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}
func (f *fakeConcurrencyCache) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (f *fakeConcurrencyCache) ReleaseUserSlot(context.Context, int64, string) error   { return nil }
func (f *fakeConcurrencyCache) GetUserConcurrency(context.Context, int64) (int, error) { return 0, nil }
func (f *fakeConcurrencyCache) AcquireCountTokensUserSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (f *fakeConcurrencyCache) ReleaseCountTokensUserSlot(context.Context, int64, string) error {
	return nil
}
func (f *fakeConcurrencyCache) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (f *fakeConcurrencyCache) DecrementWaitCount(context.Context, int64) error { return nil }
func (f *fakeConcurrencyCache) GetAccountsLoadBatch(context.Context, []service.AccountWithConcurrency) (map[int64]*service.AccountLoadInfo, error) {
	return map[int64]*service.AccountLoadInfo{}, nil
}
func (f *fakeConcurrencyCache) GetUsersLoadBatch(context.Context, []service.UserWithConcurrency) (map[int64]*service.UserLoadInfo, error) {
	return map[int64]*service.UserLoadInfo{}, nil
}
func (f *fakeConcurrencyCache) GetAccountConcurrencyBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		result[id] = 0
	}
	return result, nil
}
func (f *fakeConcurrencyCache) CleanupExpiredAccountSlots(context.Context, int64) error { return nil }
func (f *fakeConcurrencyCache) CleanupExpiredAccountSlotKeys(context.Context) error     { return nil }
func (f *fakeConcurrencyCache) CleanupStaleProcessSlots(context.Context, string) error  { return nil }

func newTestGatewayHandler(t *testing.T, group *service.Group, accounts []*service.Account, upstreamOpt ...service.HTTPUpstream) (*GatewayHandler, func()) {
	t.Helper()
	var upstream service.HTTPUpstream
	if len(upstreamOpt) > 0 {
		upstream = upstreamOpt[0]
	}

	schedulerCache := &fakeSchedulerCache{accounts: accounts}
	schedulerSnapshot := service.NewSchedulerSnapshotService(schedulerCache, nil, nil, nil, nil)

	gwSvc := service.NewGatewayService(
		nil, // accountRepo (not used: scheduler snapshot hit)
		&fakeGroupRepo{group: group},
		nil, // usageLogRepo
		nil, // usageBillingRepo
		nil, // userRepo
		nil, // userSubRepo
		nil, // userGroupRateRepo
		nil, // cache (disable sticky)
		nil, // cfg
		schedulerSnapshot,
		nil, // concurrencyService (disable load-aware; tryAcquire always acquired)
		nil, // billingService
		nil, // rateLimitService
		nil, // billingCacheService
		nil, // identityService
		upstream,
		nil, // deferredService
		nil, // claudeTokenProvider
		nil, // kiroTokenProvider
		nil, // kiroCooldownStore
		nil, // sessionLimitCache
		nil, // rpmCache
		nil, // modelCapacityCooldownCache
		nil, // digestStore
		nil, // settingService
		nil, // tlsFPProfileService
		nil, // channelService
		nil, // resolver
		nil, // compositeResolver
		nil, // balanceNotifyService
		nil, // userPlatformQuotaRepo
	)

	// RunModeSimple：跳过计费检查，避免引入 repo/cache 依赖。
	cfg := &config.Config{RunMode: config.RunModeSimple}
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)

	concurrencySvc := service.NewConcurrencyService(&fakeConcurrencyCache{})
	concurrencyHelper := NewConcurrencyHelper(concurrencySvc, SSEPingFormatClaude, 0)

	h := &GatewayHandler{
		gatewayService:      gwSvc,
		billingCacheService: billingCacheSvc,
		concurrencyHelper:   concurrencyHelper,
		// 这些字段对本测试不敏感，保持较小即可
		maxAccountSwitches:       1,
		maxAccountSwitchesGemini: 1,
	}

	cleanup := func() {
		billingCacheSvc.Stop()
	}
	return h, cleanup
}

type nativeOAuthHandlerUpstream struct {
	response *http.Response
	request  *http.Request
	body     []byte
	calls    int
}

type nativeOAuthHandlerSettingRepo struct {
	service.SettingRepository
}

func nativeOAuthHandlerHeaderValue(header http.Header, key string) string {
	for wireKey, values := range header {
		if strings.EqualFold(wireKey, key) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func (r *nativeOAuthHandlerSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = ""
	}
	return values, nil
}

func (u *nativeOAuthHandlerUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.DoWithTLS(req, "", 0, 0, nil)
}

func (u *nativeOAuthHandlerUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls++
	u.request = req
	if req != nil && req.Body != nil {
		u.body, _ = io.ReadAll(req.Body)
	}
	return u.response, nil
}

func TestGatewayHandlerMessages_AnthropicOAuthNativeErrorEventIsNotDuplicated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(2010)
	accountID := int64(1010)
	group := &service.Group{
		ID:       groupID,
		Hydrated: true,
		Platform: service.PlatformAnthropic,
		Status:   service.StatusActive,
	}
	account := &service.Account{
		ID:       accountID,
		Name:     "anthropic-native-handler",
		Platform: service.PlatformAnthropic,
		Type:     service.AccountTypeSetupToken,
		Credentials: map[string]any{
			"access_token":              "oauth-upstream-token",
			"intercept_warmup_requests": true,
		},
		Extra: map[string]any{
			"anthropic_oauth_passthrough": true,
		},
		Concurrency:   1,
		Priority:      1,
		Status:        service.StatusActive,
		Schedulable:   true,
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	upstreamSSE := "event: error\n" +
		"data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"upstream busy\"}}\n\n"
	upstream := &nativeOAuthHandlerUpstream{response: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"x-request-id": []string{"rid-native-handler"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account}, upstream)
	defer cleanup()
	h.cfg = &config.Config{RunMode: config.RunModeSimple}
	h.settingService = service.NewSettingService(&nativeOAuthHandlerSettingRepo{}, &config.Config{})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-opus-5","stream":true,"max_tokens":256,"messages":[{"role":"user","content":[{"type":"text","text":"Warmup"}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.220 (external, cli)")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	c.Request = req

	apiKey := &service.APIKey{
		ID: 3010, UserID: 4010, GroupID: &groupID, Status: service.StatusActive,
		User:  &service.User{ID: 4010, Concurrency: 10, Balance: 100},
		Group: group,
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

	h.Messages(c)

	require.Equal(t, upstreamSSE, rec.Body.String())
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
	require.NotContains(t, rec.Body.String(), "Upstream request failed")
	require.Equal(t, body, upstream.body)
	require.Equal(t, "Bearer oauth-upstream-token", nativeOAuthHandlerHeaderValue(upstream.request.Header, "Authorization"))
	require.Equal(t, "claude-cli/2.1.220 (external, cli)", nativeOAuthHandlerHeaderValue(upstream.request.Header, "User-Agent"))
	require.Equal(t, 1, upstream.calls, "native strict warmup must bypass the optional gateway mock")
	mode, _ := c.Get("anthropic_passthrough_mode")
	require.Equal(t, "native", mode)
}

func TestGatewayHandlerMessages_AnthropicOAuthNativeCompactionBypassesSyncGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(2011)
	accountID := int64(1011)
	group := &service.Group{
		ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic,
		Status: service.StatusActive, AllowNonStreamMessages: false,
	}
	account := &service.Account{
		ID: accountID, Name: "anthropic-native-compaction", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeSetupToken,
		Credentials: map[string]any{
			"access_token": "oauth-upstream-token",
		},
		Extra: map[string]any{
			"anthropic_oauth_passthrough": true,
		},
		Concurrency: 1, Priority: 1, Status: service.StatusActive, Schedulable: true,
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	upstreamBody := `{"type":"error","error":{"type":"upstream_test","message":"compaction reached upstream"}}`
	upstream := &nativeOAuthHandlerUpstream{response: &http.Response{
		StatusCode: http.StatusTeapot,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account}, upstream)
	defer cleanup()
	h.cfg = &config.Config{RunMode: config.RunModeSimple}
	h.settingService = service.NewSettingService(&nativeOAuthHandlerSettingRepo{}, &config.Config{})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-opus-5","stream":false,"max_tokens":1024,"system":[{"type":"text","text":"You are a helpful AI assistant tasked with summarizing conversations."}],"messages":[{"role":"user","content":"summarize"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.220 (external, cli)")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
	req.Header.Set("X-Stainless-Helper", "compaction")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	c.Request = req

	apiKey := &service.APIKey{
		ID: 3011, UserID: 4011, GroupID: &groupID, Status: service.StatusActive,
		User: &service.User{ID: 4011, Concurrency: 10, Balance: 100}, Group: group,
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

	h.Messages(c)

	require.Equal(t, 1, upstream.calls)
	require.Equal(t, body, upstream.body)
	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Equal(t, upstreamBody, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "Synchronous /v1/messages requests are not supported")
}

func TestGatewayHandlerCountTokens_AnthropicOAuthNativePreservesRequestAndErrorResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(2012)
	accountID := int64(1012)
	group := &service.Group{
		ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic,
		Status: service.StatusActive,
	}
	account := &service.Account{
		ID: accountID, Name: "anthropic-native-count-tokens", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeSetupToken,
		Credentials: map[string]any{
			"access_token": "oauth-upstream-token",
		},
		Extra: map[string]any{
			"anthropic_oauth_passthrough": true,
		},
		Concurrency: 1, Priority: 1, Status: service.StatusActive, Schedulable: true,
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	upstreamBody := `{"type":"error","error":{"type":"invalid_request_error","message":"native count error"},"extra":{"preserve":true}}`
	upstream := &nativeOAuthHandlerUpstream{response: &http.Response{
		StatusCode: http.StatusTeapot,
		Header: http.Header{
			"Content-Type": []string{"application/problem+json"},
			"x-request-id": []string{"rid-native-count-handler"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account}, upstream)
	defer cleanup()
	h.cfg = &config.Config{RunMode: config.RunModeSimple}
	h.settingService = service.NewSettingService(&nativeOAuthHandlerSettingRepo{}, &config.Config{})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"count this"}],"metadata":{"user_id":"user_01ARZ3NDEKTSV4RRFFQ69G5FAV_account__session_01ARZ3NDEKTSV4RRFFQ69G5FAV"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.220 (external, cli)")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
	req.Header.Set("X-Stainless-Helper", "compaction")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	c.Request = req

	apiKey := &service.APIKey{
		ID: 3012, UserID: 4012, GroupID: &groupID, Status: service.StatusActive,
		User: &service.User{ID: 4012, Concurrency: 10, Balance: 100}, Group: group,
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

	h.CountTokens(c)

	require.Equal(t, 1, upstream.calls)
	require.Equal(t, body, upstream.body)
	require.Equal(t, "Bearer oauth-upstream-token", nativeOAuthHandlerHeaderValue(upstream.request.Header, "Authorization"))
	require.Equal(t, "claude-cli/2.1.220 (external, cli)", nativeOAuthHandlerHeaderValue(upstream.request.Header, "User-Agent"))
	require.Equal(t, "compaction", nativeOAuthHandlerHeaderValue(upstream.request.Header, "X-Stainless-Helper"))
	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	require.Equal(t, upstreamBody, rec.Body.String())
}

func TestGatewayHandlerMessages_InterceptWarmup_AntigravityAccount_MixedSchedulingV1(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(2001)
	accountID := int64(1001)

	group := &service.Group{
		ID:       groupID,
		Hydrated: true,
		Platform: service.PlatformAnthropic, // /v1/messages（Claude兼容）入口
		Status:   service.StatusActive,
	}

	account := &service.Account{
		ID:       accountID,
		Name:     "ag-1",
		Platform: service.PlatformAntigravity,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":              "tok_xxx",
			"intercept_warmup_requests": true,
		},
		Extra: map[string]any{
			"mixed_scheduling": true, // 关键：允许被 anthropic 分组混合调度选中
		},
		Concurrency:   1,
		Priority:      1,
		Status:        service.StatusActive,
		Schedulable:   true,
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}

	h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account})
	defer cleanup()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body := []byte(`{
		"model": "claude-sonnet-4-5",
		"max_tokens": 256,
		"messages": [{"role":"user","content":[{"type":"text","text":"Warmup"}]}]
	}`)
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	c.Request = req

	apiKey := &service.APIKey{
		ID:      3001,
		UserID:  4001,
		GroupID: &groupID,
		Status:  service.StatusActive,
		User: &service.User{
			ID:          4001,
			Concurrency: 10,
			Balance:     100,
		},
		Group: group,
	}

	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

	h.Messages(c)

	require.Equal(t, 200, rec.Code)

	// 断言：确实选中了 antigravity 账号（不是纯函数测试，而是从 Handler 里验证调度结果）
	selected, ok := c.Get(opsAccountIDKey)
	require.True(t, ok)
	require.Equal(t, accountID, selected)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, strings.HasPrefix(resp["id"].(string), "msg_01"))
	require.Equal(t, "claude-sonnet-4-5", resp["model"])

	content, ok := resp["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	first, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "New Conversation", first["text"])
}

func TestGatewayHandlerMessages_InterceptWarmup_AntigravityAccount_ForcePlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(2002)
	accountID := int64(1002)

	group := &service.Group{
		ID:       groupID,
		Hydrated: true,
		Platform: service.PlatformAntigravity,
		Status:   service.StatusActive,
	}

	account := &service.Account{
		ID:       accountID,
		Name:     "ag-2",
		Platform: service.PlatformAntigravity,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":              "tok_xxx",
			"intercept_warmup_requests": true,
		},
		Concurrency:   1,
		Priority:      1,
		Status:        service.StatusActive,
		Schedulable:   true,
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}

	h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account})
	defer cleanup()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body := []byte(`{
		"model": "claude-sonnet-4-5",
		"max_tokens": 256,
		"messages": [{"role":"user","content":[{"type":"text","text":"Warmup"}]}]
	}`)
	req := httptest.NewRequest("POST", "/antigravity/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// 模拟 routes/gateway.go 里的 ForcePlatform 中间件效果：
	// - 写入 request.Context（Service读取）
	// - 写入 gin.Context（Handler快速读取）
	ctx := context.WithValue(req.Context(), ctxkey.Group, group)
	ctx = context.WithValue(ctx, ctxkey.ForcePlatform, service.PlatformAntigravity)
	req = req.WithContext(ctx)
	c.Request = req
	c.Set(string(middleware.ContextKeyForcePlatform), service.PlatformAntigravity)

	apiKey := &service.APIKey{
		ID:      3002,
		UserID:  4002,
		GroupID: &groupID,
		Status:  service.StatusActive,
		User: &service.User{
			ID:          4002,
			Concurrency: 10,
			Balance:     100,
		},
		Group: group,
	}

	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

	h.Messages(c)

	require.Equal(t, 200, rec.Code)

	selected, ok := c.Get(opsAccountIDKey)
	require.True(t, ok)
	require.Equal(t, accountID, selected)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, strings.HasPrefix(resp["id"].(string), "msg_01"))
	require.Equal(t, "claude-sonnet-4-5", resp["model"])
}
