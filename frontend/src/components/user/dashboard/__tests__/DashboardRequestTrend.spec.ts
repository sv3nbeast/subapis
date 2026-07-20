import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { TrendDataPoint } from '@/types'
import DashboardRequestTrend from '../DashboardRequestTrend.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: { count?: number }) => params?.count ? `${params.count} days` : key,
    }),
  }
})

const trend: TrendDataPoint[] = [
  {
    date: '2026-07-18',
    requests: 1200,
    input_tokens: 10,
    output_tokens: 20,
    cache_creation_tokens: 0,
    cache_read_tokens: 0,
    total_tokens: 30,
    cost: 0.1,
    actual_cost: 0.08,
  },
  {
    date: '2026-07-19',
    requests: 2500,
    input_tokens: 30,
    output_tokens: 40,
    cache_creation_tokens: 0,
    cache_read_tokens: 0,
    total_tokens: 70,
    cost: 0.2,
    actual_cost: 0.16,
  },
]

describe('DashboardRequestTrend', () => {
  it('uses request counts and emits the selected prototype period', async () => {
    const wrapper = mount(DashboardRequestTrend, {
      props: { trend, loading: false, period: 30 },
    })

    expect(wrapper.text()).toContain('3.7K')
    expect(wrapper.find('svg.dashboard-request-svg').exists()).toBe(true)
    expect(wrapper.find('polyline.dashboard-request-line').attributes('points')).not.toBe('')
    expect(wrapper.find('path.dashboard-request-area').attributes('d')).toContain('Z')

    const periodButton = wrapper.findAll('button').find((button) => button.text() === '90 days')
    expect(periodButton).toBeTruthy()
    await periodButton!.trigger('click')

    expect(wrapper.emitted('periodChange')).toEqual([[90]])
  })
})
