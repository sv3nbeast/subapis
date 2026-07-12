import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import MonitorAvailabilityMeter from '../MonitorAvailabilityMeter.vue'

const messages: Record<string, string> = {
  'monitorCommon.availabilityPrefix': '可用性',
  'monitorCommon.availability.level.excellent': '极佳',
  'monitorCommon.availability.level.noData': '暂无数据',
  'monitorCommon.availability.description.excellent': '服务质量极佳',
  'monitorCommon.availability.noSamples': '暂无可用样本',
  'monitorCommon.availability.target': '目标 {n}%',
  'monitorCommon.availability.ariaLabel': '{label}：{value}，{level}',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params: Record<string, string | number> = {}) => {
      let value = messages[key] ?? key
      for (const [name, replacement] of Object.entries(params)) {
        value = value.replace(`{${name}}`, String(replacement))
      }
      return value
    },
  }),
}))

function mountMeter(props: Record<string, unknown>) {
  return mount(MonitorAvailabilityMeter, {
    props: props as never,
  })
}

describe('MonitorAvailabilityMeter', () => {
  it('renders a labeled health meter with an accurate progress value', () => {
    const wrapper = mountMeter({
      value: 99.9,
      label: '可用性 · 7 天',
      note: '+ 3 模型',
    })

    expect(wrapper.text()).toContain('99.90')
    expect(wrapper.findAll('span').some((span) => span.text() === '%')).toBe(true)
    expect(wrapper.text()).toContain('极佳')
    expect(wrapper.text()).toContain('服务质量极佳')
    expect(wrapper.text()).toContain('目标 99%')
    expect(wrapper.text()).toContain('+ 3 模型')

    const progress = wrapper.get('[role="progressbar"]')
    expect(progress.attributes('aria-valuenow')).toBe('99.9')
    expect(progress.get('div').attributes('style')).toContain('width: 99.9%')
  })

  it('renders missing compact values without a dangling percent sign', () => {
    const wrapper = mountMeter({ value: null, compact: true })

    expect(wrapper.text()).toBe('--')
    expect(wrapper.text()).not.toContain('%')
    expect(wrapper.find('[role="progressbar"]').exists()).toBe(false)
  })
})
