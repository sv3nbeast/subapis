import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountTodayStatsCell from '../AccountTodayStatsCell.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        const messages: Record<string, string> = {
          'admin.accounts.stats.requests': 'Requests',
          'admin.accounts.stats.tokens': 'Tokens',
          'admin.accounts.stats.kiroCredits': 'Credits Used',
          'admin.accounts.stats.approxCost': `approx. ${params?.amount ?? ''}`,
          'usage.accountBilled': 'Account billed',
          'usage.userBilled': 'User billed'
        }
        return messages[key] ?? key
      }
    })
  }
})

vi.mock('@/i18n', () => ({
  i18n: {
    global: {
      t: (key: string, params?: Record<string, unknown>) => key + JSON.stringify(params ?? {})
    }
  },
  getLocale: () => 'en-US'
}))

const stats = {
  requests: 2,
  tokens: 140600,
  cost: 0.77,
  user_cost: 0.7,
  kiro_credits: 0.17
}

describe('AccountTodayStatsCell', () => {
  it('shows Kiro credits with estimated cost when unit price is configured', () => {
    const wrapper = mount(AccountTodayStatsCell, {
      props: {
        stats,
        platform: 'kiro',
        kiroCreditUnitPriceUsd: 0.071
      }
    })

    expect(wrapper.text()).toContain('Credits Used:')
    expect(wrapper.text()).toContain('0.17')
    expect(wrapper.text()).toContain('approx. $0.01')
  })

  it('highlights credits when estimated credit cost exceeds user cost', () => {
    const wrapper = mount(AccountTodayStatsCell, {
      props: {
        stats: {
          ...stats,
          user_cost: 0.5,
          kiro_credits: 10
        },
        platform: 'kiro',
        kiroCreditUnitPriceUsd: 0.1
      }
    })

    const row = wrapper.get('[data-testid="kiro-credits-row"]')
    expect(row.classes()).toContain('text-red-600')
    expect(row.classes()).toContain('dark:text-red-400')
    expect(wrapper.text()).toContain('approx. $1.00')
  })

  it('does not highlight credits when user cost is unavailable', () => {
    const wrapper = mount(AccountTodayStatsCell, {
      props: {
        stats: {
          requests: 2,
          tokens: 140600,
          cost: 0.77,
          kiro_credits: 10
        },
        platform: 'kiro',
        kiroCreditUnitPriceUsd: 0.1
      }
    })

    expect(wrapper.get('[data-testid="kiro-credits-row"]').classes()).not.toContain('text-red-600')
    expect(wrapper.text()).toContain('approx. $1.00')
  })

  it('shows Kiro credits without estimated cost when unit price is zero', () => {
    const wrapper = mount(AccountTodayStatsCell, {
      props: {
        stats,
        platform: 'kiro',
        kiroCreditUnitPriceUsd: 0
      }
    })

    expect(wrapper.text()).toContain('Credits Used:')
    expect(wrapper.text()).toContain('0.17')
    expect(wrapper.text()).not.toContain('approx. $')
  })

  it('does not show credits for relay or non-Kiro accounts', () => {
    const relayWrapper = mount(AccountTodayStatsCell, {
      props: {
        stats,
        platform: 'kiro',
        isRelay: true,
        kiroCreditUnitPriceUsd: 0.071
      }
    })
    const openAIWrapper = mount(AccountTodayStatsCell, {
      props: {
        stats,
        platform: 'openai',
        kiroCreditUnitPriceUsd: 0.071
      }
    })

    expect(relayWrapper.text()).not.toContain('Credits Used')
    expect(openAIWrapper.text()).not.toContain('Credits Used')
  })
})
