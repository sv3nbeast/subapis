package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsCaptureWriterIsRestoredAfterCompactKeepalive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	outerStatus := 0
	router.Use(func(c *gin.Context) {
		c.Next()
		outerStatus = c.Writer.Status()
	})
	router.Use(OpsErrorLoggerMiddleware(nil))
	router.GET("/compact", func(c *gin.Context) {
		service.MarkOpenAICompactClientStream(c)
		stop := service.StartOpenAICompactSSEKeepalive(c, time.Hour)
		defer stop()
		c.Status(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/compact", nil)
	require.NotPanics(t, func() { router.ServeHTTP(rec, req) })
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, http.StatusNoContent, outerStatus)
}

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

func TestKiroContentProcessingFailureClassifiesAsNonRetryableUpstream(t *testing.T) {
	const errType = "upstream_content_processing_failed"
	require.True(t, isKnownOpsErrorType(errType))
	require.Equal(t, "upstream", classifyOpsPhase(errType, service.KiroUpstreamContentProcessingFailedClientMessage, ""))
	require.False(t, classifyOpsIsRetryable(errType, http.StatusUnprocessableEntity))
	require.Equal(t, "P2", classifyOpsSeverity(errType, http.StatusUnprocessableEntity))
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
	opsErrorLogQueueBytes.Store(0)
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

func TestOpsAccountAttribution_KiroCommitsOnlyWhenAttemptStarts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	previous := &service.Account{ID: 1945, Platform: service.PlatformKiro}
	candidate := &service.Account{ID: 1960, Platform: service.PlatformKiro}

	setOpsKiroAttemptedAccount(c, previous)
	setOpsSelectedAccountBeforeAttempt(c, candidate)

	accountID, exists := c.Get(opsAccountIDKey)
	require.True(t, exists)
	require.Equal(t, previous.ID, accountID)
	require.Equal(t, previous.ID, c.Request.Context().Value(ctxkey.AccountID))
	require.Equal(t, service.PlatformKiro, c.Request.Context().Value(ctxkey.Platform))

	setOpsKiroAttemptedAccount(c, candidate)

	accountID, exists = c.Get(opsAccountIDKey)
	require.True(t, exists)
	require.Equal(t, candidate.ID, accountID)
	require.Equal(t, candidate.ID, c.Request.Context().Value(ctxkey.AccountID))
	require.Equal(t, service.PlatformKiro, c.Request.Context().Value(ctxkey.Platform))

	noPreviousRecorder := httptest.NewRecorder()
	noPrevious, _ := gin.CreateTestContext(noPreviousRecorder)
	noPrevious.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	setOpsSelectedAccountBeforeAttempt(noPrevious, candidate)
	_, exists = noPrevious.Get(opsAccountIDKey)
	require.False(t, exists)
	require.Nil(t, noPrevious.Request.Context().Value(ctxkey.AccountID))
}

func TestOpsAccountAttribution_NonKiroCommitsWhenSelected(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	account := &service.Account{ID: 77, Platform: service.PlatformAnthropic}

	setOpsSelectedAccountBeforeAttempt(c, account)

	accountID, exists := c.Get(opsAccountIDKey)
	require.True(t, exists)
	require.Equal(t, account.ID, accountID)
	require.Equal(t, account.ID, c.Request.Context().Value(ctxkey.AccountID))
	require.Equal(t, service.PlatformAnthropic, c.Request.Context().Value(ctxkey.Platform))
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
	c.Writer.WriteHeader(http.StatusInternalServerError)
	_, err := writer.WriteString("temp-error-body")
	require.NoError(t, err)
	require.NotEmpty(t, writer.capturedBytes())

	releaseOpsCaptureWriter(writer)

	reused := acquireOpsCaptureWriter(c.Writer)
	defer releaseOpsCaptureWriter(reused)

	require.Empty(t, reused.capturedBytes(), "writer should be reset before reuse")
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

func TestShouldSkipFinalOpsFailureUsesOnlyFinalAttemptRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{
		{UpstreamStatusCode: http.StatusBadGateway, Message: "hidden intermediate", SkipMonitoring: true},
		{UpstreamStatusCode: http.StatusServiceUnavailable, Message: "visible final"},
	})
	require.False(t, shouldSkipFinalOpsFailure(c))

	c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{
		{UpstreamStatusCode: http.StatusBadGateway, Message: "visible intermediate"},
		nil,
		{UpstreamStatusCode: http.StatusServiceUnavailable, Message: "hidden final", SkipMonitoring: true},
	})
	require.True(t, shouldSkipFinalOpsFailure(c))
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

func TestLogOpsStreamError_RecordsOneFailurePerWebSocketTurn(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	service.SetOpenAIClientTransport(c, service.OpenAIClientTransportWS)

	service.BeginOpsStreamTurn(c, 1)
	service.MarkOpsStreamFailure(c, "rate_limit_error", "rate_limit_exceeded", "turn one failed", http.StatusTooManyRequests)
	service.MarkOpsStreamError(c, "upstream_error", "generic duplicate for turn one", http.StatusBadGateway)
	service.BeginOpsStreamTurn(c, 2)
	service.MarkOpsStreamFailure(c, "permission_error", "permission_denied", "turn two failed", http.StatusForbidden)

	streamErrors := service.GetOpsStreamErrors(c)
	require.Len(t, streamErrors, 2)
	require.Equal(t, 1, streamErrors[0].Turn)
	require.Equal(t, 2, streamErrors[1].Turn)
	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	logOpsStreamError(c, ops, http.StatusSwitchingProtocols)

	require.Equal(t, int64(2), OpsErrorLogQueueLength())
	first := <-opsErrorLogQueue
	second := <-opsErrorLogQueue
	require.Equal(t, "turn one failed", first.entry.ErrorMessage)
	require.Equal(t, http.StatusTooManyRequests, first.entry.StatusCode)
	require.Equal(t, "turn two failed", second.entry.ErrorMessage)
	require.Equal(t, http.StatusForbidden, second.entry.StatusCode)
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
		{"legacy model_not_found", "model_not_found", "", "not_found_error"},
		{"explicit business code wins over legacy model_not_found", "model_not_found", "INSUFFICIENT_BALANCE", "billing_error"},

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

func TestClassifyOpsRoutingCapacityIsSLAError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	markOpsRoutingCapacityLimited(c)

	phase, limited, owner, source := classifyOpsErrorLog(
		c,
		"api_error",
		"Service temporarily unavailable",
		"",
		http.StatusServiceUnavailable,
	)

	require.Equal(t, "routing", phase)
	require.False(t, limited)
	require.Equal(t, "platform", owner)
	require.Equal(t, "gateway", source)
}

type opsErrorLogSettingsRepoProbe struct {
	service.SettingRepository
	sawCanceled bool
}

func (r *opsErrorLogSettingsRepoProbe) GetValue(ctx context.Context, key string) (string, error) {
	r.sawCanceled = ctx != nil && ctx.Err() != nil
	if key != service.SettingKeyOpsAdvancedSettings {
		return "", service.ErrSettingNotFound
	}
	raw, err := json.Marshal(map[string]any{"ignore_context_canceled": true})
	return string(raw), err
}

func (r *opsErrorLogSettingsRepoProbe) Set(context.Context, string, string) error {
	return nil
}

func TestShouldSkipOpsErrorLog_UsesDetachedContextAfterClientCancel(t *testing.T) {
	repo := &opsErrorLogSettingsRepoProbe{}
	ops := service.NewOpsService(nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.True(t, shouldSkipOpsErrorLog(ctx, ops, "context canceled", "", "/v1/messages"))
	require.False(t, repo.sawCanceled, "settings lookup must not inherit a canceled request context")
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

func TestClassifyOpsGatewayTimeoutAsPlatformNetworkError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	service.SetOpsUpstreamError(c, 0, "Kiro gateway response header timeout", "network_error_type=response_header_timeout")
	service.MarkOpsNetworkError(c, "response_header_timeout")

	phase, limited, owner, source := classifyOpsErrorLog(
		c,
		"upstream_error",
		"Upstream service temporarily unavailable",
		"",
		http.StatusServiceUnavailable,
	)

	require.Equal(t, "network", phase)
	require.False(t, limited)
	require.Equal(t, "platform", owner)
	require.Equal(t, "gateway", source)
}

func TestParseOpsErrorResponsePreservesStructuredTopLevelSemantics(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantType string
		wantCode string
		wantMsg  string
	}{
		{
			name:     "model not found",
			body:     `{"type":"model_not_found","code":404,"message":"model unavailable"}`,
			wantType: "model_not_found",
			wantCode: "404",
			wantMsg:  "model unavailable",
		},
		{
			name:     "string error",
			body:     `{"type":"service_unavailable","code":"temporarily_unavailable","error":"capacity exhausted"}`,
			wantType: "service_unavailable",
			wantCode: "temporarily_unavailable",
			wantMsg:  "capacity exhausted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseOpsErrorResponse([]byte(tt.body))
			require.Equal(t, tt.wantType, normalizeOpsErrorType(parsed.ErrorType, parsed.Code))
			require.Equal(t, tt.wantCode, parsed.Code)
			require.Equal(t, tt.wantMsg, parsed.Message)
		})
	}
}

func TestApplyOpsUpstreamFieldsUsesLastNonNilAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	service.SetOpsUpstreamError(c, http.StatusUnauthorized, "stale context", "stale detail")
	c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{
		{UpstreamStatusCode: http.StatusTooManyRequests, Message: "first attempt", Detail: "first detail"},
		nil,
		{UpstreamStatusCode: http.StatusServiceUnavailable, Message: "final attempt", Detail: "final detail"},
		nil,
	})
	entry := &service.OpsInsertErrorLogInput{}

	applyOpsUpstreamFieldsFromContext(c, entry)

	require.NotNil(t, entry.UpstreamStatusCode)
	require.Equal(t, http.StatusServiceUnavailable, *entry.UpstreamStatusCode)
	require.NotNil(t, entry.UpstreamErrorMessage)
	require.Equal(t, "final attempt", *entry.UpstreamErrorMessage)
	require.NotNil(t, entry.UpstreamErrorDetail)
	require.Equal(t, "final detail", *entry.UpstreamErrorDetail)
	require.Len(t, entry.UpstreamErrors, 4)
}

func TestApplyOpsUpstreamFieldsFinalStatuslessAttemptClearsStaleContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	service.SetOpsUpstreamError(c, http.StatusBadGateway, "stale response", "stale body")
	c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{
		{UpstreamStatusCode: http.StatusBadGateway, Message: "first response"},
		{Kind: "request_error", Message: "final transport failure", Detail: "connection reset"},
	})
	entry := &service.OpsInsertErrorLogInput{}

	applyOpsUpstreamFieldsFromContext(c, entry)

	require.Nil(t, entry.UpstreamStatusCode)
	require.NotNil(t, entry.UpstreamErrorMessage)
	require.Equal(t, "final transport failure", *entry.UpstreamErrorMessage)
	require.NotNil(t, entry.UpstreamErrorDetail)
	require.Equal(t, "connection reset", *entry.UpstreamErrorDetail)
}

func TestOpsCaptureWriter_ProtocolLevelTerminalFrameDetection(t *testing.T) {
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	chunks := []string{
		"event : response.failed\r\n",
		"data: { \"response\" : { \"error\" : { \"message\" : \"busy\", \"code\" : \"service_unavailable\" } },",
		" \"type\" : \"response.failed\" }\r\n\r\n",
	}
	for _, chunk := range chunks {
		state.captureResponseChunk([]byte(chunk), http.StatusOK)
	}

	parsed := parseOpsErrorResponse(state.buf.Bytes())
	require.True(t, state.sseCapturing)
	require.True(t, parsed.StreamFailure)
	require.Equal(t, "service_unavailable_error", parsed.ErrorType)
	require.Equal(t, "service_unavailable", parsed.Code)
	require.Equal(t, "busy", parsed.Message)
	require.Equal(t, http.StatusServiceUnavailable, inferStreamFailureStatus(nil, parsed))
}

func TestParseOpsSSEFailure_TopLevelErrorsAndUnknownStatus(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantType   string
		wantStatus int
	}{
		{
			name:       "top-level permission",
			body:       "event: error\ndata: {\"message\":\"denied\",\"code\":\"permission_denied\",\"type\":\"error\"}\n\n",
			wantType:   "permission_error",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "top-level unavailable",
			body:       "data: {\"message\":\"busy\",\"type\":\"error\",\"code\":\"service_unavailable\"}\n\n",
			wantType:   "service_unavailable_error",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "unknown terminal",
			body:       "event: response.failed\ndata: {\"type\":\"response.failed\",\"error\":{\"code\":\"new_provider_code\",\"message\":\"failed\"}}\n\n",
			wantType:   "upstream_error",
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "explicit terminal status",
			body:       "event: error\ndata: {\"type\":\"error\",\"status_code\":429,\"code\":\"new_rate_code\",\"message\":\"slow down\"}\n\n",
			wantType:   "api_error",
			wantStatus: http.StatusTooManyRequests,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseOpsErrorResponse([]byte(tt.body))
			require.True(t, parsed.StreamFailure)
			require.Equal(t, tt.wantType, parsed.ErrorType)
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			service.SetOpsUpstreamError(c, http.StatusUnauthorized, "old attempt", "")
			require.Equal(t, tt.wantStatus, inferStreamFailureStatus(c, parsed), "terminal status must not inherit an earlier attempt")
		})
	}
}

func TestOpsCaptureWriter_OversizedNonTerminalFrameRemainsBounded(t *testing.T) {
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	state.captureResponseChunk([]byte("data: "+strings.Repeat("x", opsTerminalSSEFrameProbeLimit*2)+"\n\n"), http.StatusOK)
	require.Empty(t, state.buf.Bytes())
	require.LessOrEqual(t, cap(state.probe), opsTerminalSSEFrameProbeLimit)

	state.captureResponseChunk([]byte("event: error\ndata: {\"type\":\"error\",\"code\":\"permission_denied\",\"message\":\"denied\"}\n\n"), http.StatusOK)
	require.True(t, state.sseCapturing)
	require.NotEmpty(t, state.buf.Bytes())
}

func TestOpsCaptureWriter_TerminalMetadataSurvivesBodyCaptureTruncation(t *testing.T) {
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	frame := "event: response.failed\ndata: {\"padding\":\"" + strings.Repeat("x", opsCaptureWriterLimit) + "\",\"type\":\"response.failed\",\"error\":{\"code\":\"service_unavailable\",\"message\":\"busy\"}}\n\n"
	state.captureResponseChunk([]byte(frame), http.StatusOK)
	state.finalizeResponseCapture()

	require.Len(t, state.buf.Bytes(), opsCaptureWriterLimit)
	require.True(t, parseOpsErrorResponse(state.buf.Bytes()).StreamFailure, "the bounded parser must fail closed from the terminal event line")
	require.True(t, state.terminalFound)
	require.Equal(t, "service_unavailable_error", state.terminalError.ErrorType)
	require.Equal(t, "busy", state.terminalError.Message)
}

func TestOpsErrorLoggerMiddleware_LargeTerminalFrameUsesEventFallback(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)
	gin.SetMode(gin.TestMode)
	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString("event: response.failed\n")
		_, _ = c.Writer.WriteString("data: {\"authorization\":\"Bearer must-not-persist\",\"padding\":\"" + strings.Repeat("x", opsTerminalSSEFrameProbeLimit*2) + "\"}")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusBadGateway, job.entry.StatusCode)
	require.Equal(t, "upstream_error", job.entry.ErrorType)
	require.Equal(t, "upstream stream failed", job.entry.ErrorMessage)
	require.NotContains(t, job.entry.ErrorMessage, "must-not-persist")
	require.NotContains(t, job.entry.ErrorBody, "must-not-persist")
	require.Contains(t, job.entry.ErrorBody, `"payload_truncated":true`)
}

func TestOpsErrorLoggerMiddleware_DetectsTerminalDataAtEOFWithoutBlankLine(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)
	gin.SetMode(gin.TestMode)
	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString(`data: {"message":"denied","code":"permission_denied","type":"error"}`)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusForbidden, job.entry.StatusCode)
	require.Equal(t, "permission_error", job.entry.ErrorType)
	require.Equal(t, "denied", job.entry.ErrorMessage)
}

func TestOpsCaptureWriter_DetectsCROnlySSEFrame(t *testing.T) {
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	state.captureResponseChunk([]byte("data: {\"type\":\"error\",\"code\":\"service_unavailable\",\"message\":\"busy\"}\r\r"), http.StatusOK)

	require.True(t, state.sseCapturing)
	parsed := parseOpsErrorResponse(state.buf.Bytes())
	require.True(t, parsed.StreamFailure)
	require.Equal(t, "service_unavailable_error", parsed.ErrorType)
}

func TestSanitizeOpsSSEDataForPersistence_RedactsJSONFields(t *testing.T) {
	body := []byte("event: error\ndata: {\"type\":\"error\",\"authorization\":\"Bearer secret\",\ndata: \"nested\":{\"api_key\":\"sk-secret\"}}\n\n")
	sanitized := sanitizeOpsSSEDataForPersistence(body)
	require.NotContains(t, sanitized, "Bearer secret")
	require.NotContains(t, sanitized, "sk-secret")
	require.Contains(t, sanitized, `"authorization":"[REDACTED]"`)
	require.Contains(t, sanitized, `"api_key":"[REDACTED]"`)
}

func TestSanitizeOpsSSEDataForPersistence_DropsTruncatedJSONFragment(t *testing.T) {
	body := []byte("event: error\ndata: {\"type\":\"error\",\"authorization\":\"Bearer leaked")
	sanitized := sanitizeOpsSSEDataForPersistence(body)
	require.NotContains(t, sanitized, "Bearer leaked")
	require.Contains(t, sanitized, `data: {"payload_truncated":true}`)
}

func BenchmarkOpsCaptureWriterSuccessfulSSEFrames(b *testing.B) {
	frame := []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		state.captureResponseChunk(frame, http.StatusOK)
	}
	if state.buf.Len() != 0 {
		b.Fatal("successful frames must not be captured")
	}
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
