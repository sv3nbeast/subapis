import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import GrokQuotaProbeCell from '../GrokQuotaProbeCell.vue'
import type { Account } from '@/types'

const { queryQuota } = vi.hoisted(() => ({
  queryQuota: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    grok: {
      queryQuota
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const account = {
  id: 3863,
  name: 'Grok OAuth',
  platform: 'grok',
  type: 'oauth'
} as Account

describe('GrokQuotaProbeCell', () => {
  it('只保留额度刷新操作，不显示无效的重置入口或重复摘要', async () => {
    queryQuota.mockResolvedValue({
      source: 'billing',
      billing: {
        credit_usage_percent: 56,
        credit_remaining_percent: 44,
        current_period_type: 'USAGE_PERIOD_TYPE_WEEKLY',
        current_period_start: '2026-07-09T18:40:47Z',
        current_period_end: '2026-07-16T18:40:47Z',
        on_demand_cap: 0,
        on_demand_used: 0,
        on_demand_remaining: 0,
        prepaid_balance: 0,
        unified_billing_user: true,
        subscription_tier: 'SuperGrok',
        updated_at: '2026-07-12T05:30:00Z'
      },
      status_code: 200,
      reset_supported: false,
      fetched_at: 1783834200
    })

    const wrapper = mount(GrokQuotaProbeCell, { props: { account } })

    expect(wrapper.findAll('button')).toHaveLength(1)
    expect(wrapper.text()).toContain('admin.accounts.usageWindow.grokProbe')
    expect(wrapper.text()).not.toContain('admin.accounts.usageWindow.grokResetUnsupported')

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(queryQuota).toHaveBeenCalledWith(3863)
    expect(wrapper.emitted('updated')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('admin.accounts.usageWindow.grokWeeklySummary')
    expect(wrapper.text()).not.toContain('admin.accounts.usageWindow.grokOnDemandDisabled')
  })
})
