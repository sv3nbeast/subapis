import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import type { UserDashboardStats as UserStatsType } from '@/api/usage'
import UserDashboardStats from '../UserDashboardStats.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const createStats = (): UserStatsType => ({
  total_api_keys: 1,
  active_api_keys: 1,
  total_requests: 2,
  total_input_tokens: 100,
  total_output_tokens: 200,
  total_cache_creation_tokens: 300,
  total_cache_read_tokens: 400,
  total_tokens: 1000,
  total_cost: 1,
  total_actual_cost: 0.5,
  today_requests: 1,
  today_input_tokens: 10,
  today_output_tokens: 20,
  today_cache_creation_tokens: 30,
  today_cache_read_tokens: 40,
  today_tokens: 100,
  today_cost: 0.1,
  today_actual_cost: 0.05,
  average_duration_ms: 123,
  rpm: 1,
  tpm: 2,
  by_platform: []
})

describe('UserDashboardStats', () => {
  const mountStats = (mode: 'all' | 'primary' | 'secondary' = 'all') => mount(UserDashboardStats, {
    props: {
      stats: createStats(),
      balance: 0,
      isSimple: false,
      platformQuotas: [],
      mode,
    },
    global: {
      stubs: {
        Icon: true,
      },
    },
  })

  it('shows cache read and write token breakdown on token cards', () => {
    const wrapper = mountStats()

    const text = wrapper.text()
    expect(text).toContain('dashboard.cache: 70')
    expect(text).toContain('usage.inputCacheReadRatio: 50.0%')
    expect(text).toContain('usage.cacheRead: 40')
    expect(text).toContain('usage.cacheWrite: 30')
    expect(text).toContain('dashboard.cache: 700')
    expect(text).toContain('usage.inputCacheReadRatio: 50.0%')
    expect(text).toContain('usage.cacheRead: 400')
    expect(text).toContain('usage.cacheWrite: 300')
  })

  it('renders only the four approved first-screen metrics in primary mode', () => {
    const wrapper = mountStats('primary')

    expect(wrapper.findAll('.dashboard-metric-card')).toHaveLength(4)
    expect(wrapper.find('.dashboard-metric-balance').exists()).toBe(true)
    expect(wrapper.find('.dashboard-metric-requests').exists()).toBe(true)
    expect(wrapper.find('.dashboard-metric-today-tokens').exists()).toBe(true)
    expect(wrapper.find('.dashboard-metric-response').exists()).toBe(true)
    expect(wrapper.find('.dashboard-metric-keys').exists()).toBe(false)
    expect(wrapper.find('.dashboard-metric-cost').exists()).toBe(false)
  })

  it('keeps the remaining original metrics available in secondary mode', () => {
    const wrapper = mountStats('secondary')

    expect(wrapper.findAll('.dashboard-metric-card')).toHaveLength(5)
    expect(wrapper.find('.dashboard-metric-keys').exists()).toBe(true)
    expect(wrapper.find('.dashboard-metric-cost').exists()).toBe(true)
    expect(wrapper.find('.dashboard-metric-total-tokens').exists()).toBe(true)
    expect(wrapper.find('.dashboard-metric-performance').exists()).toBe(true)
    expect(wrapper.find('.dashboard-metric-today-tokens').exists()).toBe(true)
    expect(wrapper.find('.dashboard-metric-balance').exists()).toBe(false)
    expect(wrapper.find('.dashboard-metric-response').exists()).toBe(false)
  })
})
