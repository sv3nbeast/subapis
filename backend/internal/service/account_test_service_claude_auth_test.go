//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type claudeAccountTestRepo struct {
	AccountRepository
	setErrorID  int64
	setErrorMsg string
	setErrorErr error
}

func (r *claudeAccountTestRepo) SetError(_ context.Context, id int64, message string) error {
	r.setErrorID = id
	r.setErrorMsg = message
	return r.setErrorErr
}

type claudeAccountTestUpstream struct {
	HTTPUpstream
	responses []*http.Response
}

func (u *claudeAccountTestUpstream) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	if len(u.responses) == 0 {
		return nil, fmt.Errorf("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func newClaudeAccountTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/2591/test", nil)
	return c, recorder
}

func newClaudeAccountTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type failingClaudeAccountTestRepo struct {
	claudeAccountTestRepo
}

func nativeClaudeTestAccount() *Account {
	return &Account{
		ID:          2591,
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-native-test",
			"base_url": "http://127.0.0.1:8787",
		},
		Extra: map[string]any{"anthropic_passthrough": true},
	}
}

func newNativeClaudeAccountTestService(repo AccountRepository, upstream HTTPUpstream) *AccountTestService {
	return &AccountTestService{
		accountRepo:  repo,
		httpUpstream: upstream,
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled:           false,
			AllowInsecureHTTP: true,
		}}},
	}
}

func TestClaudeAccountTestDeterministicNativeOAuth401SetsError(t *testing.T) {
	for _, message := range []string{
		`{"type":"error","error":{"message":"OAuth access token has expired. Re-authenticate to continue."}}`,
		`{"error":{"message":"OAuth authentication could not be refreshed; re-authenticate to continue"}}`,
	} {
		t.Run(message, func(t *testing.T) {
			ctx, _ := newClaudeAccountTestContext()
			repo := &claudeAccountTestRepo{}
			svc := newNativeClaudeAccountTestService(repo, &claudeAccountTestUpstream{responses: []*http.Response{
				newClaudeAccountTestResponse(http.StatusUnauthorized, message),
			}})

			err := svc.testClaudeAccountConnection(ctx, nativeClaudeTestAccount(), "claude-opus-5")

			require.Error(t, err)
			require.Equal(t, int64(2591), repo.setErrorID)
			require.Contains(t, repo.setErrorMsg, "API returned 401")
		})
	}
}

func TestClaudeAccountTestUnknown401DoesNotSetError(t *testing.T) {
	ctx, _ := newClaudeAccountTestContext()
	repo := &claudeAccountTestRepo{}
	svc := newNativeClaudeAccountTestService(repo, &claudeAccountTestUpstream{responses: []*http.Response{
		newClaudeAccountTestResponse(http.StatusUnauthorized, `{"error":{"message":"invalid authentication credentials"}}`),
	}})

	err := svc.testClaudeAccountConnection(ctx, nativeClaudeTestAccount(), "claude-opus-5")

	require.Error(t, err)
	require.Zero(t, repo.setErrorID)
}

func TestClaudeAccountTestOAuth401RequiresNativePassthroughAccount(t *testing.T) {
	ctx, _ := newClaudeAccountTestContext()
	account := nativeClaudeTestAccount()
	account.Extra["anthropic_passthrough"] = false
	repo := &claudeAccountTestRepo{}
	svc := newNativeClaudeAccountTestService(repo, &claudeAccountTestUpstream{responses: []*http.Response{
		newClaudeAccountTestResponse(http.StatusUnauthorized, `{"error":{"message":"OAuth access token has expired. Re-authenticate to continue."}}`),
	}})

	err := svc.testClaudeAccountConnection(ctx, account, "claude-opus-5")

	require.Error(t, err)
	require.Zero(t, repo.setErrorID)
}

func TestClaudeAccountTest403StillSetsError(t *testing.T) {
	ctx, _ := newClaudeAccountTestContext()
	repo := &claudeAccountTestRepo{}
	svc := newNativeClaudeAccountTestService(repo, &claudeAccountTestUpstream{responses: []*http.Response{
		newClaudeAccountTestResponse(http.StatusForbidden, `{"error":{"message":"forbidden"}}`),
	}})

	err := svc.testClaudeAccountConnection(ctx, nativeClaudeTestAccount(), "claude-opus-5")

	require.Error(t, err)
	require.Equal(t, int64(2591), repo.setErrorID)
}

func TestClaudeAccountTestSetErrorFailurePreservesUpstreamError(t *testing.T) {
	ctx, recorder := newClaudeAccountTestContext()
	repo := &failingClaudeAccountTestRepo{claudeAccountTestRepo: claudeAccountTestRepo{setErrorErr: errors.New("database unavailable")}}
	svc := newNativeClaudeAccountTestService(repo, &claudeAccountTestUpstream{responses: []*http.Response{
		newClaudeAccountTestResponse(http.StatusUnauthorized, `{"error":{"message":"OAuth authentication could not be refreshed; re-authenticate to continue"}}`),
	}})

	err := svc.testClaudeAccountConnection(ctx, nativeClaudeTestAccount(), "claude-opus-5")

	require.EqualError(t, err, `API returned 401: {"error":{"message":"OAuth authentication could not be refreshed; re-authenticate to continue"}}`)
	require.Contains(t, recorder.Body.String(), "OAuth authentication could not be refreshed")
}
