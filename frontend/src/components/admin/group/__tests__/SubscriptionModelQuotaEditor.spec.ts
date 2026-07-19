import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import SubscriptionModelQuotaEditor from '../SubscriptionModelQuotaEditor.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('SubscriptionModelQuotaEditor', () => {
  it('converts percentages to API ratios', async () => {
    const wrapper = mount(SubscriptionModelQuotaEditor, {
      props: { modelValue: {} },
      global: { stubs: { Icon: true } },
    })

    await wrapper.find('button').trigger('click')
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('claude-fable-5')
    await inputs[1].setValue('50')

    const updates = wrapper.emitted('update:modelValue') || []
    expect(updates.at(-1)?.[0]).toEqual({ 'claude-fable-5': 0.5 })
  })

  it('hydrates existing ratios as percentages', () => {
    const wrapper = mount(SubscriptionModelQuotaEditor, {
      props: { modelValue: { 'claude-fable-5': 0.5 } },
      global: { stubs: { Icon: true } },
    })

    const inputs = wrapper.findAll<HTMLInputElement>('input')
    expect(inputs[0].element.value).toBe('claude-fable-5')
    expect(inputs[1].element.value).toBe('50')
  })
})
