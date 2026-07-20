package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelCapabilityRateLimitCall struct {
	accountID int64
	model     string
	resetAt   time.Time
	reason    string
}

type modelCapabilityAccountRepoStub struct {
	AccountRepository
	modelRateLimitCalls []modelCapabilityRateLimitCall
}

func (r *modelCapabilityAccountRepoStub) SetModelRateLimit(_ context.Context, accountID int64, model string, resetAt time.Time, reason ...string) error {
	call := modelCapabilityRateLimitCall{
		accountID: accountID,
		model:     model,
		resetAt:   resetAt,
	}
	if len(reason) > 0 {
		call.reason = reason[0]
	}
	r.modelRateLimitCalls = append(r.modelRateLimitCalls, call)
	return nil
}

func TestIsUpstreamAccountModelUnsupportedError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{
			name:       "codex chatgpt account model capability",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account.","type":"invalid_request_error"}}`,
			want:       true,
		},
		{
			name:       "api key group model capability",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"Requested model is not supported by this API key/group","type":"invalid_request_error"}}`,
			want:       true,
		},
		{
			name:       "ordinary model validation remains client error",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"model must be provided","type":"invalid_request_error"}}`,
			want:       false,
		},
		{
			name:       "same text with non 400 status is not capability signal",
			statusCode: http.StatusNotFound,
			body:       `{"error":{"message":"Requested model is not supported by this API key/group"}}`,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUpstreamAccountModelUnsupportedError(tt.statusCode, []byte(tt.body)))
		})
	}
}

func TestRateLimitService_AccountModelUnsupportedUsesModelCooldownDespitePoolAndCustom400Policy(t *testing.T) {
	repo := &modelCapabilityAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := &Account{
		ID:          81,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"api_key":                    "test-key",
			"pool_mode":                  true,
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusTooManyRequests)},
		},
	}
	body := []byte(`{"error":{"message":"Requested model is not supported by this API key/group","type":"invalid_request_error"}}`)

	handled := svc.HandleUpstreamError(context.Background(), account, http.StatusBadRequest, http.Header{}, body, "grok-4")

	require.True(t, handled)
	require.Len(t, repo.modelRateLimitCalls, 1)
	call := repo.modelRateLimitCalls[0]
	require.Equal(t, account.ID, call.accountID)
	require.Equal(t, "grok-4", call.model)
	require.Equal(t, upstreamAccountModelUnsupportedReason, call.reason)
	require.WithinDuration(t, time.Now().Add(upstreamModelNotFoundCooldown), call.resetAt, 5*time.Second)
}

func TestOpenAIGatewayService_ForwardAccountModelUnsupportedReturnsFailoverBeforeResponseWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"instructions":"test","input":"hello"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account.","type":"invalid_request_error"}}`)),
	}}
	repo := &modelCapabilityAccountRepoStub{}
	svc := &OpenAIGatewayService{
		cfg:              &config.Config{},
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := &Account{
		ID:          82,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "test-token",
			"chatgpt_account_id": "test-account",
		},
	}

	result, err := svc.Forward(context.Background(), c, account, body)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
	require.False(t, c.Writer.Written())
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.6-sol", repo.modelRateLimitCalls[0].model)
	require.True(t, svc.isOpenAIOAuthModelUnsupportedForRequest(account, "gpt-5.6-sol", false))
}

func TestOpenAIOAuthModelUnsupportedCacheUsesPassthroughModelKey(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{
		ID:       8201,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "test-token",
			"chatgpt_account_id": "test-account",
			"model_mapping": map[string]any{
				"client-model": "gpt-5.6-sol",
			},
		},
		Extra: map[string]any{
			"openai_passthrough": true,
		},
	}
	body := []byte(`{"error":{"message":"The 'client-model' model is not supported when using Codex with a ChatGPT account.","type":"invalid_request_error"}}`)

	_ = svc.newOpenAIAccountModelUnsupportedFailover(
		context.Background(), nil, account, http.Header{}, body, "client-model",
	)

	require.True(t, svc.isOpenAIOAuthModelUnsupportedForRequest(account, "client-model", false))
}

func TestOpenAIOAuthModelUnsupportedCacheUsesNativeWSPassthroughModelKey(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	svc := &OpenAIGatewayService{cfg: cfg}
	account := &Account{
		ID:          8202,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":       "test-token",
			"chatgpt_account_id": "test-account",
			"model_mapping": map[string]any{
				"client-model": "gpt-5.6-sol",
			},
		},
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough,
		},
	}
	body := []byte(`{"error":{"message":"The 'client-model' model is not supported when using Codex with a ChatGPT account.","type":"invalid_request_error"}}`)

	_ = svc.newOpenAIAccountModelUnsupportedFailover(
		context.Background(), nil, account, http.Header{}, body, "client-model",
	)

	require.False(t, svc.isOpenAIOAuthModelUnsupportedForRequest(account, "client-model", false))
	require.True(t, svc.isOpenAIOAuthModelUnsupportedForSchedule(account, OpenAIAccountScheduleRequest{
		RequestedModel:    "client-model",
		RequiredTransport: OpenAIUpstreamTransportResponsesWebsocketV2Ingress,
	}))
}

func TestOpenAIGatewayService_OpenAIPassthroughAccountModelUnsupportedReturnsFailoverBeforeResponseWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"instructions":"test","input":"hello"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Requested model is not supported by this API key/group","type":"invalid_request_error"}}`)),
	}}
	repo := &modelCapabilityAccountRepoStub{}
	svc := &OpenAIGatewayService{
		cfg:              &config.Config{},
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := &Account{
		ID:          83,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "test-key",
			"model_mapping": map[string]any{
				"gpt-5.6-sol": "gpt-5.6-sol",
			},
		},
		Extra: map[string]any{
			"openai_passthrough": true,
		},
	}

	result, err := svc.Forward(context.Background(), c, account, body)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
	require.False(t, c.Writer.Written())
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.6-sol", repo.modelRateLimitCalls[0].model)
}

func TestOpenAIGatewayService_ForwardOrdinaryBadRequestDoesNotFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"instructions":"test","input":"hello"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"model must be provided","type":"invalid_request_error"}}`)),
	}}
	repo := &modelCapabilityAccountRepoStub{}
	svc := &OpenAIGatewayService{
		cfg:              &config.Config{},
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := &Account{
		ID:          84,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "test-token",
			"chatgpt_account_id": "test-account",
		},
	}

	result, err := svc.Forward(context.Background(), c, account, body)

	var failoverErr *UpstreamFailoverError
	require.Error(t, err)
	require.False(t, errors.As(err, &failoverErr))
	require.Nil(t, result)
	require.True(t, c.Writer.Written())
	require.Empty(t, repo.modelRateLimitCalls)
}

func TestOpenAIGatewayService_ChatGPTModelCapabilityFailureSkipsOnlyAffectedOAuthCandidate(t *testing.T) {
	groupID := int64(85)
	oauthAccount := Account{
		ID:          85,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    0,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-account",
			"model_mapping": map[string]any{
				"gpt-5.6-sol": "gpt-5.6-sol",
			},
		},
	}
	secondOAuthAccount := oauthAccount
	secondOAuthAccount.ID++
	secondOAuthAccount.Priority++
	secondOAuthAccount.Credentials = map[string]any{
		"access_token":       "second-oauth-token",
		"chatgpt_account_id": "second-chatgpt-account",
		"model_mapping": map[string]any{
			"gpt-5.6-sol": "gpt-5.6-sol",
		},
	}
	accounts := []Account{oauthAccount, secondOAuthAccount}
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}
	svc := &OpenAIGatewayService{
		accountRepo: repo,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAIKiroBridgeEnabled: true,
			OpenAIWS: config.GatewayOpenAIWSConfig{
				LBTopK:                1,
				SchedulerScoreWeights: config.GatewayOpenAIWSSchedulerScoreWeights{Priority: 1},
			},
		}},
	}
	body := []byte(`{"error":{"message":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account.","type":"invalid_request_error"}}`)

	_ = svc.newOpenAIAccountModelUnsupportedFailover(
		context.Background(), nil, &oauthAccount, http.Header{}, body, "gpt-5.6-sol",
	)
	selected, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, "", "gpt-5.6-sol", nil)

	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, secondOAuthAccount.ID, selected.ID)
	require.True(t, svc.isOpenAIOAuthModelUnsupportedForRequest(&oauthAccount, "gpt-5.6-sol", false))
	require.False(t, svc.isOpenAIOAuthModelUnsupportedForRequest(&secondOAuthAccount, "gpt-5.6-sol", false))
	require.False(t, svc.isOpenAICompatibleAccountEligibleForSchedule(context.Background(), &oauthAccount, OpenAIAccountScheduleRequest{
		RequestedModel: "gpt-5.6-sol",
		Platform:       PlatformOpenAI,
	}))
	require.True(t, svc.isOpenAICompatibleAccountEligibleForSchedule(context.Background(), &secondOAuthAccount, OpenAIAccountScheduleRequest{
		RequestedModel: "gpt-5.6-sol",
		Platform:       PlatformOpenAI,
	}))

	bridgeSelection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "", "gpt-5.6-sol", "gpt-5.6-sol", nil,
	)
	require.NoError(t, err)
	require.NotNil(t, bridgeSelection)
	require.NotNil(t, bridgeSelection.Account)
	require.Equal(t, secondOAuthAccount.ID, bridgeSelection.Account.ID)
	if bridgeSelection.ReleaseFunc != nil {
		bridgeSelection.ReleaseFunc()
	}

	oauthOnlySvc := &OpenAIGatewayService{
		accountRepo: openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts[:1]}},
		cfg:         &config.Config{},
	}
	_ = oauthOnlySvc.newOpenAIAccountModelUnsupportedFailover(
		context.Background(), nil, &oauthAccount, http.Header{}, body, "gpt-5.6-sol",
	)
	_, err = oauthOnlySvc.SelectAccountForModelWithExclusions(context.Background(), nil, "", "gpt-5.6-sol", nil)
	require.EqualError(t, err, "no available OpenAI accounts supporting model: gpt-5.6-sol")
}

func TestOpenAIOAuthModelUnsupportedCacheBoundsAndExpires(t *testing.T) {
	var cache openAIOAuthModelUnsupportedCache
	for i := 0; i < maxOpenAIOAuthModelUnsupportedEntries+1; i++ {
		cache.Mark(fmt.Sprintf("model-%d", i), time.Now().Add(time.Hour))
	}

	cache.mu.Lock()
	count := len(cache.untilByAccountModel)
	cache.untilByAccountModel["expired"] = time.Now().Add(-time.Minute)
	cache.mu.Unlock()

	require.Equal(t, maxOpenAIOAuthModelUnsupportedEntries, count)
	require.False(t, cache.IsActive("expired"))

	cache.mu.Lock()
	_, expiredStillPresent := cache.untilByAccountModel["expired"]
	cache.mu.Unlock()
	require.False(t, expiredStillPresent)
}

func TestOpenAIOAuthModelUnsupportedCacheConcurrentAccess(t *testing.T) {
	var cache openAIOAuthModelUnsupportedCache
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 64; i++ {
				model := fmt.Sprintf("model-%d", (worker+i)%192)
				cache.Mark(model, time.Now().Add(time.Minute))
				_ = cache.IsActive(model)
			}
		}()
	}
	wg.Wait()

	cache.mu.Lock()
	count := len(cache.untilByAccountModel)
	cache.mu.Unlock()
	require.LessOrEqual(t, count, maxOpenAIOAuthModelUnsupportedEntries)
}
