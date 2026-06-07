package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type kiroRuntimeStateDefaultCooldownStore struct {
	state        *kirocooldown.State
	clearCalled  bool
	reserveCalls int
}

func (s *kiroRuntimeStateDefaultCooldownStore) ReserveRequest(context.Context, string) (time.Duration, error) {
	s.reserveCalls++
	return time.Second, nil
}

func (s *kiroRuntimeStateDefaultCooldownStore) MarkSuccess(context.Context, string) error {
	return nil
}

func (s *kiroRuntimeStateDefaultCooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *kiroRuntimeStateDefaultCooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *kiroRuntimeStateDefaultCooldownStore) GetState(context.Context, string) (*kirocooldown.State, error) {
	if s.clearCalled {
		return nil, nil
	}
	return s.state, nil
}

func (s *kiroRuntimeStateDefaultCooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	s.clearCalled = true
	return true, nil
}

type kiroRuntimeStateDefaultUpstream struct {
	responses []*http.Response
	requests  []*http.Request
}

func (u *kiroRuntimeStateDefaultUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected Do call")
}

func (u *kiroRuntimeStateDefaultUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests = append(u.requests, req)
	if len(u.responses) == 0 {
		return nil, fmt.Errorf("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func kiroRuntimeStateDefaultJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestExecuteKiroUpstreamClears429CooldownAndContinuesDefault(t *testing.T) {
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/DEFAULT",
		},
	}
	upstream := &kiroRuntimeStateDefaultUpstream{
		responses: []*http.Response{
			kiroRuntimeStateDefaultJSONResponse(http.StatusOK, `{"ok":true}`),
		},
	}
	store := &kiroRuntimeStateDefaultCooldownStore{
		state: &kirocooldown.State{
			Active:        true,
			Reason:        kirocooldown.CooldownReason429,
			CooldownUntil: time.Now().Add(time.Minute),
			Remaining:     time.Minute,
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   store,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	payload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, _, err := svc.executeKiroUpstream(context.Background(), account, payloadBytes, "claude-sonnet-4-6", "claude-sonnet-4-6", "test-token", nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, upstream.requests, 1)
	require.True(t, store.clearCalled)
	require.Equal(t, 0, store.reserveCalls, "Kiro 429 must not use ReserveRequest spacing")
}

func TestExecuteKiroUpstreamSuspendedCooldownStillFailsDefault(t *testing.T) {
	store := &kiroRuntimeStateDefaultCooldownStore{
		state: &kirocooldown.State{
			Active:        true,
			Reason:        kirocooldown.CooldownReasonSuspended,
			CooldownUntil: time.Now().Add(32500 * time.Millisecond),
			Remaining:     32500 * time.Millisecond,
		},
	}
	svc := &GatewayService{
		kiroCooldownStore: store,
	}

	_, _, err := svc.executeKiroUpstream(context.Background(), &Account{ID: 42}, []byte(`{}`), "claude-sonnet-4-6", "claude-sonnet-4-6", "token", nil)

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), kirocooldown.CooldownReasonSuspended)
	require.False(t, store.clearCalled)
	require.Equal(t, 0, store.reserveCalls)
}
