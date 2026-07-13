package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestApplyOpsIdentityFieldsFromContext_PrefersAPIKeyAndFallsBackToSubject(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	accountID := int64(321)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.AccountID, accountID))
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 77, Concurrency: 3})

	groupID := int64(10)
	apiKey := &service.APIKey{
		ID:      55,
		UserID:  88,
		GroupID: &groupID,
		Group:   &service.Group{ID: groupID, Platform: service.PlatformAnthropic},
	}
	entry := &service.OpsInsertErrorLogInput{}

	applyOpsIdentityFieldsFromContext(c, entry, apiKey)

	require.NotNil(t, entry.APIKeyID)
	require.Equal(t, apiKey.ID, *entry.APIKeyID)
	require.NotNil(t, entry.UserID)
	require.Equal(t, apiKey.UserID, *entry.UserID)
	require.NotNil(t, entry.GroupID)
	require.Equal(t, groupID, *entry.GroupID)
	require.NotNil(t, entry.AccountID)
	require.Equal(t, accountID, *entry.AccountID)
	require.Equal(t, service.PlatformAnthropic, entry.Platform)

	entry = &service.OpsInsertErrorLogInput{}
	applyOpsIdentityFieldsFromContext(c, entry, nil)
	require.Nil(t, entry.APIKeyID)
	require.NotNil(t, entry.UserID)
	require.Equal(t, int64(77), *entry.UserID)
	require.NotNil(t, entry.AccountID)
	require.Equal(t, accountID, *entry.AccountID)
}

func resetOpsErrorLoggerStateForTest(t *testing.T) {
	t.Helper()

	opsErrorLogMu.Lock()
	ch := opsErrorLogQueue
	opsErrorLogQueue = nil
	opsErrorLogStopping = true
	opsErrorLogMu.Unlock()

	if ch != nil {
		close(ch)
	}
	opsErrorLogWorkersWg.Wait()

	opsErrorLogOnce = sync.Once{}
	opsErrorLogStopOnce = sync.Once{}
	opsErrorLogWorkersWg = sync.WaitGroup{}
	opsErrorLogMu = sync.RWMutex{}
	opsErrorLogStopping = false

	opsErrorLogQueueLen.Store(0)
	opsErrorLogEnqueued.Store(0)
	opsErrorLogDropped.Store(0)
	opsErrorLogProcessed.Store(0)
	opsErrorLogSanitized.Store(0)
	opsErrorLogLastDropLogAt.Store(0)

	opsErrorLogShutdownCh = make(chan struct{})
	opsErrorLogShutdownOnce = sync.Once{}
	opsErrorLogDrained.Store(false)
}

func TestEnqueueOpsErrorLog_QueueFullDrop(t *testing.T) {
	resetOpsErrorLoggerStateForTest(t)

	// 禁止 enqueueOpsErrorLog 触发 workers，使用测试队列验证满队列降级。
	opsErrorLogOnce.Do(func() {})

	opsErrorLogMu.Lock()
	opsErrorLogQueue = make(chan opsErrorLogJob, 1)
	opsErrorLogMu.Unlock()

	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	entry := &service.OpsInsertErrorLogInput{ErrorPhase: "upstream", ErrorType: "upstream_error"}

	enqueueOpsErrorLog(ops, entry)
	enqueueOpsErrorLog(ops, entry)

	require.Equal(t, int64(1), OpsErrorLogEnqueuedTotal())
	require.Equal(t, int64(1), OpsErrorLogDroppedTotal())
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
}

func TestShouldSkipOpsErrorLog_UsesUpstreamContextText(t *testing.T) {
	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	skip := shouldSkipOpsErrorLog(
		context.Background(),
		ops,
		"Upstream request failed",
		`{"error":{"message":"Upstream request failed"}}`,
		"/v1/chat/completions",
		`Post "https://chatgpt.com/backend-api/codex/responses": context canceled`,
	)

	require.True(t, skip)
}

func TestSetOpsRequestContext_AcceptsOptionalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	raw := []byte(`{"model":"claude-3","messages":[]}`)

	setOpsRequestContext(c, "claude-3", true, raw)

	model, ok := c.Get(opsModelKey)
	require.True(t, ok)
	require.Equal(t, "claude-3", model)
	stream, ok := c.Get(opsStreamKey)
	require.True(t, ok)
	require.Equal(t, true, stream)
	body, ok := c.Get(opsRequestBodyKey)
	require.True(t, ok)
	require.Equal(t, raw, body)
	require.Equal(t, "claude-3", c.Request.Context().Value(ctxkey.Model))
}

func TestEnqueueOpsErrorLog_EarlyReturnBranches(t *testing.T) {
	resetOpsErrorLoggerStateForTest(t)

	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	entry := &service.OpsInsertErrorLogInput{ErrorPhase: "upstream", ErrorType: "upstream_error"}

	// nil 入参分支
	enqueueOpsErrorLog(nil, entry)
	enqueueOpsErrorLog(ops, nil)
	require.Equal(t, int64(0), OpsErrorLogEnqueuedTotal())

	// shutdown 分支
	close(opsErrorLogShutdownCh)
	enqueueOpsErrorLog(ops, entry)
	require.Equal(t, int64(0), OpsErrorLogEnqueuedTotal())

	// stopping 分支
	resetOpsErrorLoggerStateForTest(t)
	opsErrorLogMu.Lock()
	opsErrorLogStopping = true
	opsErrorLogMu.Unlock()
	enqueueOpsErrorLog(ops, entry)
	require.Equal(t, int64(0), OpsErrorLogEnqueuedTotal())

	// queue nil 分支（防止启动 worker 干扰）
	resetOpsErrorLoggerStateForTest(t)
	opsErrorLogOnce.Do(func() {})
	opsErrorLogMu.Lock()
	opsErrorLogQueue = nil
	opsErrorLogMu.Unlock()
	enqueueOpsErrorLog(ops, entry)
	require.Equal(t, int64(0), OpsErrorLogEnqueuedTotal())
}

func TestOpsCaptureWriterPool_ResetOnRelease(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	writer := acquireOpsCaptureWriter(c.Writer)
	require.NotNil(t, writer)
	_, err := writer.buf.WriteString("temp-error-body")
	require.NoError(t, err)

	releaseOpsCaptureWriter(writer)

	reused := acquireOpsCaptureWriter(c.Writer)
	defer releaseOpsCaptureWriter(reused)

	require.Zero(t, reused.buf.Len(), "writer should be reset before reuse")
}

func TestOpsErrorLoggerMiddleware_DoesNotBreakOuterMiddlewares(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware2.Recovery())
	r.Use(middleware2.RequestLogger())
	r.Use(middleware2.Logger())
	r.GET("/v1/messages", OpsErrorLoggerMiddleware(nil), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)

	require.NotPanics(t, func() {
		r.ServeHTTP(rec, req)
	})
	require.Equal(t, http.StatusNoContent, rec.Code)
}

// setupOpsErrorLogTestQueue 阻止 enqueueOpsErrorLog 启动真实 worker，改用可检查的测试队列。
func setupOpsErrorLogTestQueue(t *testing.T, size int) {
	t.Helper()
	resetOpsErrorLoggerStateForTest(t)
	opsErrorLogOnce.Do(func() {})
	opsErrorLogMu.Lock()
	opsErrorLogQueue = make(chan opsErrorLogJob, size)
	opsErrorLogMu.Unlock()
}

// 就地(in-band) SSE 错误挂在已固化的 HTTP 200 流上：wire 状态码为 200，
// 常规 status>=400 采集路径不会触发。logOpsStreamError 必须据 MarkOpsStreamError
// 补记一条错误日志，且用 IntendedStatus(429) 分级、StatusCode 仍记 wire 的 200。
func TestLogOpsStreamError_RecordsInBandConcurrencyLimit(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Set(opsModelKey, "test-model")

	service.MarkOpsStreamError(c, "rate_limit_error",
		"Concurrency limit exceeded for account, please retry later", http.StatusTooManyRequests)

	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	logOpsStreamError(c, ops, http.StatusOK)

	require.Equal(t, int64(1), OpsErrorLogEnqueuedTotal())
	require.Equal(t, int64(1), OpsErrorLogQueueLength())

	job := <-opsErrorLogQueue
	require.NotNil(t, job.entry)
	require.Equal(t, "rate_limit_error", job.entry.ErrorType)
	require.Equal(t, "request", job.entry.ErrorPhase)
	require.True(t, job.entry.IsBusinessLimited)
	require.True(t, job.entry.Stream)
	require.Equal(t, http.StatusOK, job.entry.StatusCode) // wire 状态码保持 200
	require.Equal(t, "P1", job.entry.Severity)            // 用 IntendedStatus 429 分级
	require.Equal(t, "test-model", job.entry.Model)
	require.Equal(t, "Concurrency limit exceeded for account, please retry later", job.entry.ErrorMessage)
}

// 未标记流内错误时 logOpsStreamError 必须是 no-op（不误记正常的 200 流）。
func TestLogOpsStreamError_NoopWhenNotMarked(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	logOpsStreamError(c, ops, http.StatusOK)

	require.Equal(t, int64(0), OpsErrorLogEnqueuedTotal())
}

// 命中 skip_monitoring=true 透传规则时不落库，与其它采集分支一致。
func TestLogOpsStreamError_SkipWhenPassthroughSkipMonitoring(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	service.MarkOpsStreamError(c, "upstream_error", "Upstream request failed", http.StatusBadGateway)
	c.Set(service.OpsSkipPassthroughKey, true)

	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	logOpsStreamError(c, ops, http.StatusOK)

	require.Equal(t, int64(0), OpsErrorLogEnqueuedTotal())
}

// MarkOpsStreamError 采用「首个标记生效」：后续的通用兜底帧不得覆盖根因错误。
func TestMarkOpsStreamError_FirstWins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	service.MarkOpsStreamError(c, "rate_limit_error", "Concurrency limit exceeded for account", http.StatusTooManyRequests)
	service.MarkOpsStreamError(c, "upstream_error", "Upstream request failed", http.StatusBadGateway)

	se, ok := service.GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "rate_limit_error", se.ErrType)
	require.Equal(t, "Concurrency limit exceeded for account", se.Message)
	require.Equal(t, http.StatusTooManyRequests, se.IntendedStatus)
}

func TestIsKnownOpsErrorType(t *testing.T) {
	known := []string{
		"invalid_request_error",
		"authentication_error",
		"rate_limit_error",
		"billing_error",
		"subscription_error",
		"upstream_error",
		"overloaded_error",
		"api_error",
		"not_found_error",
		"forbidden_error",
	}
	for _, k := range known {
		require.True(t, isKnownOpsErrorType(k), "expected known: %s", k)
	}

	unknown := []string{"<nil>", "null", "", "random_error", "some_new_type", "<nil>\u003e"}
	for _, u := range unknown {
		require.False(t, isKnownOpsErrorType(u), "expected unknown: %q", u)
	}
}

func TestNormalizeOpsErrorType(t *testing.T) {
	tests := []struct {
		name    string
		errType string
		code    string
		want    string
	}{
		// Known types pass through.
		{"known invalid_request_error", "invalid_request_error", "", "invalid_request_error"},
		{"known rate_limit_error", "rate_limit_error", "", "rate_limit_error"},
		{"known upstream_error", "upstream_error", "", "upstream_error"},

		// Unknown/garbage types are rejected and fall through to code-based or default.
		{"nil literal from upstream", "<nil>", "", "api_error"},
		{"null string", "null", "", "api_error"},
		{"random string", "something_weird", "", "api_error"},

		// Generic api_error should still allow business-limit codes to refine classification.
		{"api_error with balance code", "api_error", "INSUFFICIENT_BALANCE", "billing_error"},
		{"api_error with subscription code", "api_error", "SUBSCRIPTION_NOT_FOUND", "subscription_error"},
		{"api_error with api key quota code", "api_error", "API_KEY_QUOTA_EXHAUSTED", "subscription_error"},
		{"api_error with api key rate window code", "api_error", "API_KEY_RATE_1D_EXCEEDED", "subscription_error"},

		// Unknown type but known code still maps correctly.
		{"nil with INSUFFICIENT_BALANCE code", "<nil>", "INSUFFICIENT_BALANCE", "billing_error"},
		{"nil with USAGE_LIMIT_EXCEEDED code", "<nil>", "USAGE_LIMIT_EXCEEDED", "subscription_error"},

		// Empty type falls through to code-based mapping.
		{"empty type with balance code", "", "INSUFFICIENT_BALANCE", "billing_error"},
		{"empty type with subscription code", "", "SUBSCRIPTION_NOT_FOUND", "subscription_error"},
		{"empty type no code", "", "", "api_error"},

		// Known type overrides conflicting code-based mapping.
		{"known type overrides conflicting code", "rate_limit_error", "INSUFFICIENT_BALANCE", "rate_limit_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOpsErrorType(tt.errType, tt.code)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestClassifyOpsPhase_APIKeyBusinessLimitCodes(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"quota exhausted", "API_KEY_QUOTA_EXHAUSTED"},
		{"generic api key rate limit", "API_KEY_RATE_LIMITED"},
		{"5h rate limit", "API_KEY_RATE_5H_EXCEEDED"},
		{"1d rate limit", "API_KEY_RATE_1D_EXCEEDED"},
		{"7d rate limit", "API_KEY_RATE_7D_EXCEEDED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, "request", classifyOpsPhase("api_error", "API key 额度已用完", tt.code))
		})
	}
}

func TestClassifyOpsIsBusinessLimited_APIKeyBusinessLimitCodes(t *testing.T) {
	tests := []string{
		"API_KEY_QUOTA_EXHAUSTED",
		"API_KEY_RATE_LIMITED",
		"API_KEY_RATE_5H_EXCEEDED",
		"API_KEY_RATE_1D_EXCEEDED",
		"API_KEY_RATE_7D_EXCEEDED",
	}

	for _, code := range tests {
		t.Run(code, func(t *testing.T) {
			require.True(t, classifyOpsIsBusinessLimited("subscription_error", "request", code, http.StatusTooManyRequests, "API key 额度已用完"))
		})
	}
}

func TestClassifyOpsModelNotFoundIsClientRequestLimited(t *testing.T) {
	msg := "model: claude-opus-4-7-thinking"

	phase := classifyOpsPhase("not_found_error", msg, "")
	require.Equal(t, "request", phase)
	require.True(t, classifyOpsIsBusinessLimited("not_found_error", phase, "", http.StatusNotFound, msg))
	require.Equal(t, "client", classifyOpsErrorOwner(phase, msg))
	require.Equal(t, "client_request", classifyOpsErrorSource(phase, msg))
	require.False(t, classifyOpsIsRetryable("not_found_error", http.StatusNotFound))
}

func TestClassifyOpsClaudeCodeOnlyRestrictionIsClientBusinessLimited(t *testing.T) {
	tests := []string{
		"No available accounts: this group only allows Claude Code clients",
		"This group is restricted to Claude Code clients (/v1/messages only)",
	}

	for _, msg := range tests {
		t.Run(msg, func(t *testing.T) {
			phase := classifyOpsPhase("api_error", msg, "")
			require.Equal(t, "request", phase)
			require.True(t, classifyOpsIsBusinessLimited("api_error", phase, "", http.StatusServiceUnavailable, msg))
			require.Equal(t, "client", classifyOpsErrorOwner(phase, msg))
			require.Equal(t, "client_request", classifyOpsErrorSource(phase, msg))
		})
	}
}

func TestClassifyOpsLocalRateLimitsAreClientBusinessLimited(t *testing.T) {
	tests := []string{
		"group requests-per-minute limit exceeded",
		"user requests-per-minute limit exceeded",
		"Concurrency limit exceeded for user, please retry later",
		"Concurrency limit exceeded for account, please retry later",
		"Request queue is busy, please retry later",
		"Request queue wait timeout, please retry later",
	}

	for _, msg := range tests {
		t.Run(msg, func(t *testing.T) {
			phase := classifyOpsPhase("api_error", msg, "")
			require.Equal(t, "request", phase)
			require.True(t, classifyOpsIsBusinessLimited("api_error", phase, "", http.StatusTooManyRequests, msg))
			require.Equal(t, "client", classifyOpsErrorOwner(phase, msg))
			require.Equal(t, "client_request", classifyOpsErrorSource(phase, msg))
		})
	}
}

func TestClassifyOpsUpstreamRateLimitRemainsSLAError(t *testing.T) {
	msg := "Upstream rate limit exceeded, please retry later"

	phase := classifyOpsPhase("rate_limit_error", msg, "")
	require.Equal(t, "upstream", phase)
	require.False(t, classifyOpsIsBusinessLimited("rate_limit_error", phase, "", http.StatusTooManyRequests, msg))
	require.Equal(t, "provider", classifyOpsErrorOwner(phase, msg))
	require.Equal(t, "upstream_http", classifyOpsErrorSource(phase, msg))
}

func TestClassifyOpsUpstreamConfirmedClientContextLimitIsExcludedFromSLA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	service.SetOpsUpstreamError(c, http.StatusBadRequest, "maximum prompt length exceeded", "")
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonContextLimit)

	phase, limited, owner, source := classifyOpsErrorLog(
		c,
		"invalid_request_error",
		"prompt is too long",
		"context_length_exceeded",
		http.StatusBadRequest,
	)

	require.Equal(t, "request", phase)
	require.True(t, limited)
	require.Equal(t, "client", owner)
	require.Equal(t, "client_request", source)
}

func TestClassifyOpsMarkedTransportFailureAsNetwork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	service.SetOpsUpstreamError(c, 0, `Post "https://example.invalid": Service Unavailable`, "network_error_type=proxy_connect")
	service.MarkOpsNetworkError(c, "proxy_connect")

	phase, limited, owner, source := classifyOpsErrorLog(
		c,
		"upstream_error",
		"Upstream request failed",
		"",
		http.StatusBadGateway,
	)

	require.Equal(t, "network", phase)
	require.False(t, limited)
	require.Equal(t, "provider", owner)
	require.Equal(t, "gateway", source)
	entry := &service.OpsInsertErrorLogInput{}
	applyOpsNetworkFieldsFromContext(c, entry)
	require.Equal(t, "proxy_connect", entry.NetworkErrorType)
}

func TestSetOpsEndpointContext_SetsContextKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	setOpsEndpointContext(c, "claude-3-5-sonnet-20241022", int16(2)) // stream

	v, ok := c.Get(opsUpstreamModelKey)
	require.True(t, ok)
	vStr, ok := v.(string)
	require.True(t, ok)
	require.Equal(t, "claude-3-5-sonnet-20241022", vStr)

	rt, ok := c.Get(opsRequestTypeKey)
	require.True(t, ok)
	rtVal, ok := rt.(int16)
	require.True(t, ok)
	require.Equal(t, int16(2), rtVal)
}

func TestSetOpsEndpointContext_EmptyModelNotStored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	setOpsEndpointContext(c, "", int16(1))

	_, ok := c.Get(opsUpstreamModelKey)
	require.False(t, ok, "empty upstream model should not be stored")

	rt, ok := c.Get(opsRequestTypeKey)
	require.True(t, ok)
	rtVal, ok := rt.(int16)
	require.True(t, ok)
	require.Equal(t, int16(1), rtVal)
}

func TestSetOpsEndpointContext_NilContext(t *testing.T) {
	require.NotPanics(t, func() {
		setOpsEndpointContext(nil, "model", int16(1))
	})
}

func TestGetOpsAPIKeyFallsBackToOpsFallbackKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// 主 key 缺席（鉴权早退场景）：返回 nil。
	require.Nil(t, getOpsAPIKey(c))

	// 写入 ops 专用 fallback key 后应能取到，且带齐 user/group。
	groupID := int64(55)
	apiKey := &service.APIKey{
		ID:      100,
		GroupID: &groupID,
		User:    &service.User{ID: 7},
		Group:   &service.Group{ID: groupID, Platform: service.PlatformAnthropic},
	}
	c.Set(string(middleware2.ContextKeyOpsFallbackAPIKey), apiKey)

	got := getOpsAPIKey(c)
	require.NotNil(t, got)
	require.Equal(t, int64(100), got.ID)
	require.NotNil(t, got.User)
	require.Equal(t, int64(7), got.User.ID)
	require.NotNil(t, got.Group)
	require.Equal(t, service.PlatformAnthropic, got.Group.Platform)
}

func TestGetOpsAPIKeyPrefersPrimaryContextKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	primary := &service.APIKey{ID: 1}
	fallback := &service.APIKey{ID: 2}
	c.Set(string(middleware2.ContextKeyAPIKey), primary)
	c.Set(string(middleware2.ContextKeyOpsFallbackAPIKey), fallback)

	got := getOpsAPIKey(c)
	require.NotNil(t, got)
	require.Equal(t, int64(1), got.ID, "已鉴权请求应优先使用正式 api key")
}
