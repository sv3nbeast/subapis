package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type companionProbeRecordingUpstream struct {
	mu       sync.Mutex
	requests []*http.Request
	proxies  []string
	profiles []*tlsfingerprint.Profile
}

func (u *companionProbeRecordingUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *companionProbeRecordingUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	u.requests = append(u.requests, req)
	u.proxies = append(u.proxies, proxyURL)
	u.profiles = append(u.profiles, profile)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"content-type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}, nil
}

func (u *companionProbeRecordingUpstream) snapshot() ([]*http.Request, []string, []*tlsfingerprint.Profile) {
	u.mu.Lock()
	defer u.mu.Unlock()
	requests := append([]*http.Request(nil), u.requests...)
	proxies := append([]string(nil), u.proxies...)
	profiles := append([]*tlsfingerprint.Profile(nil), u.profiles...)
	return requests, proxies, profiles
}

func waitForCompanionRequests(t *testing.T, upstream *companionProbeRecordingUpstream, want int) []*http.Request {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		requests, _, _ := upstream.snapshot()
		if len(requests) >= want {
			return requests
		}
		time.Sleep(10 * time.Millisecond)
	}
	requests, _, _ := upstream.snapshot()
	require.Len(t, requests, want)
	return requests
}

func TestClaudeCodeCompanionProbeService_SendsCapturedAuxAndTitleRequests(t *testing.T) {
	upstream := &companionProbeRecordingUpstream{}
	service := NewClaudeCodeCompanionProbeService(upstream)
	profile := tlsfingerprint.BuiltInDefaultProfile()

	service.MaybeTrigger(context.Background(), ClaudeCodeCompanionProbeInput{
		Account: &Account{
			ID:          42,
			Name:        "anthropic-oauth",
			Platform:    PlatformAnthropic,
			Type:        AccountTypeOAuth,
			Concurrency: 3,
		},
		Body:       []byte(`{"metadata":{"user_id":"user_abc#org=org_123#session_id=11111111-1111-4111-8111-111111111111"},"messages":[{"role":"user","content":"hello"}]}`),
		Token:      "oauth-token",
		TokenType:  "oauth",
		ProxyURL:   "http://proxy.local:8080",
		TLSProfile: profile,
		SessionID:  "11111111-1111-4111-8111-111111111111",
		Config: config.GatewayClaudeCodeMimicryConfig{
			Enabled: true,
			SyntheticCompanion: config.GatewayClaudeCodeSyntheticCompanionConfig{
				Enabled:            true,
				Mode:               config.ClaudeCodeSyntheticCompanionModeAuxAndTitle,
				MinIntervalSeconds: 300,
				TimeoutSeconds:     1,
				FailOpen:           true,
			},
		},
	})

	requests := waitForCompanionRequests(t, upstream, 7)
	proxies, _, profiles := func() ([]string, []string, []*tlsfingerprint.Profile) {
		_, proxies, profiles := upstream.snapshot()
		return proxies, nil, profiles
	}()

	paths := make([]string, 0, len(requests))
	for i, req := range requests {
		paths = append(paths, req.URL.Path)
		require.Equal(t, "http://proxy.local:8080", proxies[i])
		require.Same(t, profile, profiles[i])
	}
	require.Contains(t, paths, "/api/claude_cli/bootstrap")
	require.Contains(t, paths, "/api/claude_code_penguin_mode")
	require.Contains(t, paths, "/api/claude_code_grove")
	require.Contains(t, paths, "/api/oauth/account/settings")
	require.Contains(t, paths, "/v1/mcp_servers")
	require.Contains(t, paths, "/mcp-registry/v0/servers")
	require.Contains(t, paths, "/v1/messages")

	require.Equal(t, "claude-code/2.1.111", getHeaderRaw(requests[0].Header, "User-Agent"))
	require.Equal(t, "Bearer oauth-token", getHeaderRaw(requests[0].Header, "authorization"))
	require.Equal(t, "oauth-2025-04-20", getHeaderRaw(requests[0].Header, "anthropic-beta"))
}

func TestClaudeCodeCompanionProbeService_ThrottlesByAccountAndSession(t *testing.T) {
	upstream := &companionProbeRecordingUpstream{}
	service := NewClaudeCodeCompanionProbeService(upstream)
	input := ClaudeCodeCompanionProbeInput{
		Account:   &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		Body:      []byte(`{"metadata":{"user_id":"user_abc#session_id=22222222-2222-4222-8222-222222222222"},"messages":[{"role":"user","content":"hello"}]}`),
		Token:     "oauth-token",
		TokenType: "oauth",
		SessionID: "22222222-2222-4222-8222-222222222222",
		Config: config.GatewayClaudeCodeMimicryConfig{
			Enabled: true,
			SyntheticCompanion: config.GatewayClaudeCodeSyntheticCompanionConfig{
				Enabled:            true,
				Mode:               config.ClaudeCodeSyntheticCompanionModeAuxOnly,
				MinIntervalSeconds: 300,
				TimeoutSeconds:     1,
				FailOpen:           true,
			},
		},
	}

	service.MaybeTrigger(context.Background(), input)
	service.MaybeTrigger(context.Background(), input)

	_ = waitForCompanionRequests(t, upstream, 6)
	time.Sleep(50 * time.Millisecond)
	requests, _, _ := upstream.snapshot()
	require.Len(t, requests, 6)
}
