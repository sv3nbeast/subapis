package service

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
)

// formBucketCacheStub 是一个按 (accountID, form) 双键索引的内存 stub,
// 用于验证 GetOrCreateFingerprint 在 plain CLI 与 agent-sdk 之间互不污染。
type formBucketCacheStub struct {
	store map[string]*Fingerprint
}

func newFormBucketCacheStub() *formBucketCacheStub {
	return &formBucketCacheStub{store: map[string]*Fingerprint{}}
}

func (s *formBucketCacheStub) key(accountID int64, form UAForm) string {
	return string(form) + ":" + strconv.FormatInt(accountID, 10)
}

func (s *formBucketCacheStub) GetFingerprint(_ context.Context, accountID int64, form UAForm) (*Fingerprint, error) {
	if fp, ok := s.store[s.key(accountID, form)]; ok {
		// 拷贝避免外部修改影响后续读
		copy := *fp
		return &copy, nil
	}
	return nil, nil
}

func (s *formBucketCacheStub) SetFingerprint(_ context.Context, accountID int64, form UAForm, fp *Fingerprint) error {
	if fp == nil {
		delete(s.store, s.key(accountID, form))
		return nil
	}
	copy := *fp
	s.store[s.key(accountID, form)] = &copy
	return nil
}

func (s *formBucketCacheStub) GetMaskedSessionID(_ context.Context, _ int64) (string, error) {
	return "", nil
}

func (s *formBucketCacheStub) SetMaskedSessionID(_ context.Context, _ int64, _ string) error {
	return nil
}

func TestClassifyUAForm(t *testing.T) {
	cases := []struct {
		name string
		ua   string
		want UAForm
	}{
		{"plain cli", "claude-cli/2.1.177 (external, cli)", UAFormPlainCLI},
		{"agent-sdk full", "claude-cli/2.1.181 (external, claude-desktop-3p, agent-sdk/0.3.181)", UAFormAgentSDK},
		{"agent-sdk uppercase", "claude-cli/2.1.181 (external, claude-desktop-3p, Agent-SDK/0.3.181)", UAFormAgentSDK},
		{"electron desktop", "Mozilla/5.0 Claude/1.0 Electron/30.0.0", UAFormPlainCLI},
		{"empty", "", UAFormPlainCLI},
		{"random third-party", "python-anthropic/0.40.0", UAFormPlainCLI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ClassifyUAForm(tc.ua))
		})
	}
}

func TestCanonicalUpstreamUserAgentForForm(t *testing.T) {
	require.Equal(t, claude.PlainCLICanonicalUserAgent, canonicalUpstreamUserAgentForForm(UAFormPlainCLI))
	require.Equal(t, claude.AgentSDKCanonicalUserAgent, canonicalUpstreamUserAgentForForm(UAFormAgentSDK))
	// 未来若引入新 form,未识别 form 兜底为 plain CLI
	require.Equal(t, claude.PlainCLICanonicalUserAgent, canonicalUpstreamUserAgentForForm(UAForm("future-unknown")))
}

func TestGetOrCreateFingerprint_FirstCallPlainCLI_UsesCanonical_IgnoresInboundOSHeader(t *testing.T) {
	cache := newFormBucketCacheStub()
	svc := NewIdentityService(cache)

	// 模拟 admin 真实事故:首位入站客户端 Windows headers,canonical 必须强制 MacOS
	headers := http.Header{}
	headers.Set("User-Agent", "claude-cli/2.1.185 (external, cli)")
	headers.Set("X-Stainless-OS", "Windows")
	headers.Set("X-Stainless-Arch", "x64")

	fp, err := svc.GetOrCreateFingerprint(context.Background(), 1535, headers, UAFormPlainCLI)
	require.NoError(t, err)
	require.NotNil(t, fp)
	require.Equal(t, claude.PlainCLICanonicalUserAgent, fp.UserAgent)
	require.Equal(t, "MacOS", fp.StainlessOS)
	require.Equal(t, "arm64", fp.StainlessArch)
	require.NotEmpty(t, fp.ClientID)
}

func TestGetOrCreateFingerprint_FirstCallAgentSDK_UsesCanonical(t *testing.T) {
	cache := newFormBucketCacheStub()
	svc := NewIdentityService(cache)

	// agent-sdk 入站也忽略入站 X-Stainless-* 头,统一 canonical MacOS/arm64
	headers := http.Header{}
	headers.Set("User-Agent", "claude-cli/2.1.181 (external, claude-desktop-3p, agent-sdk/0.3.181)")
	headers.Set("X-Stainless-OS", "Windows")
	headers.Set("X-Stainless-Arch", "x64")

	fp, err := svc.GetOrCreateFingerprint(context.Background(), 1535, headers, UAFormAgentSDK)
	require.NoError(t, err)
	require.NotNil(t, fp)
	require.Equal(t, claude.AgentSDKCanonicalUserAgent, fp.UserAgent)
	require.Equal(t, "MacOS", fp.StainlessOS)
	require.Equal(t, "arm64", fp.StainlessArch)
	require.NotEmpty(t, fp.ClientID)
}

func TestGetOrCreateFingerprint_SameAccountTwoForms_IndependentBuckets(t *testing.T) {
	cache := newFormBucketCacheStub()
	svc := NewIdentityService(cache)
	ctx := context.Background()

	plainHeaders := http.Header{}
	plainHeaders.Set("User-Agent", "claude-cli/2.1.185 (external, cli)")
	plainFP, err := svc.GetOrCreateFingerprint(ctx, 1535, plainHeaders, UAFormPlainCLI)
	require.NoError(t, err)

	agentHeaders := http.Header{}
	agentHeaders.Set("User-Agent", "claude-cli/2.1.181 (external, claude-desktop-3p, agent-sdk/0.3.181)")
	agentFP, err := svc.GetOrCreateFingerprint(ctx, 1535, agentHeaders, UAFormAgentSDK)
	require.NoError(t, err)

	// 两个 form 的 cache 互不影响,UA 各自 canonical,ClientID 互不相同
	require.Equal(t, claude.PlainCLICanonicalUserAgent, plainFP.UserAgent)
	require.Equal(t, claude.AgentSDKCanonicalUserAgent, agentFP.UserAgent)
	require.NotEqual(t, plainFP.ClientID, agentFP.ClientID)

	// 再次取应命中各自缓存,UA/ClientID 稳定
	plainFP2, _ := svc.GetOrCreateFingerprint(ctx, 1535, plainHeaders, UAFormPlainCLI)
	require.Equal(t, plainFP.ClientID, plainFP2.ClientID)

	agentFP2, _ := svc.GetOrCreateFingerprint(ctx, 1535, agentHeaders, UAFormAgentSDK)
	require.Equal(t, agentFP.ClientID, agentFP2.ClientID)
}

func TestGetOrCreateFingerprint_LegacyWindowsCache_NormalizedToCanonical(t *testing.T) {
	// 模拟现网 redis 残留:旧版本写入了 Windows 指纹,新版本读取时应一次性
	// 覆写为 canonical MacOS,ClientID 保留。
	cache := newFormBucketCacheStub()
	legacy := &Fingerprint{
		ClientID:                "legacy-client-id-deadbeef",
		UserAgent:               "claude-cli/2.1.185 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.94.0",
		StainlessOS:             "Windows",
		StainlessArch:           "x64",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v24.3.0",
		UpdatedAt:               1,
	}
	require.NoError(t, cache.SetFingerprint(context.Background(), 1535, UAFormPlainCLI, legacy))

	svc := NewIdentityService(cache)
	fp, err := svc.GetOrCreateFingerprint(context.Background(), 1535, http.Header{}, UAFormPlainCLI)
	require.NoError(t, err)
	require.Equal(t, "legacy-client-id-deadbeef", fp.ClientID, "ClientID 应保留")
	require.Equal(t, claude.PlainCLICanonicalUserAgent, fp.UserAgent)
	require.Equal(t, "MacOS", fp.StainlessOS)
	require.Equal(t, "arm64", fp.StainlessArch)
}

func TestGatewayService_ClaudeUpstreamUserAgent_FollowsInboundUAForm(t *testing.T) {
	// 没有 settings override 时,按 ctx 入站 UA 形式选择上游 UA。
	// 这是修 4-8 死循环的核心路径。
	svc := &GatewayService{}

	plainCtx := SetClaudeCodeUserAgent(context.Background(), "claude-cli/2.1.177 (external, cli)")
	require.Equal(t, claude.PlainCLICanonicalUserAgent, svc.claudeUpstreamUserAgent(plainCtx))

	agentCtx := SetClaudeCodeUserAgent(context.Background(), "claude-cli/2.1.181 (external, claude-desktop-3p, agent-sdk/0.3.181)")
	require.Equal(t, claude.AgentSDKCanonicalUserAgent, svc.claudeUpstreamUserAgent(agentCtx))

	// 入站 UA 缺失时兜底 plain CLI canonical
	require.Equal(t, claude.PlainCLICanonicalUserAgent, svc.claudeUpstreamUserAgent(context.Background()))
}
