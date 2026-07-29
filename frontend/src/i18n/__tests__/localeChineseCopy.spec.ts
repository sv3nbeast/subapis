import { describe, expect, it } from 'vitest'

import zh from '../locales/zh'

describe('Chinese UI copy', () => {
  it('localizes recently added account and admin actions', () => {
    expect(zh.admin.accounts.bulkActions.probeUpstreamBilling).toBe('探测上游倍率')
    expect(zh.admin.accounts.openai.wsMode).toBe('WebSocket 模式')
    expect(zh.admin.accounts.usageWindow.kiroCredits).toBe('积分')
    expect(zh.admin.accounts.usageWindow.kiroBonus).toBe('奖励积分')
    expect(zh.admin.groups.supportedScopes.geminiText).toBe('Gemini 文本')
    expect(zh.admin.groups.supportedScopes.geminiImage).toBe('Gemini 图像')
  })

  it('localizes visible labels in web chat, monitoring, and composite routes', () => {
    expect(zh.webChat.temperature).toBe('温度')
    expect(zh.monitorCommon.past).toBe('过去')
    expect(zh.monitorCommon.now).toBe('现在')
    expect(zh.admin.groups.compositeRoutes.endpoints.countTokens).toContain('Token 计数')
    expect(zh.admin.channelMonitor.advanced.headerValuePlaceholder).toBe('请求头值')
    expect(zh.admin.channelMonitor.advanced.bodyJson).toBe('请求体 JSON')
  })
})
