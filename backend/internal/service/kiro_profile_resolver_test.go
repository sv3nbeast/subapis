package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type kiroProfileHTTPUpstream struct {
	mu        sync.Mutex
	responses []*http.Response
	requests  []*http.Request
}

func (u *kiroProfileHTTPUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected Do call")
}

func (u *kiroProfileHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests = append(u.requests, req)
	if len(u.responses) == 0 {
		return nil, fmt.Errorf("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

type kiroProfileRepo struct {
	AccountRepository
	account                *Account
	updateCredentialsCalls int
	lastCredentials        map[string]any
}

func (r *kiroProfileRepo) GetByID(context.Context, int64) (*Account, error) {
	if r.account == nil {
		return nil, fmt.Errorf("account not found")
	}
	return r.account, nil
}

func (r *kiroProfileRepo) Update(context.Context, *Account) error {
	return nil
}

func (r *kiroProfileRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.updateCredentialsCalls++
	r.lastCredentials = cloneCredentials(credentials)
	if r.account != nil {
		r.account.Credentials = cloneCredentials(credentials)
	}
	return nil
}

func (r *kiroProfileRepo) List(ctx context.Context, params pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

type kiroProfileRefresher struct {
	tokenInfo *KiroTokenInfo
	err       error
}

func (r *kiroProfileRefresher) RefreshAccountToken(context.Context, *Account) (*KiroTokenInfo, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.tokenInfo, nil
}

func (r *kiroProfileRefresher) BuildAccountCredentials(tokenInfo *KiroTokenInfo) map[string]any {
	if tokenInfo == nil {
		return nil
	}
	return map[string]any{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   tokenInfo.ExpiresAt,
		"profile_arn":  tokenInfo.ProfileArn,
	}
}

func newKiroProfileJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestKiroProfileResolverResolvesAndCachesMissingProfileArn(t *testing.T) {
	account := &Account{
		ID:          201,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
		},
	}
	repo := &kiroProfileRepo{account: account}
	upstream := &kiroProfileHTTPUpstream{
		responses: []*http.Response{
			newKiroProfileJSONResponse(http.StatusOK, `{"profiles":[{"arn":"arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED"}]}`),
		},
	}
	svc := &GatewayService{
		accountRepo:  repo,
		httpUpstream: upstream,
	}
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)

	endpoint := kiroEndpointConfig{Name: "KiroRuntime", Origin: "AI_EDITOR", URL: kiroKRSEndpointURL}
	buildResult, err := svc.buildKiroPayloadForAccountEndpoint(context.Background(), account, body, "claude-sonnet-4.6", "access-token", "claude-sonnet-4-6", nil, endpoint)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://q.us-east-1.amazonaws.com/", upstream.requests[0].URL.String())
	require.Equal(t, upstream.requests[0].URL.Host, upstream.requests[0].Host)
	require.Equal(t, "Bearer access-token", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "application/x-amz-json-1.0", upstream.requests[0].Header.Get("Content-Type"))
	require.Equal(t, kiroListAvailableProfilesTarget, upstream.requests[0].Header.Get("X-Amz-Target"))
	require.Contains(t, upstream.requests[0].Header.Get("User-Agent"), "api/codewhispererstreaming#1.0.34")
	require.Contains(t, upstream.requests[0].Header.Get("X-Amz-User-Agent"), "aws-sdk-js/1.0.34")
	require.Equal(t, "attempt=1; max=3", upstream.requests[0].Header.Get("Amz-Sdk-Request"))
	listBody, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"maxResults":10}`, string(listBody))

	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED", repo.lastCredentials["profile_arn"])
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED", account.GetCredential("profile_arn"))
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED", gjson.GetBytes(buildResult.Payload, "profileArn").String())
}

func TestKiroProfileResolverUsesOIDCRegionBeforeDefault(t *testing.T) {
	account := &Account{
		ID:          206,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"auth_method":  "idc",
			"region":       "eu-central-1",
		},
	}
	repo := &kiroProfileRepo{account: account}
	upstream := &kiroProfileHTTPUpstream{
		responses: []*http.Response{
			newKiroProfileJSONResponse(http.StatusOK, `{"profiles":[{"arn":"arn:aws:codewhisperer:eu-central-1:123456789012:profile/EU"}]}`),
		},
	}
	svc := &GatewayService{accountRepo: repo, httpUpstream: upstream}

	profileArn := svc.resolveAndPersistKiroProfileArnOnly(context.Background(), account, "access-token")

	require.Equal(t, "arn:aws:codewhisperer:eu-central-1:123456789012:profile/EU", profileArn)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "q.eu-central-1.amazonaws.com", upstream.requests[0].URL.Host)
	require.Equal(t, "eu-central-1", kiroAPIRegion(account))
}

func TestKiroProfileResolverFallsBackFromOIDCRegionToDefault(t *testing.T) {
	account := &Account{
		ID:          207,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"auth_method":  "idc",
			"region":       "eu-central-1",
		},
	}
	repo := &kiroProfileRepo{account: account}
	upstream := &kiroProfileHTTPUpstream{
		responses: []*http.Response{
			newKiroProfileJSONResponse(http.StatusOK, `{"profiles":[]}`),
			newKiroProfileJSONResponse(http.StatusOK, `{"profiles":[{"arn":"arn:aws:codewhisperer:us-east-1:123456789012:profile/US"}]}`),
		},
	}
	svc := &GatewayService{accountRepo: repo, httpUpstream: upstream}

	profileArn := svc.resolveAndPersistKiroProfileArnOnly(context.Background(), account, "access-token")

	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/US", profileArn)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "q.eu-central-1.amazonaws.com", upstream.requests[0].URL.Host)
	require.Equal(t, "q.us-east-1.amazonaws.com", upstream.requests[1].URL.Host)
	require.Equal(t, "us-east-1", kiroAPIRegion(account))
}

func TestKiroProfileResolverFallsBackToRefreshProfileArn(t *testing.T) {
	account := &Account{
		ID:          202,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "expired-token",
			"refresh_token": "refresh-token",
		},
	}
	repo := &kiroProfileRepo{account: account}
	upstream := &kiroProfileHTTPUpstream{
		responses: []*http.Response{
			newKiroProfileJSONResponse(http.StatusInternalServerError, `{"message":"temporary"}`),
			newKiroProfileJSONResponse(http.StatusInternalServerError, `{"message":"temporary"}`),
			newKiroProfileJSONResponse(http.StatusInternalServerError, `{"message":"temporary"}`),
		},
	}
	provider := NewKiroTokenProvider(repo, nil, nil)
	provider.kiroOAuthService = &kiroProfileRefresher{tokenInfo: &KiroTokenInfo{
		AccessToken: "fresh-token",
		ProfileArn:  "arn:aws:codewhisperer:us-west-2:123456789012:profile/REFRESHED",
		ExpiresAt:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}}
	svc := &GatewayService{
		accountRepo:       repo,
		httpUpstream:      upstream,
		kiroTokenProvider: provider,
	}
	originalSleep := kiroRetrySleep
	kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { kiroRetrySleep = originalSleep })
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)

	endpoint := kiroEndpointConfig{Name: "KiroRuntime", Origin: "AI_EDITOR", URL: kiroKRSEndpointURL}
	buildResult, err := svc.buildKiroPayloadForAccountEndpoint(context.Background(), account, body, "claude-sonnet-4.6", "expired-token", "claude-sonnet-4-6", nil, endpoint)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "fresh-token", account.GetCredential("access_token"))
	require.Equal(t, "arn:aws:codewhisperer:us-west-2:123456789012:profile/REFRESHED", account.GetCredential("profile_arn"))
	require.Equal(t, "arn:aws:codewhisperer:us-west-2:123456789012:profile/REFRESHED", gjson.GetBytes(buildResult.Payload, "profileArn").String())
}

func TestKiroProfileResolverDefaultQPayloadDoesNotResolveOrAttachProfileArn(t *testing.T) {
	account := &Account{
		ID:          203,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"auth_method":  "idc",
			"provider":     "AWS",
		},
	}
	repo := &kiroProfileRepo{account: account}
	upstream := &kiroProfileHTTPUpstream{
		responses: []*http.Response{
			newKiroProfileJSONResponse(http.StatusOK, `{"profiles":[{"arn":"arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED"}]}`),
		},
	}
	svc := &GatewayService{
		accountRepo:  repo,
		httpUpstream: upstream,
	}
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)

	buildResult, err := svc.buildKiroPayloadForAccount(context.Background(), account, body, "claude-sonnet-4.6", "access-token", "claude-sonnet-4-6", nil)

	require.NoError(t, err)
	require.Empty(t, upstream.requests)
	require.Equal(t, 0, repo.updateCredentialsCalls)
	require.Empty(t, gjson.GetBytes(buildResult.Payload, "profileArn").String())
}

func TestKiroProfileResolverSingleflightCoalescesConcurrentResolves(t *testing.T) {
	account := &Account{
		ID:          204,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
			"auth_method":  "idc",
			"provider":     "AWS",
		},
	}
	repo := &kiroProfileRepo{account: account}
	upstream := &kiroProfileHTTPUpstream{
		responses: []*http.Response{
			newKiroProfileJSONResponse(http.StatusOK, `{"profiles":[{"arn":"arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED-SF"}]}`),
		},
	}
	svc := &GatewayService{
		accountRepo:  repo,
		httpUpstream: upstream,
	}

	const goroutines = 8
	start := make(chan struct{})
	results := make(chan string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			results <- svc.resolveAndPersistKiroProfileArnOnly(context.Background(), account, "access-token")
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	for profileArn := range results {
		require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED-SF", profileArn)
	}
	require.Len(t, upstream.requests, 1)
	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/RESOLVED-SF", account.GetCredential("profile_arn"))
}

func TestKiroProfileResolverSingleflightDoesNotCacheFailure(t *testing.T) {
	account := &Account{
		ID:          205,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "access-token",
		},
	}
	repo := &kiroProfileRepo{account: account}
	upstream := &kiroProfileHTTPUpstream{
		responses: []*http.Response{
			newKiroProfileJSONResponse(http.StatusForbidden, `{"message":"not ready"}`),
			newKiroProfileJSONResponse(http.StatusOK, `{"profiles":[{"arn":"arn:aws:codewhisperer:us-east-1:123456789012:profile/RETRY"}]}`),
		},
	}
	svc := &GatewayService{
		accountRepo:  repo,
		httpUpstream: upstream,
	}

	require.Empty(t, svc.resolveAndPersistKiroProfileArnOnly(context.Background(), account, "access-token"))
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/RETRY", svc.resolveAndPersistKiroProfileArnOnly(context.Background(), account, "access-token"))
	require.Len(t, upstream.requests, 2)
	require.Equal(t, 1, repo.updateCredentialsCalls)
}
