package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiroCountTokensAccountRepo struct {
	AccountRepository
	account                *Account
	updateCredentialsCalls int
	setTempCalls           int
	setErrorCalls          int
	lastCredentials        map[string]any
}

func (r *kiroCountTokensAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	if r.account == nil {
		return nil, errors.New("account not found")
	}
	return r.account, nil
}

func (r *kiroCountTokensAccountRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.updateCredentialsCalls++
	r.lastCredentials = cloneCredentials(credentials)
	r.account.Credentials = cloneCredentials(credentials)
	return nil
}

func (r *kiroCountTokensAccountRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrorCalls++
	return nil
}

func (r *kiroCountTokensAccountRepo) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.setTempCalls++
	return nil
}

type kiroCountTokensCacheStub struct {
	deletedKeys []string
}

func (s *kiroCountTokensCacheStub) GetAccessToken(context.Context, string) (string, error) {
	return "", nil
}

func (s *kiroCountTokensCacheStub) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

func (s *kiroCountTokensCacheStub) DeleteAccessToken(_ context.Context, cacheKey string) error {
	s.deletedKeys = append(s.deletedKeys, cacheKey)
	return nil
}

func (s *kiroCountTokensCacheStub) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func (s *kiroCountTokensCacheStub) ReleaseRefreshLock(context.Context, string) error {
	return nil
}

type kiroCountTokensRefresherStub struct {
	tokenInfo *KiroTokenInfo
	err       error
}

func (s *kiroCountTokensRefresherStub) RefreshAccountToken(context.Context, *Account) (*KiroTokenInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.tokenInfo, nil
}

func (s *kiroCountTokensRefresherStub) BuildAccountCredentials(tokenInfo *KiroTokenInfo) map[string]any {
	if tokenInfo == nil {
		return nil
	}
	return map[string]any{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   tokenInfo.ExpiresAt,
	}
}

func TestGatewayService_CountTokensKiroOAuth401RefreshesWithoutTempUnschedule(t *testing.T) {
	account := &Account{
		ID:       1459,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":   "expired-token",
			"refresh_token":  "refresh-token",
			"client_id_hash": "client-hash",
		},
	}
	repo := &kiroCountTokensAccountRepo{
		account: account,
	}
	cache := &kiroCountTokensCacheStub{}
	rateLimitSvc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	rateLimitSvc.SetTokenCacheInvalidator(NewCompositeTokenCacheInvalidator(cache))
	provider := NewKiroTokenProvider(repo, nil, nil)
	provider.kiroOAuthService = &kiroCountTokensRefresherStub{tokenInfo: &KiroTokenInfo{
		AccessToken: "fresh-token",
		ExpiresAt:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}}
	svc := &GatewayService{
		rateLimitService:  rateLimitSvc,
		kiroTokenProvider: provider,
	}

	svc.handleCountTokensUpstreamError(
		context.Background(),
		account,
		http.StatusUnauthorized,
		http.Header{},
		[]byte(`{"type":"error","error":{"type":"authentication_error","message":"Invalid bearer token"}}`),
	)

	require.Equal(t, 0, repo.setTempCalls, "Kiro count_tokens 401 must not remove the account from the main request scheduler")
	require.Equal(t, 1, repo.updateCredentialsCalls, "Kiro count_tokens 401 should still force-refresh the token")
	require.Equal(t, "fresh-token", repo.lastCredentials["access_token"])
	require.Equal(t, []string{"kiro:client-hash", "kiro:account:1459"}, cache.deletedKeys)
}

func TestGatewayService_CountTokensKiroOAuth401NonRetryableRefreshCanSetErrorButNotTempUnschedule(t *testing.T) {
	account := &Account{
		ID:       1460,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token":  "refresh-token",
			"client_id_hash": "client-hash-2",
		},
	}
	repo := &kiroCountTokensAccountRepo{
		account: account,
	}
	provider := NewKiroTokenProvider(repo, nil, nil)
	provider.kiroOAuthService = &kiroCountTokensRefresherStub{err: errors.New("invalid_grant: token revoked")}
	svc := &GatewayService{
		rateLimitService:  NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
		kiroTokenProvider: provider,
	}

	svc.handleCountTokensUpstreamError(
		context.Background(),
		account,
		http.StatusUnauthorized,
		http.Header{},
		[]byte(`{"type":"error","error":{"type":"authentication_error","message":"Invalid bearer token"}}`),
	)

	require.Equal(t, 0, repo.setTempCalls)
	require.Equal(t, 1, repo.setErrorCalls, "non-retryable refresh failure should still mark the account error")
}

func TestGatewayService_ForwardCountTokens_KiroReturnsEstimatedTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"text","text":"hello world"}]}]}`)), PlatformAnthropic)
	require.NoError(t, err)

	svc := &GatewayService{}
	account := &Account{
		ID:       1569,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
	}

	err = svc.ForwardCountTokens(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"input_tokens":3}`, rec.Body.String())
}
