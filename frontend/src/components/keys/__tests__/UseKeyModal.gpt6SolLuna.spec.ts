import { expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('file-saver', () => ({ saveAs: vi.fn() }))
import UseKeyModal from '../UseKeyModal.vue'

it('generates GPT-6 Sol/Luna OpenCode limits and their supported efforts', async () => {
  const wrapper = mount(UseKeyModal, {
    props: { show: true, apiKey: 'test-key', baseUrl: 'https://example.com/v1', platform: 'openai' },
    global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Icon: true } }
  })
  const tab = wrapper.findAll('button').find(b => b.text().includes('keys.useKeyModal.cliTabs.opencode'))
  expect(tab).toBeDefined()
  await tab!.trigger('click')
  await nextTick()

  const models = JSON.parse(wrapper.find('pre code').text()).provider.openai.models
  // Official model cards (2026-09-23): 1,050,000 context window, 128,000 max output.
  expect(models['gpt-6-sol'].name).toBe('GPT-6 Sol')
  expect(models['gpt-6-sol'].limit).toEqual({ context: 1050000, output: 128000 })
  expect(Object.keys(models['gpt-6-sol'].variants)).toEqual(['low', 'medium', 'high', 'xhigh', 'max'])
  expect(models['gpt-6-luna'].name).toBe('GPT-6 Luna')
  expect(models['gpt-6-luna'].limit).toEqual({ context: 1050000, output: 128000 })
  expect(Object.keys(models['gpt-6-luna'].variants)).toEqual(['low', 'medium', 'high', 'xhigh', 'max'])
  // Kiro rejects every gpt-6-* id, so the released 5.6 family must stay intact.
  expect(models['gpt-5.6-sol'].variants.max).toBeDefined()
  wrapper.unmount()
})
