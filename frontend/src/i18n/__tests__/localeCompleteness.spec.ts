import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

const requiredKeys = [
  'usage.latency',
  'usage.latencyFirstToken',
  'usage.latencyDuration',
  'usage.tabs.ranking',
  'admin.usage.billingModeVideo',
  'admin.usage.tokenRanking.subtitle',
  'admin.usage.tokenRanking.userCount',
  'admin.usage.tokenRanking.rowHint',
  'admin.usage.tokenRanking.columns.user',
  'admin.usage.tokenRanking.columns.requests',
  'admin.usage.tokenRanking.columns.inputTokens',
  'admin.usage.tokenRanking.columns.outputTokens',
  'admin.usage.tokenRanking.columns.cacheTokens',
  'admin.usage.tokenRanking.columns.totalTokens',
  'admin.usage.tokenRanking.columns.cost',
  'admin.groups.videoPricing.title',
  'admin.groups.videoPricing.description',
  'admin.groups.videoPricing.independentMultiplier',
  'admin.groups.videoPricing.videoMultiplier',
  'admin.groups.videoPricing.modeHint',
  'admin.groups.videoPricing.finalPricePreview',
  'admin.groups.videoPricing.notConfigured',
  'admin.accounts.schedulerScore.hint',
  'admin.accounts.schedulerScore.ungrouped',
  'admin.users.form.roleLabel',
  'common.availableBalance',
  'common.frozenBalance',
  'common.totalBalance',
  'keys.lastUsedIP',
  'version.rollback',
  'version.rollbackSourceHint',
  'version.noRollbackVersions',
  'version.rollbackSelectVersion',
  'version.manualRollbackCommand',
  'version.deployScript',
  'version.deployDocker',
  'version.dockerEditCompose',
  'version.dockerRecreate',
  'version.copyCommand',
  'version.copied',
  'version.rollbackWarning',
  'version.rollingBack',
  'version.rollbackConfirm',
  'version.rollbackComplete',
  'version.rollbackFailed',
  'version.loadVersionsFailed'
] as const

function resolveLocaleValue(locale: Record<string, unknown>, key: string): unknown {
  return key.split('.').reduce<unknown>((value, part) => {
    if (!value || typeof value !== 'object') return undefined
    return (value as Record<string, unknown>)[part]
  }, locale)
}

describe('recent UI locale key completeness', () => {
  it.each([
    ['en', en],
    ['zh', zh]
  ] as const)('%s locale contains every required UI key', (_name, locale) => {
    for (const key of requiredKeys) {
      const value = resolveLocaleValue(locale, key)
      expect(value, key).toBeTypeOf('string')
      expect(value, key).not.toBe('')
      expect(value, key).not.toBe(key)
    }
  })

  it('uses Chinese labels for the usage latency column and its details', () => {
    expect(zh.usage.latency).toBe('延迟')
    expect(zh.usage.latencyFirstToken).toBe('首 Token')
    expect(zh.usage.latencyDuration).toBe('总耗时')
  })

  it('does not duplicate the version prefix supplied by the rollback UI', () => {
    expect(en.version.rollbackConfirm).toBe('Roll back to {version}')
    expect(zh.version.rollbackConfirm).toBe('回滚到 {version}')
  })
})
