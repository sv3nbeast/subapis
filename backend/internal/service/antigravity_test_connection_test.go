//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type accountTestRepoStub struct {
	AccountRepository
	account *Account
}

func (r *accountTestRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account != nil && r.account.ID == id {
		return r.account, nil
	}
	return nil, errors.New("account not found")
}

func (r *accountTestRepoStub) Update(_ context.Context, account *Account) error {
	if r.account != nil && account != nil && r.account.ID == account.ID {
		r.account.Credentials = account.Credentials
	}
	return nil
}

func TestAntigravityGatewayService_TestConnection_BypassesModelRateLimitPrecheck(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	resetAt := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	account := &Account{
		ID:          101,
		Name:        "ag-upstream",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeUpstream,
		Schedulable: true,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "token",
		},
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limit_reset_at": resetAt,
				},
			},
		},
	}

	upstream := &recordingOKUpstream{}
	svc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  upstream,
	}

	result, err := svc.TestConnection(context.Background(), account, "claude-sonnet-4-5")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, upstream.calls, "测试连接应绕过 model cooldown 预检查并实际发起探测")
}

type recordingBodyUpstream struct {
	calls              int
	body               []byte
	url                string
	userAgent          string
	singleAccountRetry bool
	authorization      string
}

func (r *recordingBodyUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	r.calls++
	if req.URL != nil {
		r.url = req.URL.String()
	}
	r.userAgent = req.Header.Get("User-Agent")
	r.singleAccountRetry = isSingleAccountRetry(req.Context())
	r.authorization = req.Header.Get("Authorization")
	if req.Body != nil {
		r.body, _ = io.ReadAll(req.Body)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("ok")),
	}, nil
}

func (r *recordingBodyUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return r.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestAntigravityGatewayService_TestConnection_OAuthBootstrapsMissingProjectID(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	bootstrap := &antigravityBootstrapClientStub{
		loadResp: &antigravity.LoadCodeAssistResponse{
			CloudAICompanionProject: "project-from-bootstrap",
			CurrentTier:             &antigravity.TierInfo{ID: "free-tier"},
		},
		modelsResp: &antigravity.FetchAvailableModelsResponse{
			Models: map[string]antigravity.ModelInfo{
				"claude-sonnet-4-6": {},
			},
		},
		userInfoResp: &antigravity.FetchUserInfoResponse{RegionCode: "US"},
	}
	account := &Account{
		ID:          505,
		Name:        "ag-oauth-missing-project",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Schedulable: true,
		Status:      StatusError,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
		},
	}

	upstream := &recordingBodyUpstream{}
	repo := &accountTestRepoStub{account: account}
	svc := &AntigravityGatewayService{
		accountRepo:          repo,
		bootstrapProbeCache:  newAntigravityBootstrapCache(),
		tokenProvider:        &AntigravityTokenProvider{},
		httpUpstream:         upstream,
		newAntigravityClient: func(proxyURL string) (antigravityBootstrapClient, error) { return bootstrap, nil },
	}

	result, err := svc.TestConnection(context.Background(), account, "claude-sonnet-4-6")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "project-from-bootstrap", account.GetCredential("project_id"))
	require.Equal(t, 1, bootstrap.loadCalls)
	require.Equal(t, 1, upstream.calls)
	require.True(t, upstream.singleAccountRetry, "测试连接应按指定账号单账号探测，不能进入多账号切换语义")

	var sent map[string]any
	require.NoError(t, json.Unmarshal(upstream.body, &sent))
	require.Equal(t, "project-from-bootstrap", sent["project"])
}

type antigravityForceRefreshExecutorStub struct {
	token        string
	refreshCalls int
}

func (e *antigravityForceRefreshExecutorStub) CacheKey(account *Account) string {
	return AntigravityTokenCacheKey(account)
}

func (e *antigravityForceRefreshExecutorStub) CanRefresh(account *Account) bool {
	return account != nil && account.Platform == PlatformAntigravity && account.Type == AccountTypeOAuth
}

func (e *antigravityForceRefreshExecutorStub) NeedsRefresh(_ *Account, _ time.Duration) bool {
	return false
}

func (e *antigravityForceRefreshExecutorStub) Refresh(_ context.Context, account *Account) (map[string]any, error) {
	e.refreshCalls++
	creds := MergeCredentials(account.Credentials, map[string]any{
		"access_token": e.token,
		"expires_at":   time.Now().Add(time.Hour).Unix(),
	})
	return creds, nil
}

func TestAntigravityGatewayService_TestConnection_ForceRefreshesValidationErrorToken(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	account := &Account{
		ID:           607,
		Name:         "ag-validation-refresh",
		Platform:     PlatformAntigravity,
		Type:         AccountTypeOAuth,
		Status:       StatusError,
		ErrorMessage: "Validation required (403): Verify your account to continue.",
		Schedulable:  true,
		Concurrency:  1,
		Credentials: map[string]any{
			"access_token":  "old-token",
			"refresh_token": "refresh-token",
			"project_id":    "project-from-creds",
		},
	}
	repo := &accountTestRepoStub{account: account}
	executor := &antigravityForceRefreshExecutorStub{token: "new-token"}
	upstream := &recordingBodyUpstream{}
	svc := &AntigravityGatewayService{
		accountRepo: repo,
		tokenProvider: &AntigravityTokenProvider{
			accountRepo: repo,
			refreshAPI:  NewOAuthRefreshAPI(repo, nil),
			executor:    executor,
		},
		httpUpstream: upstream,
	}

	result, err := svc.TestConnection(context.Background(), account, "claude-sonnet-4-6")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, executor.refreshCalls)
	require.Equal(t, "new-token", account.GetCredential("access_token"))
	require.Equal(t, "Bearer new-token", upstream.authorization)
}

type validationThenLegacyOKUpstream struct {
	calls      int
	bodies     [][]byte
	urls       []string
	userAgents []string
}

func (u *validationThenLegacyOKUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.calls++
	if req.URL != nil {
		u.urls = append(u.urls, req.URL.String())
	}
	u.userAgents = append(u.userAgents, req.Header.Get("User-Agent"))
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		u.bodies = append(u.bodies, body)
	}
	if u.calls == 1 {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":403,"message":"Verify your account to continue.","status":"PERMISSION_DENIED","details":[{"reason":"VALIDATION_REQUIRED","metadata":{"validation_url":"https://accounts.google.com/signin/continue"}}]}}`)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"legacy ok"}]}}]}}` + "\n\n")),
	}, nil
}

func (u *validationThenLegacyOKUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestAntigravityGatewayService_TestConnection_LegacyWakeupFallbackOnValidationRequired(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	account := &Account{
		ID:          608,
		Name:        "ag-validation-legacy",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusError,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "project-from-creds",
		},
	}
	upstream := &validationThenLegacyOKUpstream{}
	svc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  upstream,
	}

	result, err := svc.TestConnection(context.Background(), account, "claude-sonnet-4-6")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "legacy ok", result.Text)
	require.Equal(t, "claude-sonnet-4-6", result.MappedModel)
	require.Equal(t, 2, upstream.calls)
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, upstream.urls[1], "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse")
	require.Equal(t, "antigravity", upstream.userAgents[1])

	var legacy map[string]any
	require.NoError(t, json.Unmarshal(upstream.bodies[1], &legacy))
	require.Equal(t, "project-from-creds", legacy["project"])
	require.Equal(t, "claude-sonnet-4-6", legacy["model"])
	require.Equal(t, "antigravity", legacy["userAgent"])
	require.Equal(t, "agent", legacy["requestType"])
	require.Contains(t, legacy["requestId"], "req_")
	request := legacy["request"].(map[string]any)
	require.Contains(t, request["session_id"], "sess_")
	require.Equal(t, float64(0), request["generationConfig"].(map[string]any)["temperature"])
	systemText := request["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
	require.Contains(t, systemText, "Absolute paths only")
}

func TestAntigravityGatewayService_GetAvailableModelsForAccount_UsesLiveModelsAndPriority(t *testing.T) {
	bootstrap := &antigravityBootstrapClientStub{
		modelsResp: &antigravity.FetchAvailableModelsResponse{
			Models: map[string]antigravity.ModelInfo{
				"gemini-3.1-pro-high": {
					DisplayName: "Gemini 3.1 Pro High",
				},
				"claude-opus-4-6-thinking": {
					DisplayName: "Claude Opus 4.6 Thinking",
				},
				"claude-sonnet-4-6": {
					DisplayName: "Claude Sonnet 4.6",
				},
			},
		},
	}
	account := &Account{
		ID:          606,
		Name:        "ag-live-models",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusError,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "existing-project",
		},
	}
	svc := &AntigravityGatewayService{
		bootstrapProbeCache: newAntigravityBootstrapCache(),
		tokenProvider:       &AntigravityTokenProvider{},
		newAntigravityClient: func(proxyURL string) (antigravityBootstrapClient, error) {
			return bootstrap, nil
		},
	}

	models, err := svc.GetAvailableModelsForAccount(context.Background(), account)
	require.NoError(t, err)
	require.Len(t, models, 3)
	require.Equal(t, "claude-sonnet-4-6", models[0].ID)
	require.Equal(t, "claude-opus-4-6-thinking", models[1].ID)
	require.Equal(t, "gemini-3.1-pro-high", models[2].ID)
	require.Equal(t, 1, bootstrap.modelsCalls)
	require.Equal(t, 0, bootstrap.loadCalls, "已有 project_id 时模型列表不应强制同步 loadCodeAssist")
}

func TestAccountTestService_RunTestBackground_AntigravityDefaultModelPrefersCurrentSonnet(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	account := &Account{
		ID:          707,
		Name:        "ag-default-model",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeUpstream,
		Schedulable: true,
		Status:      StatusError,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "token",
		},
	}
	upstream := &recordingBodyUpstream{}
	agSvc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  upstream,
	}
	testSvc := &AccountTestService{
		accountRepo:               &accountTestRepoStub{account: account},
		antigravityGatewayService: agSvc,
	}

	result, err := testSvc.RunTestBackground(context.Background(), account.ID, "")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)

	var sent map[string]any
	require.NoError(t, json.Unmarshal(upstream.body, &sent))
	require.Equal(t, "claude-sonnet-4-6", sent["model"])
}

func TestAntigravityGatewayService_TestConnection_BypassesModelCapacityCooldownPrecheck(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	accountModelCapacityCooldownMu.Lock()
	accountModelCapacityCooldownUntil = make(map[accountModelCapacityCooldownKey]time.Time)
	accountModelCapacityCooldownUntil[modelCapacityCooldownMapKey(102, "claude-sonnet-4-5")] = time.Now().Add(5 * time.Minute)
	accountModelCapacityCooldownMu.Unlock()
	t.Cleanup(func() {
		accountModelCapacityCooldownMu.Lock()
		accountModelCapacityCooldownUntil = make(map[accountModelCapacityCooldownKey]time.Time)
		accountModelCapacityCooldownMu.Unlock()
	})

	antigravity.BaseURLs = []string{"https://ag-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	account := &Account{
		ID:          102,
		Name:        "ag-capacity-cooldown",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeUpstream,
		Schedulable: true,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "token",
		},
	}

	upstream := &recordingOKUpstream{}
	svc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  upstream,
	}

	result, err := svc.TestConnection(context.Background(), account, "claude-sonnet-4-5")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, upstream.calls, "测试连接应绕过账号级短容量冷却预检查并实际发起探测")
}

func TestAccountTestService_RunTestBackground_AntigravityNotFilteredByModelRateLimit(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	resetAt := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	account := &Account{
		ID:          202,
		Name:        "ag-scheduled",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeUpstream,
		Schedulable: true,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "token",
		},
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limit_reset_at": resetAt,
				},
			},
		},
	}

	upstream := &recordingOKUpstream{}
	agSvc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  upstream,
	}
	testSvc := &AccountTestService{
		accountRepo:               &accountTestRepoStub{account: account},
		antigravityGatewayService: agSvc,
	}

	result, err := testSvc.RunTestBackground(context.Background(), account.ID, "claude-sonnet-4-5")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "success", result.Status)
	require.Equal(t, 1, upstream.calls, "计划测试走同一条账号测试链路，不应被 model cooldown 预检查挡住")
}

func TestAccountTestService_RunTestBackground_AntigravityNotFilteredByTempUnsched(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	until := time.Now().Add(30 * time.Minute)
	account := &Account{
		ID:                      303,
		Name:                    "ag-temp-unsched",
		Platform:                PlatformAntigravity,
		Type:                    AccountTypeUpstream,
		Schedulable:             true,
		Status:                  StatusActive,
		Concurrency:             1,
		TempUnschedulableUntil:  &until,
		TempUnschedulableReason: `{"status_code":429}`,
		Credentials: map[string]any{
			"api_key": "token",
		},
	}

	upstream := &recordingOKUpstream{}
	agSvc := &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  upstream,
	}
	testSvc := &AccountTestService{
		accountRepo:               &accountTestRepoStub{account: account},
		antigravityGatewayService: agSvc,
	}

	result, err := testSvc.RunTestBackground(context.Background(), account.ID, "claude-sonnet-4-5")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "success", result.Status)
	require.Equal(t, 1, upstream.calls, "计划测试不应被 temp_unsched 状态本身挡住")
}

type quotaExhaustedUpstream struct{}

func (q *quotaExhaustedUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"check quota"}}`)),
	}, nil
}

func (q *quotaExhaustedUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return q.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestAntigravityGatewayService_TestConnection_QuotaExhaustedReturnsAccurateMessage(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "")

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	}()

	antigravity.BaseURLs = []string{"https://ag-test.example"}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	account := &Account{
		ID:          404,
		Name:        "ag-quota-exhausted",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeUpstream,
		Schedulable: true,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "token",
		},
	}

	repo := &stubAntigravityAccountRepo{}
	rateLimitSvc := NewRateLimitService(repo, nil, nil, nil, nil)
	svc := &AntigravityGatewayService{
		accountRepo:      repo,
		rateLimitService: rateLimitSvc,
		tokenProvider:    &AntigravityTokenProvider{},
		httpUpstream:     &quotaExhaustedUpstream{},
		settingService: &SettingService{cfg: &config.Config{Gateway: config.GatewayConfig{
			AntigravityQuotaExhaustedTempUnschedMinutes: 60,
		}}},
	}

	_, err := svc.TestConnection(context.Background(), account, "claude-opus-4-6-thinking")
	require.Error(t, err)
	require.Contains(t, err.Error(), "本次测试已请求上游")
	require.Contains(t, err.Error(), "429 配额耗尽响应")
	require.Contains(t, err.Error(), "已按模型隔离至")
	require.NotContains(t, err.Error(), "当前限流中")
}
