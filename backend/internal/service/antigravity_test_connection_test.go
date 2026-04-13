//go:build unit

package service

import (
	"context"
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
