package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type grokAuthRecoveryTrackingBody struct {
	reader     io.Reader
	readCalls  int
	closeCalls int
}

func (b *grokAuthRecoveryTrackingBody) Read(p []byte) (int, error) {
	b.readCalls++
	return b.reader.Read(p)
}

func (b *grokAuthRecoveryTrackingBody) Close() error {
	b.closeCalls++
	return nil
}

type grokAuthRecoveryRepo struct {
	AccountRepository
	account     *Account
	updateCalls int
	errorCalls  int
	tempCalls   int
}

func (r *grokAuthRecoveryRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *grokAuthRecoveryRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.updateCalls++
	r.account.Credentials = cloneCredentials(credentials)
	return nil
}

func (r *grokAuthRecoveryRepo) SetError(context.Context, int64, string) error {
	r.errorCalls++
	return nil
}

func (r *grokAuthRecoveryRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

type grokAuthRecoveryTokenService struct{}

func (grokAuthRecoveryTokenService) RefreshAccountToken(context.Context, *Account) (*GrokTokenInfo, error) {
	return &GrokTokenInfo{AccessToken: "refreshed-access", RefreshToken: "refresh-token", ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
}

func (grokAuthRecoveryTokenService) BuildAccountCredentials(info *GrokTokenInfo) map[string]any {
	return map[string]any{
		"access_token": info.AccessToken, "refresh_token": info.RefreshToken,
		"expires_at": time.Unix(info.ExpiresAt, 0).UTC().Format(time.RFC3339),
	}
}

func TestGrokCredentialRecoveryCandidateIsNarrow(t *testing.T) {
	require.True(t, isGrokCredentialRecoveryCandidate(http.StatusUnauthorized, []byte(`{"error":"expired"}`)))
	require.True(t, isGrokCredentialRecoveryCandidate(http.StatusForbidden, []byte(`{"error":"Access denied"}`)))
	require.True(t, isGrokCredentialRecoveryCandidate(http.StatusForbidden, []byte(`{"error":"Access to the chat endpoint is denied. Please update permissions."}`)))
	require.False(t, isGrokCredentialRecoveryCandidate(http.StatusForbidden, []byte(`{"error":"regional policy denied"}`)))
	require.False(t, isGrokCredentialRecoveryCandidate(http.StatusPaymentRequired, []byte(`{"error":"spending limit"}`)))
}

func TestRetryGrokAfterCredentialRefreshLeavesSuccessfulStreamUntouched(t *testing.T) {
	payload := bytes.Repeat([]byte("stream-data-"), int(openAIUpstreamErrorBodyReadLimit/12)+4096)
	body := &grokAuthRecoveryTrackingBody{reader: bytes.NewReader(payload)}
	account := &Account{
		ID: 78, Platform: PlatformGrok, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "valid-access", "refresh_token": "refresh-token",
			"expires_at": time.Now().Add(4 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	service := &OpenAIGatewayService{
		grokTokenProvider: NewGrokTokenProvider(&grokAuthRecoveryRepo{account: account}, nil),
	}
	success := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	retryCalls := 0

	response, retried, err := service.retryGrokAfterCredentialRefresh(context.Background(), account, success, func(string) (*http.Response, error) {
		retryCalls++
		return nil, nil
	})

	require.NoError(t, err)
	require.False(t, retried)
	require.Same(t, success, response)
	require.Same(t, body, response.Body)
	require.Zero(t, retryCalls)
	require.Zero(t, body.readCalls)
	require.Zero(t, body.closeCalls)

	got, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestRetryGrokAfterCredentialRefreshRetriesSameAccountOnce(t *testing.T) {
	account := &Account{
		ID: 77, Platform: PlatformGrok, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "old-access", "refresh_token": "refresh-token",
			"expires_at": time.Now().Add(4 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &grokAuthRecoveryRepo{account: account}
	executor := NewGrokTokenRefresher(grokAuthRecoveryTokenService{})
	provider := NewGrokTokenProvider(repo, nil)
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, nil), executor)
	service := &OpenAIGatewayService{grokTokenProvider: provider}
	denied := &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"Access denied"}`)),
	}
	retryCalls := 0

	response, retried, err := service.retryGrokAfterCredentialRefresh(context.Background(), account, denied, func(token string) (*http.Response, error) {
		retryCalls++
		require.Equal(t, "refreshed-access", token)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})

	require.NoError(t, err)
	require.True(t, retried)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, 1, retryCalls)
	require.Equal(t, 1, repo.updateCalls)
	require.Zero(t, repo.errorCalls)
	require.Zero(t, repo.tempCalls)
}
