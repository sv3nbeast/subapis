package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type droidAccountTestRepo struct {
	AccountRepository
	accountsByID map[int64]*Account
}

func (r *droidAccountTestRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.accountsByID[id], nil
}

type droidQueuedHTTPUpstream struct {
	requests  []*http.Request
	responses []*http.Response
	callIndex int
}

func (s *droidQueuedHTTPUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return nil, nil
}

func (s *droidQueuedHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	s.requests = append(s.requests, req)
	resp := s.responses[s.callIndex]
	s.callIndex++
	return resp, nil
}

func newDroidJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func newDroidTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	return c, rec
}

func TestAccountTestService_DroidUsesFactoryUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newDroidTestContext()

	account := &Account{
		ID:          1,
		Name:        "droid-test",
		Platform:    PlatformDroid,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "factory-token",
		},
	}
	repo := &droidAccountTestRepo{accountsByID: map[int64]*Account{1: account}}
	upstream := &droidQueuedHTTPUpstream{
		responses: []*http.Response{
			newDroidJSONResponse(http.StatusUnauthorized, `{"error":"unauthorized"}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-sonnet-4-20250514", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "api.factory.ai", upstream.requests[0].URL.Host)
	require.Equal(t, "/api/llm/a/v1/messages", upstream.requests[0].URL.Path)
}
