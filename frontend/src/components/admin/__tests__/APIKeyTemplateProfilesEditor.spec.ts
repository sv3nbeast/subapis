import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import APIKeyTemplateProfilesEditor from '../APIKeyTemplateProfilesEditor.vue'

const profile = () => ({
  id: 'grok-profile',
  name: 'Grok profile',
  enabled: true,
  priority: 100,
  mode: 'append' as const,
  match: {
    platforms: ['grok'],
    group_ids: [] as number[],
    claude_code_only: 'any' as const
  },
  templates: []
})

describe('APIKeyTemplateProfilesEditor', () => {
  it('creates a profile without requiring a frontend platform enum', async () => {
    const wrapper = mount(APIKeyTemplateProfilesEditor, {
      props: { modelValue: [], groups: [] },
      global: { stubs: { Toggle: { template: '<button />' } } }
    })

    await wrapper.find('button').trigger('click')
    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as ReturnType<typeof profile>[]

    expect(emitted).toHaveLength(1)
    expect(emitted[0].match.platforms).toEqual([])
  })

  it('accepts arbitrary comma-separated platform identifiers', async () => {
    const wrapper = mount(APIKeyTemplateProfilesEditor, {
      props: { modelValue: [profile()], groups: [] },
      global: { stubs: { Toggle: { template: '<button />' } } }
    })
    const platformInput = wrapper.find('input[placeholder]')
    await platformInput.setValue(' Grok, future-provider, grok ')

    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as ReturnType<typeof profile>[]
    expect(emitted[0].match.platforms).toEqual(['grok', 'future-provider'])
  })

  it('reports invalid template JSON and does not overwrite the last valid templates', async () => {
    const wrapper = mount(APIKeyTemplateProfilesEditor, {
      props: { modelValue: [profile()], groups: [] },
      global: { stubs: { Toggle: { template: '<button />' } } }
    })
    const textarea = wrapper.find('textarea')
    await textarea.setValue('[invalid')
    await nextTick()

    expect(wrapper.emitted('validity')?.at(-1)?.[0]).toBe(false)
    expect(wrapper.text()).toContain('Unexpected')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('adds a Claude Code preset with the security-relevant kind', async () => {
    const wrapper = mount(APIKeyTemplateProfilesEditor, {
      props: { modelValue: [profile()], groups: [] },
      global: { stubs: { Toggle: { template: '<button />' } } }
    })
    const claudeButton = wrapper.findAll('button').find((button) => button.text() === 'Claude Code')
    expect(claudeButton).toBeDefined()
    await claudeButton!.trigger('click')

    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as ReturnType<typeof profile>[]
    expect(emitted[0].templates[0].kind).toBe('claude_code')
    expect(emitted[0].templates[0].variants).toHaveLength(2)
  })

  it('selects specific group IDs including Claude Code-only groups', async () => {
    const wrapper = mount(APIKeyTemplateProfilesEditor, {
      props: {
        modelValue: [profile()],
        groups: [{ id: 7, name: 'Restricted Grok', platform: 'grok', claude_code_only: true } as never]
      },
      global: { stubs: { Toggle: { template: '<button />' } } }
    })
    const select = wrapper.find('select[multiple]')
    await select.setValue(['7'])

    const emitted = wrapper.emitted('update:modelValue')?.at(-1)?.[0] as ReturnType<typeof profile>[]
    expect(emitted[0].match.group_ids).toEqual([7])
    expect(wrapper.text()).toContain('Claude Code only')
  })
})
