import { expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('file-saver', () => ({ saveAs: vi.fn() }))
import UseKeyModal from '../UseKeyModal.vue'

it('generates Astra OpenCode limits and only its supported efforts', async () => {
  const wrapper = mount(UseKeyModal, {
    props: { show: true, apiKey: 'test-key', baseUrl: 'https://example.com/v1', platform: 'openai' },
    global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Icon: true } }
  })
  const tab = wrapper.findAll('button').find(b => b.text().includes('keys.useKeyModal.cliTabs.opencode'))
  expect(tab).toBeDefined()
  await tab!.trigger('click')
  await nextTick()
  const models = JSON.parse(wrapper.find('pre code').text()).provider.openai.models
  expect(models['gpt-6-astra'].name).toBe('GPT-6 Astra')
  expect(models['gpt-6-astra'].limit).toEqual({ context: 1050000, output: 128000 })
  expect(Object.keys(models['gpt-6-astra'].variants)).toEqual(['low', 'medium', 'high', 'xhigh', 'max'])
  expect(models['gpt-5.6-sol'].variants.max).toBeDefined()
  wrapper.unmount()
})
