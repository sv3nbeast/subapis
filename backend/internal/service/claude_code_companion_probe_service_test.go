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
		Body:         []byte(`{"metadata":{"user_id":"user_abc#org=org_123#session_id=11111111-1111-4111-8111-111111111111"},"messages":[{"role":"user","content":"hello"}]}`),
		Token:        "oauth-token",
		TokenType:    "oauth",
		ProxyURL:     "http://proxy.local:8080",
		TLSProfile:   profile,
		SessionID:    "11111111-1111-4111-8111-111111111111",
		RequestModel: "awsclaude4.5",
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

	requests := waitForCompanionRequests(t, upstream, 8)
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
	require.Equal(t, []string{
		"/v1/mcp_servers",
		"/api/claude_cli/bootstrap",
		"/api/claude_code_penguin_mode",
		"/api/claude_code_grove",
		"/api/oauth/profile",
		"/v1/mcp_servers",
		"/mcp-registry/v0/servers",
		"/v1/messages",
	}, paths)

	require.Equal(t, "axios/1.15.2", getHeaderRaw(requests[0].Header, "User-Agent"))
	require.Equal(t, "Bearer oauth-token", getHeaderRaw(requests[0].Header, "authorization"))
	require.Equal(t, "mcp-servers-2025-12-04", getHeaderRaw(requests[0].Header, "anthropic-beta"))
	require.Equal(t, "limit=1000", requests[0].URL.RawQuery)
	require.Equal(t, "claude-code/2.1.165", getHeaderRaw(requests[1].Header, "User-Agent"))
	require.Equal(t, "Bearer oauth-token", getHeaderRaw(requests[1].Header, "authorization"))
	require.Equal(t, "oauth-2025-04-20", getHeaderRaw(requests[1].Header, "anthropic-beta"))
	require.Equal(t, "entrypoint=sdk-cli&model=awsclaude4.5", requests[1].URL.RawQuery)
	require.Equal(t, "limit=1000", requests[5].URL.RawQuery)

	requestByPath := map[string]*http.Request{}
	for _, req := range requests {
		requestByPath[req.URL.Path] = req
	}
	require.Equal(t, "axios/1.15.2", getHeaderRaw(requestByPath["/api/claude_code_penguin_mode"].Header, "User-Agent"))
	require.Equal(t, "axios/1.15.2", getHeaderRaw(requestByPath["/api/oauth/profile"].Header, "User-Agent"))
	require.Equal(t, "", getHeaderRaw(requestByPath["/api/oauth/profile"].Header, "anthropic-beta"))
	require.Equal(t, "axios/1.15.2", getHeaderRaw(requestByPath["/v1/mcp_servers"].Header, "User-Agent"))
	require.Equal(t, "claude-cli/2.1.165 (external, sdk-cli)", getHeaderRaw(requestByPath["/mcp-registry/v0/servers"].Header, "User-Agent"))
	require.Equal(t, "version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth", requestByPath["/mcp-registry/v0/servers"].URL.RawQuery)
	body, err := io.ReadAll(requestByPath["/v1/messages"].Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"model":"awsclaude4.5-haiku"`)
	require.Contains(t, string(body), `Generate a concise, sentence-case title (3-7 words)`)
	require.Contains(t, string(body), `Return JSON with a single \"title\" field.`)
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
				Mode:               config.ClaudeCodeSyntheticCompanionModeAuxAndTitle,
				MinIntervalSeconds: 300,
				TimeoutSeconds:     1,
				FailOpen:           true,
			},
		},
	}

	service.MaybeTrigger(context.Background(), input)
	service.MaybeTrigger(context.Background(), input)

	_ = waitForCompanionRequests(t, upstream, 8)
	time.Sleep(50 * time.Millisecond)
	requests, _, _ := upstream.snapshot()
	require.Len(t, requests, 8)
}

func TestClaudeCodeCompanionProbeService_ThrottlesStatelessRequestsByAccount(t *testing.T) {
	upstream := &companionProbeRecordingUpstream{}
	service := NewClaudeCodeCompanionProbeService(upstream)
	cfg := config.GatewayClaudeCodeMimicryConfig{
		Enabled: true,
		SyntheticCompanion: config.GatewayClaudeCodeSyntheticCompanionConfig{
			Enabled:            true,
			Mode:               config.ClaudeCodeSyntheticCompanionModeAuxAndTitle,
			MinIntervalSeconds: 300,
			TimeoutSeconds:     1,
			FailOpen:           true,
		},
	}
	input := ClaudeCodeCompanionProbeInput{
		Account:   &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		Body:      []byte(`{"messages":[{"role":"user","content":"first prompt"}]}`),
		Token:     "oauth-token",
		TokenType: "oauth",
		Config:    cfg,
	}
	second := input
	second.Body = []byte(`{"messages":[{"role":"user","content":"different prompt"}]}`)

	require.Equal(t, "account-42", deriveClaudeCodeCompanionSessionID(input.Account, input.Body))
	require.Equal(t, "account-42", deriveClaudeCodeCompanionSessionID(second.Account, second.Body))

	service.MaybeTrigger(context.Background(), input)
	service.MaybeTrigger(context.Background(), second)

	_ = waitForCompanionRequests(t, upstream, 8)
	time.Sleep(50 * time.Millisecond)
	requests, _, _ := upstream.snapshot()
	require.Len(t, requests, 8)
}

func TestClaudeCodeCompanionProbeService_StatelessThrottleIsAccountScoped(t *testing.T) {
	upstream := &companionProbeRecordingUpstream{}
	service := NewClaudeCodeCompanionProbeService(upstream)
	cfg := config.GatewayClaudeCodeMimicryConfig{
		Enabled: true,
		SyntheticCompanion: config.GatewayClaudeCodeSyntheticCompanionConfig{
			Enabled:            true,
			Mode:               config.ClaudeCodeSyntheticCompanionModeAuxAndTitle,
			MinIntervalSeconds: 300,
			TimeoutSeconds:     1,
			FailOpen:           true,
		},
	}

	service.MaybeTrigger(context.Background(), ClaudeCodeCompanionProbeInput{
		Account:   &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		Body:      []byte(`{"messages":[{"role":"user","content":"same prompt"}]}`),
		Token:     "oauth-token",
		TokenType: "oauth",
		Config:    cfg,
	})
	service.MaybeTrigger(context.Background(), ClaudeCodeCompanionProbeInput{
		Account:   &Account{ID: 43, Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		Body:      []byte(`{"messages":[{"role":"user","content":"same prompt"}]}`),
		Token:     "oauth-token",
		TokenType: "oauth",
		Config:    cfg,
	})

	requests := waitForCompanionRequests(t, upstream, 16)
	require.Len(t, requests, 16)
}

func TestClaudeCodeCompanionProbeService_AuxOnlyOmitsTitleProbe(t *testing.T) {
	upstream := &companionProbeRecordingUpstream{}
	service := NewClaudeCodeCompanionProbeService(upstream)
	service.MaybeTrigger(context.Background(), ClaudeCodeCompanionProbeInput{
		Account:   &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		Body:      []byte(`{"messages":[{"role":"user","content":"hello"}]}`),
		Token:     "oauth-token",
		TokenType: "oauth",
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
	})

	requests := waitForCompanionRequests(t, upstream, 7)
	time.Sleep(50 * time.Millisecond)
	requests, _, _ = upstream.snapshot()
	require.Len(t, requests, 7)
	for _, req := range requests {
		require.NotEqual(t, "/v1/messages", req.URL.Path)
	}
}

func TestClaudeCodeCompanionInterval_DefaultRandomizedBetweenOneAndFiveHours(t *testing.T) {
	for range 100 {
		interval := claudeCodeCompanionInterval(0)
		require.GreaterOrEqual(t, interval, time.Hour)
		require.LessOrEqual(t, interval, 5*time.Hour)
	}
	require.Equal(t, 42*time.Second, claudeCodeCompanionInterval(42))
}
