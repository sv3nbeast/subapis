package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

type grokMessagesStreamReadErrorBody struct{}

func (grokMessagesStreamReadErrorBody) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (grokMessagesStreamReadErrorBody) Close() error               { return nil }

type grokMessagesStreamFailoverUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

func (u *grokMessagesStreamFailoverUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()

	if accountID == 1 {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req_grok_truncated"}},
			Body:       grokMessagesStreamReadErrorBody{},
		}, nil
	}
	streamBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_grok_recovered","model":"grok-4.5","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"recovered"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_grok_recovered","object":"response","model":"grok-4.5","status":"completed","output":[{"type":"message","id":"msg_grok_recovered","role":"assistant","status":"completed","content":[{"type":"output_text","text":"recovered"}]}],"usage":{"input_tokens":9,"output_tokens":2,"total_tokens":11}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req_grok_recovered"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}, nil
}

func (u *grokMessagesStreamFailoverUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func TestGrokQuotaFailover_ReachesThirdHealthyAccountPastGenericSwitchLimit(t *testing.T) {
	accounts := []service.Account{
		{ID: 1, Name: "grok-exhausted-1", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, Credentials: map[string]any{"api_key": "key-1", "base_url": "https://api.x.ai/v1"}},
		{ID: 2, Name: "grok-exhausted-2", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, Credentials: map[string]any{"api_key": "key-2", "base_url": "https://api.x.ai/v1"}},
		{ID: 3, Name: "grok-healthy", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2, Credentials: map[string]any{"api_key": "key-3", "base_url": "https://api.x.ai/v1"}},
	}
	initialID, calls, status, responseID := runGrokQuotaFailoverRequest(t, accounts)

	require.Equal(t, int64(1), initialID)
	require.Equal(t, []int64{1, 2, 3}, calls)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "chatcmpl_grok_healthy", responseID)
}

func TestGrokQuotaFailover_PrefersRecentlySuccessfulAccountAfterFirstQuotaFailure(t *testing.T) {
	recent := time.Now().Add(-5 * time.Minute)
	accounts := []service.Account{
		{ID: 1, Name: "grok-exhausted-1", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, Credentials: map[string]any{"api_key": "key-1", "base_url": "https://api.x.ai/v1"}},
		{ID: 2, Name: "grok-unknown", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, Credentials: map[string]any{"api_key": "key-2", "base_url": "https://api.x.ai/v1"}},
		{ID: 3, Name: "grok-recent-healthy", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 20, LastUsedAt: &recent, Credentials: map[string]any{"api_key": "key-3", "base_url": "https://api.x.ai/v1"}},
	}
	initialID, calls, status, responseID := runGrokQuotaFailoverRequest(t, accounts)

	require.Equal(t, int64(1), initialID, "initial scheduling must retain normal priority/LRU behavior")
	require.Equal(t, []int64{1, 3}, calls, "the request should bypass unknown quota candidates after its first confirmed exhaustion")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "chatcmpl_grok_healthy", responseID)
}

func TestGrokMessages_StreamReadErrorBeforeOutputSwitchesAccountWithoutLeakingBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7311)
	accounts := []service.Account{
		{ID: 1, Name: "grok-truncated", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, Credentials: map[string]any{"api_key": "key-1", "base_url": "https://api.x.ai/v1"}},
		{ID: 2, Name: "grok-healthy", Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, Credentials: map[string]any{"api_key": "key-2", "base_url": "https://api.x.ai/v1"}},
	}
	repo := &grokQuotaFailoverAccountRepo{accounts: accounts}
	upstream := &grokMessagesStreamFailoverUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.MaxAccountSwitches = 2
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

	body := []byte(`{"model":"grok-4.5","max_tokens":32,"messages":[{"role":"user","content":"ping"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      93,
		GroupID: &groupID,
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformGrok,
			Status:   service.StatusActive,
		},
		User: &service.User{ID: 94, Status: service.StatusActive},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 94, Concurrency: 0})

	h.Messages(c)

	require.Equal(t, []int64{1, 2}, upstream.calls(), "status=%d body=%s", rec.Code, rec.Body.String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "req_grok_recovered", rec.Header().Get("X-Request-Id"))
	require.Contains(t, rec.Body.String(), "recovered")
	require.Contains(t, rec.Body.String(), "event: message_stop")
	require.NotContains(t, rec.Body.String(), "req_grok_truncated")
	require.NotContains(t, rec.Body.String(), "unexpected EOF")
}

func runGrokQuotaFailoverRequest(t *testing.T, accounts []service.Account) (int64, []int64, int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	groupID := int64(7310)
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
	initialID := selection.Account.ID
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

	return initialID, upstream.calls(), rec.Code, gjson.GetBytes(rec.Body.Bytes(), "id").String()
}
