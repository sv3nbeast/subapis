package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

func defaultClaudeUpstreamUserAgent() string {
	return claude.DefaultHeaders["User-Agent"]
}

func claudeUpstreamUserAgentFromSettings(ctx context.Context, settingService *SettingService) string {
	if settingService != nil {
		if ua := strings.TrimSpace(settingService.GetClaudeUpstreamUserAgent(ctx)); ua != "" {
			return ua
		}
	}
	return defaultClaudeUpstreamUserAgent()
}
