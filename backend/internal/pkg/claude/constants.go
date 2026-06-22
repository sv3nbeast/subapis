// Package claude provides constants and helpers for Claude API integration.
package claude

// Claude Code 客户端相关常量

// Beta header 常量
//
// 这里的常量对齐真实 Claude Code CLI 的最新流量（截至 2026-06）。
// 选型参考：与 Parrot (src/transform/cc_mimicry.py) 的 BETAS 保持一致，
// 原因：Anthropic 上游会基于 anthropic-beta 的完整集合判定请求来源；
// 缺少任何"官方 Claude Code 请求才会带"的 beta，都会被降级到第三方额度，
// 对应报错：`Third-party apps now draw from your extra usage, not your plan limits.`
const (
	DefaultAcceptHeader          = "application/json"
	DefaultAcceptEncodingHeader  = "gzip, deflate, br, zstd"
	AxiosAcceptHeader            = "application/json, text/plain, */*"
	AxiosAcceptEncodingHeader    = "gzip, compress, deflate, br"
	BetaOAuth                    = "oauth-2025-04-20"
	BetaClaudeCode               = "claude-code-20250219"
	BetaInterleavedThinking      = "interleaved-thinking-2025-05-14"
	BetaFineGrainedToolStreaming = "fine-grained-tool-streaming-2025-05-14"
	BetaTokenCounting            = "token-counting-2024-11-01"
	BetaContext1M                = "context-1m-2025-08-07"
	BetaFastMode                 = "fast-mode-2026-02-01"

	// 新增（对齐官方 CLI 2.1.9x 以来的流量）
	BetaPromptCachingScope    = "prompt-caching-scope-2026-01-05"
	BetaEffort                = "effort-2025-11-24"
	BetaRedactThinking        = "redact-thinking-2026-02-12"
	BetaContextManagement     = "context-management-2025-06-27"
	BetaExtendedCacheTTL      = "extended-cache-ttl-2025-04-11"
	BetaAdvancedToolUse       = "advanced-tool-use-2025-11-20"
	BetaStructuredOutputs     = "structured-outputs-2025-12-15"
	BetaMidConversationSystem = "mid-conversation-system-2026-04-07"
)

// DroppedBetas 是转发时需要从 anthropic-beta header 中移除的 beta token 列表。
// 这些 token 是客户端特有的，不应透传给上游 API。
var DroppedBetas = []string{}

// DefaultBetaHeader Claude Code 客户端默认的 anthropic-beta header。
// 对齐 Claude Code CLI 2.1.165 主模型请求抓包。
const DefaultBetaHeader = BetaClaudeCode + "," + BetaOAuth + "," + BetaInterleavedThinking + "," + BetaContextManagement + "," + BetaPromptCachingScope + "," + BetaMidConversationSystem + "," + BetaAdvancedToolUse + "," + BetaEffort + "," + BetaExtendedCacheTTL

// MessageBetaHeaderNoTools /v1/messages 在无工具时的 beta header
//
// NOTE: Claude Code OAuth credentials are scoped to Claude Code. When we "mimic"
// Claude Code for non-Claude-Code clients, we must include the claude-code beta
// even if the request doesn't use tools, otherwise upstream may reject the
// request as a non-Claude-Code API request.
const MessageBetaHeaderNoTools = DefaultBetaHeader

// MessageBetaHeaderWithTools /v1/messages 在有工具时的 beta header
const MessageBetaHeaderWithTools = DefaultBetaHeader

// CountTokensBetaHeader count_tokens 请求使用的 anthropic-beta header
const CountTokensBetaHeader = DefaultBetaHeader + "," + BetaTokenCounting

// HaikuBetaHeader Haiku 模型使用的 anthropic-beta header（不需要 claude-code beta）。
// 对齐 Claude Code CLI 2.1.165 title/Haiku 请求抓包。
const HaikuBetaHeader = BetaOAuth + "," + BetaInterleavedThinking + "," + BetaContextManagement + "," + BetaPromptCachingScope + "," + BetaMidConversationSystem + "," + BetaEffort + "," + BetaStructuredOutputs

// APIKeyBetaHeader API-key 账号建议使用的 anthropic-beta header（不包含 oauth）
const APIKeyBetaHeader = BetaClaudeCode + "," + BetaInterleavedThinking + "," + BetaContextManagement + "," + BetaPromptCachingScope + "," + BetaMidConversationSystem + "," + BetaAdvancedToolUse + "," + BetaEffort + "," + BetaExtendedCacheTTL

// APIKeyHaikuBetaHeader Haiku 模型在 API-key 账号下使用的 anthropic-beta header（不包含 oauth / claude-code）
const APIKeyHaikuBetaHeader = BetaInterleavedThinking + "," + BetaContextManagement + "," + BetaPromptCachingScope + "," + BetaMidConversationSystem + "," + BetaEffort + "," + BetaStructuredOutputs

// DefaultCacheControlTTL 是网关代理为自己生成的 cache_control 块默认使用的 ttl。
// 真实 Claude Code CLI 当前使用 "1h"，但本仓策略是"客户端透传 ttl 优先；
// 客户端缺省时统一使用 5m"，这样既不浪费 1h 缓存额度，也保留客户端自定义能力。
const DefaultCacheControlTTL = "5m"

// CLICurrentVersion 是 sub2api 当前对外伪装的 Claude Code CLI 版本号（三段 semver）。
// 用于 billing attribution block 中的 cc_version=X.Y.Z.{fp} 前缀以及 fingerprint 计算。
// 必须与 DefaultHeaders["User-Agent"] 中的版本号严格一致；不一致会被 Anthropic 判第三方。
const CLICurrentVersion = "2.1.156"

// FullClaudeCodeMimicryBetas 返回最"像"真实 Claude Code CLI 的完整 beta 列表，
// 用于 OAuth 账号伪装成 Claude Code 时使用。
// 顺序与真实 CLI 抓包一致。
//
// 使用建议：
//   - OAuth 账号 + 非 haiku：追加这整份列表，再按需保留 client 带来的 beta。
//   - OAuth 账号 + haiku：Anthropic 对 haiku 不做 third-party 判定，使用 HaikuBetaHeader 即可。
//   - API-key 账号：不要使用本函数，参见 APIKeyBetaHeader。
//   - 不默认加入 redact-thinking，避免上游抹除 thinking 内容；客户端显式传入时由合并逻辑保留。
func FullClaudeCodeMimicryBetas() []string {
	return []string{
		BetaClaudeCode,
		BetaOAuth,
		BetaInterleavedThinking,
		BetaContextManagement,
		BetaPromptCachingScope,
		BetaMidConversationSystem,
		BetaAdvancedToolUse,
		BetaEffort,
		BetaExtendedCacheTTL,
	}
}

// DefaultHeaders 是 Claude Code 客户端默认请求头(plain CLI 主对话形式)。
// 与 PlainCLICanonicalUserAgent / PlainCLICanonicalFingerprint 一一对应。
var DefaultHeaders = map[string]string{
	// Keep these in sync with current official Claude Code CLI traffic.
	"User-Agent":                                "claude-cli/2.1.156 (external, cli)",
	"X-Stainless-Lang":                          "js",
	"X-Stainless-Package-Version":               "0.94.0",
	"X-Stainless-OS":                            "MacOS",
	"X-Stainless-Arch":                          "arm64",
	"X-Stainless-Runtime":                       "node",
	"X-Stainless-Runtime-Version":               "v24.3.0",
	"X-Stainless-Retry-Count":                   "0",
	"X-Stainless-Timeout":                       "600",
	"X-App":                                     "cli",
	"Anthropic-Dangerous-Direct-Browser-Access": "true",
}

// Canonical UA / fingerprint 按入站 UA 形式分两套:plain CLI(主对话)与
// agent-sdk(Claude Code Task 子代理 / Agent SDK 桥接)。
//
// 历史背景:网关曾把所有入站 UA 一律改写为 PlainCLICanonicalUserAgent,
// 同时 X-Stainless-* 指纹按 account.ID 缓存,首位入站客户端是 Windows 时
// 整账号锁 Windows,后续 agent-sdk 形式的 admin 请求被强行套上
// plain CLI UA + Windows 指纹,messages body 又含 agent-sdk 特征,身份
// 严重不一致触发 claude-opus-4-8 退化输出(反复 count 直到 max_tokens)。
//
// 修复:按入站 UA 形式分两套 canonical,每套内部版本号/OS/Arch 全部死写为
// 固定值,fingerprint cache key 升级为 fingerprint:<account.ID>:<form>。
// agent-sdk 形式锁 admin 真实的 2.1.181 + MacOS/arm64;plain CLI 形式保留
// 2.1.156 + MacOS/arm64(版本号不变,只把曾被 Windows 污染的指纹改回 MacOS)。
const (
	// PlainCLICanonicalUserAgent 是 plain Claude CLI 主对话形式的统一 UA。
	// 版本号与 CLICurrentVersion 一致,不轻易升版避免触发上游 prompt cache 失效。
	PlainCLICanonicalUserAgent = "claude-cli/2.1.156 (external, cli)"

	// AgentSDKCanonicalUserAgent 是 Claude Code Task 子代理 / Agent SDK 桥接形式
	// 的统一 UA,对齐 admin 真实客户端 2.1.181 + agent-sdk/0.3.181。
	AgentSDKCanonicalUserAgent = "claude-cli/2.1.181 (external, claude-desktop-3p, agent-sdk/0.3.181)"
)

// CanonicalFingerprint 表示一套固定的 X-Stainless-* 指纹字段(不含 ClientID)。
// 真正写入缓存时 ClientID 由 generateClientID 生成,其余字段直接拷贝该常量。
type CanonicalFingerprint struct {
	UserAgent               string
	StainlessLang           string
	StainlessPackageVersion string
	StainlessOS             string
	StainlessArch           string
	StainlessRuntime        string
	StainlessRuntimeVersion string
}

// PlainCLICanonicalFingerprint 是 plain CLI 形式的固定指纹。
// 无论入站客户端实际是 Mac / Windows / Linux,上游一律收到该套 MacOS 指纹。
var PlainCLICanonicalFingerprint = CanonicalFingerprint{
	UserAgent:               PlainCLICanonicalUserAgent,
	StainlessLang:           "js",
	StainlessPackageVersion: "0.94.0",
	StainlessOS:             "MacOS",
	StainlessArch:           "arm64",
	StainlessRuntime:        "node",
	StainlessRuntimeVersion: "v24.3.0",
}

// AgentSDKCanonicalFingerprint 是 agent-sdk 形式的固定指纹。
// 与 plain CLI 共享 MacOS/arm64,但 UA / 形式标识不同,以匹配 admin 真实 Mac 客户端。
var AgentSDKCanonicalFingerprint = CanonicalFingerprint{
	UserAgent:               AgentSDKCanonicalUserAgent,
	StainlessLang:           "js",
	StainlessPackageVersion: "0.94.0",
	StainlessOS:             "MacOS",
	StainlessArch:           "arm64",
	StainlessRuntime:        "node",
	StainlessRuntimeVersion: "v24.3.0",
}

// Model 表示一个 Claude 模型
type Model struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// DefaultModels Claude Code 客户端支持的默认模型列表
var DefaultModels = []Model{
	{
		ID:          "claude-opus-4-5-20251101",
		Type:        "model",
		DisplayName: "Claude Opus 4.5",
		CreatedAt:   "2025-11-01T00:00:00Z",
	},
	{
		ID:          "claude-opus-4-6",
		Type:        "model",
		DisplayName: "Claude Opus 4.6",
		CreatedAt:   "2026-02-06T00:00:00Z",
	},
	{
		ID:          "claude-opus-4-7",
		Type:        "model",
		DisplayName: "Claude Opus 4.7",
		CreatedAt:   "2026-04-17T00:00:00Z",
	},
	{
		ID:          "claude-opus-4-8",
		Type:        "model",
		DisplayName: "Claude Opus 4.8",
		CreatedAt:   "2026-05-29T00:00:00Z",
	},
	{
		ID:          "claude-fable-5",
		Type:        "model",
		DisplayName: "Claude Fable 5",
		CreatedAt:   "2026-06-09T00:00:00Z",
	},
	{
		ID:          "claude-sonnet-4-6",
		Type:        "model",
		DisplayName: "Claude Sonnet 4.6",
		CreatedAt:   "2026-02-18T00:00:00Z",
	},
	{
		ID:          "claude-sonnet-4-5-20250929",
		Type:        "model",
		DisplayName: "Claude Sonnet 4.5",
		CreatedAt:   "2025-09-29T00:00:00Z",
	},
	{
		ID:          "claude-haiku-4-5-20251001",
		Type:        "model",
		DisplayName: "Claude Haiku 4.5",
		CreatedAt:   "2025-10-01T00:00:00Z",
	},
}

// DefaultModelIDs 返回默认模型的 ID 列表
func DefaultModelIDs() []string {
	ids := make([]string, len(DefaultModels))
	for i, m := range DefaultModels {
		ids[i] = m.ID
	}
	return ids
}

// DefaultTestModel 测试时使用的默认模型
const DefaultTestModel = "claude-sonnet-4-5-20250929"

// ModelIDOverrides Claude OAuth 请求需要的模型 ID 映射
var ModelIDOverrides = map[string]string{
	"claude-sonnet-4-5": "claude-sonnet-4-5-20250929",
	"claude-opus-4-5":   "claude-opus-4-5-20251101",
	"claude-haiku-4-5":  "claude-haiku-4-5-20251001",
}

// ModelIDReverseOverrides 用于将上游模型 ID 还原为短名
var ModelIDReverseOverrides = map[string]string{
	"claude-sonnet-4-5-20250929": "claude-sonnet-4-5",
	"claude-opus-4-5-20251101":   "claude-opus-4-5",
	"claude-haiku-4-5-20251001":  "claude-haiku-4-5",
}

// NormalizeModelID 根据 Claude OAuth 规则映射模型
func NormalizeModelID(id string) string {
	if id == "" {
		return id
	}
	if mapped, ok := ModelIDOverrides[id]; ok {
		return mapped
	}
	return id
}

// DenormalizeModelID 将上游模型 ID 转换为短名
func DenormalizeModelID(id string) string {
	if id == "" {
		return id
	}
	if mapped, ok := ModelIDReverseOverrides[id]; ok {
		return mapped
	}
	return id
}
