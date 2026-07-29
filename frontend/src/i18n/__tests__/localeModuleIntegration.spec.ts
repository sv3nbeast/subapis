import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

describe('locale module integration', () => {
  it('exposes newly split Chinese modules at their runtime paths', () => {
    expect(zh.admin.settings.features.modelPlaza.title).toBe('模型广场')
    expect(zh.admin.settings.panelRateLimit.title).toBe('面板接口限流')
    expect(zh.admin.audit.title).toBe('操作日志')
    expect(zh.admin.promptAudit.title).toBe('提示词审计')
    expect(zh.admin.promptAudit.runtime.queueBreakdown).toContain('处理中 {processing}')
    expect(zh.admin.promptAudit.events.requestId).toBe('请求 ID')
    expect(zh.admin.settings.oidc.clientId).toBe('客户端 ID')
    expect(zh.admin.settings.gatewayForwarding.systemBlockTitle).toBe('系统块 {index}')
    expect(zh.batchImage.actions.createJob).toBe('创建批量任务')
  })

  it('keeps local Chinese messages while merging the split modules', () => {
    expect(zh.modelMarket.navLabel).toBe('价格与模型')
    expect(zh.nav.modelStatus).toBe('模型状态')
    expect(zh.nav.docs).toBe('文档')
  })

  it('also exposes the English modules without falling back to raw keys', () => {
    expect(en.admin.settings.features.modelPlaza.title).toBe('Model Plaza')
    expect(en.admin.audit.title).toBe('Audit Logs')
    expect(en.admin.promptAudit.title).toBe('Prompt Audit')
    expect(en.batchImage.actions.createJob).toBe('Create batch job')
  })
})
