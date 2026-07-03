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
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type tokenRefreshKiroProfileRuntimeRepo struct {
	AccountRepository
	accountsByID map[int64]*Account
	updateCalls  int
}

func (r *tokenRefreshKiroProfileRuntimeRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.updateCalls++
	if r.accountsByID == nil {
		r.accountsByID = map[int64]*Account{}
	}
	account := r.accountsByID[id]
	if account == nil {
		account = &Account{ID: id}
		r.accountsByID[id] = account
	}
	account.Credentials = cloneCredentials(credentials)
	return nil
}

type tokenRefreshKiroProfileRuntimeRefresher struct {
	credentials map[string]any
}

func (r *tokenRefreshKiroProfileRuntimeRefresher) CanRefresh(*Account) bool { return true }

func (r *tokenRefreshKiroProfileRuntimeRefresher) NeedsRefresh(*Account, time.Duration) bool {
	return true
}

func (r *tokenRefreshKiroProfileRuntimeRefresher) Refresh(context.Context, *Account) (map[string]any, error) {
	return r.credentials, nil
}

func (r *tokenRefreshKiroProfileRuntimeRefresher) CacheKey(account *Account) string {
	return "test:kiro-profile:" + account.Platform
}

type tokenRefreshKiroProfileRuntimeUpstream struct {
	responses []*http.Response
	requests  []*http.Request
}

func (u *tokenRefreshKiroProfileRuntimeUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, errors.New("unexpected Do call")
}

func (u *tokenRefreshKiroProfileRuntimeUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests = append(u.requests, req)
	if len(u.responses) == 0 {
		return nil, errors.New("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func TestTokenRefreshServiceKiroProfilePrefillRuntime(t *testing.T) {
	account := &Account{
		ID:          9,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "old-token",
			"refresh_token": "refresh-token",
			"profile_arn":   kiroBuilderIDProfileARN,
		},
	}
	repo := &tokenRefreshKiroProfileRuntimeRepo{
		accountsByID: map[int64]*Account{account.ID: account},
	}
	upstream := &tokenRefreshKiroProfileRuntimeUpstream{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"profiles":[{"arn":"arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED"}]}`)),
			},
		},
	}
	cfg := &config.Config{
		TokenRefresh: config.TokenRefreshConfig{
			MaxRetries:          1,
			RetryBackoffSeconds: 0,
		},
	}
	service := NewTokenRefreshService(repo, nil, nil, nil, nil, nil, nil, nil, cfg, nil)
	service.SetKiroProfileResolverDeps(upstream, &TLSFingerprintProfileService{})
	refresher := &tokenRefreshKiroProfileRuntimeRefresher{credentials: map[string]any{
		"access_token":  "fresh-token",
		"refresh_token": "refresh-token",
		"profile_arn":   kiroBuilderIDProfileARN,
	}}

	err := service.refreshWithRetry(context.Background(), account, refresher, refresher, time.Hour)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "q.us-east-1.amazonaws.com", upstream.requests[0].URL.Host)
	require.Equal(t, "application/x-amz-json-1.0", upstream.requests[0].Header.Get("Content-Type"))
	require.Equal(t, kiroListAvailableProfilesTarget, upstream.requests[0].Header.Get("X-Amz-Target"))
	require.Equal(t, "Bearer fresh-token", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED", account.GetCredential("profile_arn"))
	require.Equal(t, account.GetCredential("profile_arn"), repo.accountsByID[account.ID].GetCredential("profile_arn"))
}
