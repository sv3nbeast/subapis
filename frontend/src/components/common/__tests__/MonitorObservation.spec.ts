import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import MonitorObservation from '../MonitorObservation.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

afterEach(() => vi.useRealTimers())

describe('MonitorObservation', () => {
  it('shows a slow successful probe and real probe cadence instead of account health', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-08T06:01:00Z'))
    const wrapper = mount(MonitorObservation, { props: { item: {
      primary_status: 'degraded', primary_checked_at: '2026-09-08T06:00:00Z',
      interval_seconds: 3600, probe_path: '/v1/chat/completions',
    } } })
    expect(wrapper.text()).toContain('monitorCommon.observation.slow')
    expect(wrapper.text()).toContain('monitorCommon.observation.interval')
    expect(wrapper.text()).toContain('/v1/chat/completions')
    expect(wrapper.text()).toContain('monitorCommon.observation.scope')
    wrapper.unmount()
  })

  it('expires automatically even if the page stops fetching data', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-08T06:02:50Z'))
    const wrapper = mount(MonitorObservation, { props: { item: {
      primary_status: 'operational', primary_checked_at: '2026-09-08T06:00:00Z',
      interval_seconds: 60,
    } } })
    expect(wrapper.text()).toContain('monitorCommon.observation.passed')
    await vi.advanceTimersByTimeAsync(30000)
    expect(wrapper.text()).toContain('monitorCommon.observation.stale')
    expect(wrapper.text()).not.toContain('monitorCommon.observation.passed')
    wrapper.unmount()
  })
})
