import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import APIKeyGroupTemplatesEditor from '../APIKeyGroupTemplatesEditor.vue'

const ToggleStub = {
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: '<button data-toggle @click="$emit(\'update:modelValue\', !modelValue)" />'
}

const groups = [
  { id: 7, name: 'Grok', platform: 'grok' },
  { id: 8, name: 'Claude AWS', platform: 'anthropic' }
] as never[]

const template = () => ({
  id: 'opencode',
  label: 'OpenCode',
  kind: 'opencode',
  enabled: true,
  sort_order: 10,
  variants: [{
    id: 'default',
    label: 'Configuration',
    files: [{ path: 'opencode.json', content: '{"provider":{}}' }]
  }]
})

describe('APIKeyGroupTemplatesEditor', () => {
  it('creates an exact-group OpenCode template with controlled placeholders', async () => {
    const wrapper = mount(APIKeyGroupTemplatesEditor, {
      props: { modelValue: [], groups },
      global: { stubs: { Toggle: ToggleStub } }
    })

    await wrapper.find('select').setValue('7')
    const addButton = wrapper.findAll('button').find((button) => (
      button.text().includes('groupTemplates.addGroup')
    ))
    expect(addButton).toBeDefined()
    await addButton!.trigger('click')

    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as Array<{
      group_id: number
      enabled: boolean
      templates: ReturnType<typeof template>[]
    }>
    expect(emitted).toHaveLength(1)
    expect(emitted[0].group_id).toBe(7)
    expect(emitted[0].enabled).toBe(true)
    expect(emitted[0].templates[0].id).toBe('opencode')
    expect(emitted[0].templates[0].variants[0].files[0].content).toContain('@ai-sdk/openai-compatible')
    expect(emitted[0].templates[0].variants[0].files[0].content).toContain('{{base_url_v1}}')
    expect(emitted[0].templates[0].variants[0].files[0].content).toContain('{{api_key}}')
    expect(emitted[0].templates[0].variants[0].files[0].content).toContain('grok-4.5')
  })

  it('uses a template toggle to choose whether the client is displayed', async () => {
    const wrapper = mount(APIKeyGroupTemplatesEditor, {
      props: {
        modelValue: [{ group_id: 7, enabled: true, templates: [template()] }],
        groups
      },
      global: { stubs: { Toggle: ToggleStub } }
    })

    const toggles = wrapper.findAll('[data-toggle]')
    expect(toggles).toHaveLength(2)
    await toggles[1].trigger('click')

    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as Array<{
      templates: ReturnType<typeof template>[]
    }>
    expect(emitted[0].templates[0].enabled).toBe(false)
  })

  it('creates an AWS-compatible Anthropic provider for an Anthropic group', async () => {
    const wrapper = mount(APIKeyGroupTemplatesEditor, {
      props: { modelValue: [], groups },
      global: { stubs: { Toggle: ToggleStub } }
    })

    await wrapper.find('select').setValue('8')
    await wrapper.findAll('button').find((button) => (
      button.text().includes('groupTemplates.addGroup')
    ))!.trigger('click')

    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as Array<{
      templates: ReturnType<typeof template>[]
    }>
    const content = JSON.parse(emitted[0].templates[0].variants[0].files[0].content)
    expect(content.provider.anthropic.npm).toBe('@ai-sdk/anthropic')
    expect(content.provider.anthropic.options.baseURL).toBe('{{base_url_v1}}')
    expect(content.provider.anthropic.models['claude-opus-4-8'].limit.context).toBe(200000)
    expect(content.provider.anthropic.models['claude-sonnet-5'].limit.context).toBe(1000000)
  })

  it('updates valid hardcoded JSON and blocks invalid drafts', async () => {
    const wrapper = mount(APIKeyGroupTemplatesEditor, {
      props: {
        modelValue: [{ group_id: 8, enabled: true, templates: [template()] }],
        groups
      },
      global: { stubs: { Toggle: ToggleStub } }
    })

    const textarea = wrapper.get('textarea')
    await textarea.setValue(JSON.stringify({ ...template(), label: 'AWS OpenCode' }))
    await nextTick()

    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as Array<{
      templates: ReturnType<typeof template>[]
    }>
    expect(emitted[0].templates[0].label).toBe('AWS OpenCode')

    await textarea.setValue('{invalid')
    await nextTick()
    expect(wrapper.emitted('validity')?.at(-1)?.[0]).toBe(false)
    expect(wrapper.text()).toMatch(/Expected|Unexpected/)
  })
})
