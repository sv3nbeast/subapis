package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type antigravity429NoCooldownRepo struct {
	AccountRepository
	tempUnschedCalls int
	modelLimitCalls  int
	accountRateCalls int
}

func (r *antigravity429NoCooldownRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempUnschedCalls++
	return nil
}

func (r *antigravity429NoCooldownRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	r.modelLimitCalls++
	return nil
}

func (r *antigravity429NoCooldownRepo) SetRateLimited(context.Context, int64, time.Time) error {
	r.accountRateCalls++
	return nil
}

type antigravity429FixedUpstream struct {
	status int
	body   string
	calls  int
	after  func()
}

type antigravity429SequenceUpstream struct {
	responses []*http.Response
	calls     int
}

func (u *antigravity429SequenceUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	if u.calls >= len(u.responses) {
		return nil, errors.New("unexpected upstream call")
	}
	resp := u.responses[u.calls]
	u.calls++
	return resp, nil
}

func (u *antigravity429SequenceUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func (u *antigravity429FixedUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	if u.after != nil {
		u.after()
	}
	return &http.Response{
		StatusCode: u.status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

func (u *antigravity429FixedUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func newAntigravity429NoCooldownAccount() *Account {
	return &Account{
		ID:          42901,
		Name:        "antigravity-429-no-cooldown",
		Type:        AccountTypeOAuth,
		Platform:    PlatformAntigravity,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
	}
}

func newAntigravity429NoCooldownParams(account *Account, upstream HTTPUpstream, repo AccountRepository) antigravityRetryLoopParams {
	return antigravityRetryLoopParams{
		ctx:            withAntigravityQuota429RetryDelay(context.Background(), 0),
		prefix:         "[antigravity-429-no-cooldown-test]",
		account:        account,
		accessToken:    "token",
		action:         "generateContent",
		body:           []byte(`{"input":"test"}`),
		httpUpstream:   upstream,
		accountRepo:    repo,
		requestedModel: "claude-sonnet-4-5",
		handleError: func(context.Context, string, *Account, int, http.Header, []byte, string, int64, string, bool) *handleModelRateLimitResult {
			return nil
		},
	}
}

func TestAntigravity429QuotaExhaustedRetriesSameAccountThenFailsOverWithoutCooldown(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "https://ag-429-no-cooldown.test")
	repo := &antigravity429NoCooldownRepo{}
	upstream := &antigravity429FixedUpstream{
		status: http.StatusTooManyRequests,
		body:   `{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`,
	}
	account := newAntigravity429NoCooldownAccount()
	cache := &stubSmartRetryCache{}
	svc := &AntigravityGatewayService{accountRepo: repo, cache: cache}
	params := newAntigravity429NoCooldownParams(account, upstream, repo)
	params.isStickySession = true
	params.groupID = 36
	params.sessionHash = "quota-sticky-session"

	result, err := svc.antigravityRetryLoop(params)

	require.Nil(t, result)
	var switchErr *AntigravityAccountSwitchError
	require.ErrorAs(t, err, &switchErr)
	require.Equal(t, http.StatusTooManyRequests, switchErr.StatusCode)
	require.Equal(t, account.ID, switchErr.OriginalAccountID)
	require.Equal(t, params.requestedModel, switchErr.RateLimitedModel)
	require.True(t, switchErr.IsStickySession)
	require.True(t, switchErr.Quota429)
	require.Contains(t, string(switchErr.ResponseBody), "check quota")
	require.Nil(t, switchErr.RateLimitResetAt)
	require.Equal(t, antigravityQuota429MaxAttempts, upstream.calls)
	require.Zero(t, repo.tempUnschedCalls)
	require.Zero(t, repo.modelLimitCalls)
	require.Zero(t, repo.accountRateCalls)
	require.Empty(t, cache.deleteCalls, "request-scoped failover must not persistently clear the sticky binding")
	require.Nil(t, account.TempUnschedulableUntil)
	require.Empty(t, account.TempUnschedulableReason)
}

func TestAntigravity429QuotaRetryDelaysAreFiveThenTenSeconds(t *testing.T) {
	require.Equal(t, 5*time.Second, antigravityQuota429RetryDelayForAttempt(context.Background(), 1))
	require.Equal(t, 10*time.Second, antigravityQuota429RetryDelayForAttempt(context.Background(), 2))
	require.Equal(t, time.Duration(0), antigravityQuota429RetryDelayForAttempt(withAntigravityQuota429RetryDelay(context.Background(), 0), 2))
}

func TestAntigravity429QuotaRetryHonorsContextCancellation(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "https://ag-429-no-cooldown.test")
	ctx, cancel := context.WithCancel(context.Background())
	upstream := &antigravity429FixedUpstream{
		status: http.StatusTooManyRequests,
		body:   `{"error":{"message":"check quota"}}`,
		after:  cancel,
	}
	account := newAntigravity429NoCooldownAccount()
	repo := &antigravity429NoCooldownRepo{}
	params := newAntigravity429NoCooldownParams(account, upstream, repo)
	params.ctx = ctx
	svc := &AntigravityGatewayService{accountRepo: repo}

	result, err := svc.antigravityRetryLoop(params)

	require.Nil(t, result)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, upstream.calls)
	require.Zero(t, repo.tempUnschedCalls)
	require.Zero(t, repo.modelLimitCalls)
	require.Zero(t, repo.accountRateCalls)
}

func TestAntigravity429QuotaExhaustedSucceedsOnThirdSameAccountAttempt(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "https://ag-429-no-cooldown.test")
	repo := &antigravity429NoCooldownRepo{}
	upstream := &antigravity429SequenceUpstream{responses: []*http.Response{
		{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"check quota"}}`)),
		},
		{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"check quota"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		},
	}}
	account := newAntigravity429NoCooldownAccount()
	svc := &AntigravityGatewayService{accountRepo: repo}

	result, err := svc.antigravityRetryLoop(newAntigravity429NoCooldownParams(account, upstream, repo))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, result.resp.StatusCode)
	require.Equal(t, antigravityQuota429MaxAttempts, upstream.calls)
	require.Zero(t, repo.tempUnschedCalls)
	require.Zero(t, repo.modelLimitCalls)
	require.Zero(t, repo.accountRateCalls)
}

func TestAntigravity429SharedErrorHandlerNeverPersistsQuotaCooldown(t *testing.T) {
	repo := &antigravity429NoCooldownRepo{}
	account := newAntigravity429NoCooldownAccount()
	body := []byte(`{"error":{"code":429,"message":"check quota","status":"RESOURCE_EXHAUSTED"}}`)
	svc := &AntigravityGatewayService{accountRepo: repo}

	result := svc.handleUpstreamError(
		context.Background(),
		"[antigravity-429-no-cooldown-test]",
		account,
		http.StatusTooManyRequests,
		http.Header{},
		body,
		"claude-sonnet-4-5",
		0,
		"",
		false,
	)

	require.NotNil(t, result)
	require.True(t, result.Handled)
	require.Nil(t, result.SwitchError)
	require.Zero(t, repo.tempUnschedCalls)
	require.Zero(t, repo.modelLimitCalls)
	require.Zero(t, repo.accountRateCalls)
	require.Nil(t, account.TempUnschedulableUntil)
	require.Empty(t, account.TempUnschedulableReason)
}

func TestAntigravity429ConfiguredTempRuleDoesNotOverrideSameAccountRetry(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "https://ag-429-no-cooldown.test")
	repo := &antigravity429NoCooldownRepo{}
	upstream := &antigravity429FixedUpstream{
		status: http.StatusTooManyRequests,
		body:   `{"error":{"message":"check quota"}}`,
	}
	account := newAntigravity429NoCooldownAccount()
	account.Credentials = map[string]any{
		"temp_unschedulable_enabled": true,
		"temp_unschedulable_rules": []any{map[string]any{
			"error_code":       float64(http.StatusTooManyRequests),
			"keywords":         []any{"check quota"},
			"duration_minutes": float64(30),
		}},
	}
	cfg := &config.Config{}
	svc := &AntigravityGatewayService{
		accountRepo:      repo,
		rateLimitService: NewRateLimitService(repo, nil, cfg, nil, nil),
	}

	result, err := svc.antigravityRetryLoop(newAntigravity429NoCooldownParams(account, upstream, repo))

	require.Nil(t, result)
	var switchErr *AntigravityAccountSwitchError
	require.ErrorAs(t, err, &switchErr)
	require.Equal(t, http.StatusTooManyRequests, switchErr.StatusCode)
	require.Equal(t, account.ID, switchErr.OriginalAccountID)
	require.Equal(t, antigravityQuota429MaxAttempts, upstream.calls)
	require.Zero(t, repo.tempUnschedCalls)
	require.Zero(t, repo.modelLimitCalls)
	require.Zero(t, repo.accountRateCalls)
	require.Nil(t, account.TempUnschedulableUntil)
	require.Empty(t, account.TempUnschedulableReason)
}

func TestAntigravityAccountSwitchFailoverErrorPreserves429WithoutCooldown(t *testing.T) {
	body := []byte(`{"error":{"message":"check quota"}}`)
	failoverErr := newAntigravityAccountSwitchFailoverError(&AntigravityAccountSwitchError{
		OriginalAccountID: 42901,
		StatusCode:        http.StatusTooManyRequests,
		RateLimitedModel:  "claude-opus-4-6",
		IsStickySession:   true,
		Quota429:          true,
		ResponseBody:      body,
	})

	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Equal(t, body, failoverErr.ResponseBody)
	require.Equal(t, "claude-opus-4-6", failoverErr.RequestedModel)
	require.True(t, failoverErr.ForceCacheBilling)
	require.True(t, failoverErr.SuppressTempUnschedule)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.AntigravityQuota429)
	require.Equal(t, UpstreamFailureRateLimited, failoverErr.FailureKind)
}

func TestAntigravity429CompatTransportErrorPreservesRequestScopedFailover(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := []byte(`{"error":{"message":"check quota"}}`)
	svc := &AntigravityGatewayService{}

	err := svc.handleAntigravityCompatTransportError(c, &AntigravityAccountSwitchError{
		OriginalAccountID: 42901,
		StatusCode:        http.StatusTooManyRequests,
		RateLimitedModel:  "claude-opus-4-6",
		Quota429:          true,
		ResponseBody:      body,
	})

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Equal(t, body, failoverErr.ResponseBody)
	require.Equal(t, "claude-opus-4-6", failoverErr.RequestedModel)
	require.True(t, failoverErr.AntigravityQuota429)
	require.True(t, failoverErr.SuppressTempUnschedule)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, UpstreamFailureRateLimited, failoverErr.FailureKind)
	require.Equal(t, http.StatusOK, recorder.Code, "transport failover must not write a client response before peer selection")
}

func TestAntigravityCompatTransportErrorKeepsLegacyNonQuotaSwitch(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	svc := &AntigravityGatewayService{}

	err := svc.handleAntigravityCompatTransportError(c, &AntigravityAccountSwitchError{
		OriginalAccountID: 42901,
		StatusCode:        http.StatusTooManyRequests,
		RateLimitedModel:  "claude-opus-4-6",
		ResponseBody:      []byte(`{"error":{"message":"model rate limited"}}`),
	})

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.False(t, failoverErr.AntigravityQuota429)
	require.Empty(t, failoverErr.ResponseBody)
	require.Empty(t, failoverErr.RequestedModel)
	require.False(t, failoverErr.SuppressTempUnschedule)
	require.Equal(t, UpstreamFailureKind(""), failoverErr.FailureKind)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestAntigravity429CompatHTTPErrorReturns429WithoutFailover(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := newAntigravity429NoCooldownAccount()
	svc := &AntigravityGatewayService{accountRepo: &antigravity429NoCooldownRepo{}}
	call := &antigravityCompatUpstreamCall{
		request: antigravityCompatRequest{
			protocol:      antigravityCompatResponses,
			originalModel: "claude-sonnet-4-5",
		},
		prefix: "[antigravity-429-no-cooldown-test]",
	}
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"check quota"}}`)),
	}

	err := svc.handleAntigravityCompatHTTPError(context.Background(), c, account, call, resp)

	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
}

func TestAntigravity429ModelLimitDoesNotSetAccountTempUnschedulable(t *testing.T) {
	repo := &antigravity429NoCooldownRepo{}
	account := newAntigravity429NoCooldownAccount()
	body := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "15s"}
			]
		}
	}`)
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
	params := newAntigravity429NoCooldownParams(account, nil, repo)
	svc := &AntigravityGatewayService{accountRepo: repo}

	result := svc.handleSmartRetry(params, resp, body, "https://ag-429-no-cooldown.test", 0, []string{"https://ag-429-no-cooldown.test"})

	require.NotNil(t, result)
	require.NotNil(t, result.switchError)
	require.Positive(t, repo.modelLimitCalls, "model-scoped cooldown remains available")
	require.Zero(t, repo.tempUnschedCalls)
	require.Zero(t, repo.accountRateCalls)
	require.Nil(t, account.TempUnschedulableUntil)
	require.Empty(t, account.TempUnschedulableReason)
}
