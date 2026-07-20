import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { ModelStatus } from '@/api/status'
import ServiceStatusBar from '../ServiceStatusBar.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

describe('ServiceStatusBar', () => {
  it('ignores malformed probe timestamps instead of breaking the status view', () => {
    const modelStatus: ModelStatus = {
      model: 'claude-sonnet-4',
      display_name: 'Claude',
      current_status: 'operational',
      uptime_percentage: 99.9,
      hourly_stats: [{
        hour: 'not-a-date',
        success: 1,
        total: 1,
        avg_latency_ms: 800,
      }],
    }

    const wrapper = mount(ServiceStatusBar, {
      props: { modelStatus, intervalMinutes: 60 },
    })

    expect(wrapper.text()).toContain('Claude')
    expect(wrapper.findAll('.flex-1')).toHaveLength(24)
  })
})
