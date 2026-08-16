import { describe, expect, it } from 'vitest'
import { resolveCodexUsageWindow } from '@/utils/codexUsage'

describe('resolveCodexUsageWindow', () => {
  it('优先解析规范的 5h/7d 快照字段', () => {
    const snapshot = {
      codex_5h_used_percent: 12,
      codex_5h_reset_at: '2099-03-07T12:00:00Z',
      codex_7d_used_percent: 34,
      codex_7d_reset_at: '2099-03-13T12:00:00Z'
    }

    expect(resolveCodexUsageWindow(snapshot, '5h', new Date('2099-03-07T10:00:00Z'))).toEqual({
      usedPercent: 12,
      resetAt: '2099-03-07T12:00:00.000Z'
    })
    expect(resolveCodexUsageWindow(snapshot, '7d', new Date('2099-03-07T10:00:00Z'))).toEqual({
      usedPercent: 34,
      resetAt: '2099-03-13T12:00:00.000Z'
    })
  })

  it('兼容旧的 primary/secondary 窗口字段', () => {
    const snapshot = {
      codex_primary_used_percent: 20,
      codex_primary_reset_after_seconds: 600,
      codex_primary_window_minutes: 300,
      codex_secondary_used_percent: 60,
      codex_secondary_reset_after_seconds: 3600,
      codex_secondary_window_minutes: 10080,
      codex_usage_updated_at: '2099-03-07T10:00:00Z'
    }

    expect(resolveCodexUsageWindow(snapshot, '5h', new Date('2099-03-07T10:00:00Z'))).toEqual({
      usedPercent: 20,
      resetAt: '2099-03-07T10:10:00.000Z'
    })
    expect(resolveCodexUsageWindow(snapshot, '7d', new Date('2099-03-07T10:00:00Z'))).toEqual({
      usedPercent: 60,
      resetAt: '2099-03-07T11:00:00.000Z'
    })
  })

  it('窗口已过期时返回零使用率，避免继续显示旧额度', () => {
    expect(resolveCodexUsageWindow({
      codex_5h_used_percent: 95,
      codex_5h_reset_at: '2099-03-07T09:00:00Z'
    }, '5h', new Date('2099-03-07T10:00:00Z'))).toEqual({
      usedPercent: 0,
      resetAt: '2099-03-07T09:00:00.000Z'
    })
  })
})
