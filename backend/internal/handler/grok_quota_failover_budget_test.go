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
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type grokQuotaFailoverAccountRepo struct {
	service.AccountRepository
	mu       sync.Mutex
	accounts []service.Account
}

func (r *grokQuotaFailoverAccountRepo) accountsForPlatform(platform string) []service.Account {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform && account.IsSchedulable() {
			out = append(out, account)
		}
	}
	return out
}

func (r *grokQuotaFailoverAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r *grokQuotaFailoverAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r *grokQuotaFailoverAccountRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r *grokQuotaFailoverAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, account := range r.accounts {
		if account.ID == id {
			copy := account
			return &copy, nil
		}
	}
	return nil, nil
}

func (r *grokQuotaFailoverAccountRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			reset := resetAt
			r.accounts[i].RateLimitResetAt = &reset
			break
		}
	}
	return nil
}

type grokQuotaFailoverHTTPUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

func (u *grokQuotaFailoverHTTPUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	if accountID != 3 {
		return &http.Response{
			StatusCode: http.StatusPaymentRequired,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(bytes.NewBufferString(
				`{"code":"personal-team-blocked:spending-limit","error":"You have run out of credits"}`,
			)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req_grok_healthy"}},
		Body: io.NopCloser(bytes.NewBufferString(
			`{"id":"chatcmpl_grok_healthy","object":"chat.completion","model":"grok-4.5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		)),
	}, nil
}

func (u *grokQuotaFailoverHTTPUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func TestGrokQuotaFailover_ReachesThirdHealthyAccountPastGenericSwitchLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7310)
	accounts := []service.Account{
		{ID: 1, Name: "grok-exhausted-1", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, Credentials: map[string]any{"api_key": "key-1", "base_url": "https://api.x.ai/v1"}},
		{ID: 2, Name: "grok-exhausted-2", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, Credentials: map[string]any{"api_key": "key-2", "base_url": "https://api.x.ai/v1"}},
		{ID: 3, Name: "grok-healthy", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2, Credentials: map[string]any{"api_key": "key-3", "base_url": "https://api.x.ai/v1"}},
	}
	repo := &grokQuotaFailoverAccountRepo{accounts: accounts}
	upstream := &grokQuotaFailoverHTTPUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.MaxAccountSwitches = 1
	gatewayService := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
		upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	h := NewOpenAIGatewayHandler(
		gatewayService,
		nil,
		service.NewConcurrencyService(nil),
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)
	selection, _, selectionErr := gatewayService.SelectAccountWithSchedulerForCapability(
		context.Background(), &groupID, "", "", "grok-4.5", nil,
		service.OpenAIUpstreamTransportAny, service.OpenAIEndpointCapabilityChatCompletions,
		false, false, service.PlatformGrok,
	)
	require.NoError(t, selectionErr)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(1), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	body := []byte(`{"model":"grok-4.5","messages":[{"role":"user","content":"ping"}],"stream":false}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      91,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                   groupID,
			Platform:             service.PlatformGrok,
			GrokChatUpstreamMode: service.GrokChatUpstreamModeRaw,
			Status:               service.StatusActive,
		},
		User: &service.User{ID: 92, Status: service.StatusActive},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 92, Concurrency: 0})

	h.ChatCompletions(c)

	require.Equal(t, []int64{1, 2, 3}, upstream.calls(), "status=%d body=%s", rec.Code, rec.Body.String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "chatcmpl_grok_healthy", gjson.GetBytes(rec.Body.Bytes(), "id").String())
}
