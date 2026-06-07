package service

import (
	"context"
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

type kiroMCP429CooldownStore struct {
	mark429Calls int
}

func (s *kiroMCP429CooldownStore) ReserveRequest(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *kiroMCP429CooldownStore) MarkSuccess(context.Context, string) error {
	return nil
}

func (s *kiroMCP429CooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	s.mark429Calls++
	return time.Minute, nil
}

func (s *kiroMCP429CooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *kiroMCP429CooldownStore) GetState(context.Context, string) (*kirocooldown.State, error) {
	return nil, nil
}

func (s *kiroMCP429CooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	return false, nil
}

type kiroMCP429Upstream struct {
	requests []*http.Request
}

func (u *kiroMCP429Upstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected Do call")
}

func (u *kiroMCP429Upstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests = append(u.requests, req)
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"message":"slow down"}`)),
	}, nil
}

func TestDoKiroMCPJSONRequestDoesNotMarkCooldownFor429(t *testing.T) {
	originalSleep := kiroRetrySleep
	sleepCalls := 0
	kiroRetrySleep = func(context.Context, time.Duration) error {
		sleepCalls++
		return nil
	}
	t.Cleanup(func() { kiroRetrySleep = originalSleep })

	upstream := &kiroMCP429Upstream{}
	store := &kiroMCP429CooldownStore{}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   store,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
	}

	resp, _, err := svc.doKiroMCPJSONRequest(context.Background(), account, "https://example.test/mcp", []byte(`{"jsonrpc":"2.0"}`), "token")

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, 0, sleepCalls)
	require.Equal(t, 0, store.mark429Calls)
}
