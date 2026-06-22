package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

// defaultClaudeUpstreamUserAgent 返回 plain CLI canonical UA(主对话路径)。
// 历史上整个网关只用这一个 UA 转发所有上游请求——但这会让 agent-sdk 形式的
// 入站(body 含 system-reminder / Task tool / skills 等 agent-sdk 特征)被
// 改写成 plain CLI UA,身份不一致触发 4-8 死循环。新逻辑见 claudeUpstreamUserAgent。
func defaultClaudeUpstreamUserAgent() string {
	return claude.PlainCLICanonicalUserAgent
}

// claudeUpstreamUserAgentFromSettings 仍是后台覆盖兜底入口——若管理员在
// settings 中显式配置了 upstream UA,优先使用;否则走 canonical 路径。
//
// 注意:该函数本身不知道入站 UA 形式,只用于 settings 覆盖场景及 channel
// monitor / account usage 等"网关自身作为客户端"的路径(那些场景永远使用
// plain CLI canonical)。真实 forward 路径请用 GatewayService.claudeUpstreamUserAgent。
func claudeUpstreamUserAgentFromSettings(ctx context.Context, settingService *SettingService) string {
	if settingService != nil {
		if ua := strings.TrimSpace(settingService.GetClaudeUpstreamUserAgent(ctx)); ua != "" {
			return ua
		}
	}
	return defaultClaudeUpstreamUserAgent()
}

// canonicalUpstreamUserAgentForForm 根据入站 UA 形式选择上游 canonical UA。
// 这是修 4-8 死循环的核心:agent-sdk 形式入站时上游也用 agent-sdk 形式 UA,
// body 中 agent-sdk 特征与 header UA 形式保持一致,避免身份冲突。
func canonicalUpstreamUserAgentForForm(form UAForm) string {
	if form == UAFormAgentSDK {
		return claude.AgentSDKCanonicalUserAgent
	}
	return claude.PlainCLICanonicalUserAgent
}
