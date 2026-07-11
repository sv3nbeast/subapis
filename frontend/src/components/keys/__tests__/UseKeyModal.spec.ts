import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn().mockResolvedValue(true)
  })
}))

import UseKeyModal from '../UseKeyModal.vue'

describe('UseKeyModal', () => {
  it('renders GPT-5.5 and goals feature in OpenAI Codex config', () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    const configToml = codeBlocks.find((content) => content.includes('model_provider = "OpenAI"'))

    expect(configToml).toBeDefined()
    expect(configToml).toContain('model = "gpt-5.5"')
    expect(configToml).toContain('review_model = "gpt-5.5"')
    expect(configToml).not.toContain('model = "gpt-5.4"')
    expect(configToml).not.toContain('model_context_window')
    expect(configToml).not.toContain('model_auto_compact_token_limit')
    expect(configToml).toContain('[features]\ngoals = true')
  })

  it('renders GPT-5.5 and goals feature in OpenAI Codex WebSocket config', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const wsTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.codexCliWs')
    )

    expect(wsTab).toBeDefined()
    await wsTab!.trigger('click')
    await nextTick()

    const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    const configToml = codeBlocks.find((content) => content.includes('supports_websockets = true'))

    expect(configToml).toBeDefined()
    expect(configToml).toContain('model = "gpt-5.5"')
    expect(configToml).toContain('review_model = "gpt-5.5"')
    expect(configToml).not.toContain('model = "gpt-5.4"')
    expect(configToml).not.toContain('model_context_window')
    expect(configToml).not.toContain('model_auto_compact_token_limit')
    expect(configToml).toContain('[features]\ngoals = true')
    expect(configToml).not.toContain('responses_websockets_v2 = true')
  })

  it('renders GPT-5.4 mini entry in OpenCode config', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const opencodeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencode')
    )

    expect(opencodeTab).toBeDefined()
    await opencodeTab!.trigger('click')
    await nextTick()

    const codeBlock = wrapper.find('pre code')
    expect(codeBlock.exists()).toBe(true)
    expect(codeBlock.text()).toContain('"name": "GPT-5.4 Mini"')
    expect(codeBlock.text()).not.toContain('"name": "GPT-5.4 Nano"')
  })

  it('includes Claude Code default model in anthropic settings config', () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'anthropic'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    const terminalConfig = codeBlocks.find((content) => content.includes('export ANTHROPIC_BASE_URL='))
    const settingsConfig = codeBlocks.find((content) => content.includes('"ANTHROPIC_BASE_URL"'))

    expect(terminalConfig).toBeDefined()
    expect(terminalConfig).toContain('CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1')
    expect(terminalConfig).not.toContain('CLAUDE_CODE_EFFORT_LEVEL')
    expect(terminalConfig).toContain('CLAUDE_CODE_ATTRIBUTION_HEADER=0')

    expect(settingsConfig).toBeDefined()
    expect(settingsConfig).toContain('//, "model": "claude-opus-4-7" // 修改这里的模型名可指定使用模型，默认claude-opus-4-7')
    expect(settingsConfig).not.toContain('CLAUDE_CODE_EFFORT_LEVEL')
  })

  it('renders centrally configured client defaults', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai',
        usageConfig: {
          codex_model: 'gpt-custom',
          codex_review_model: 'gpt-review',
          codex_reasoning_effort: 'medium',
          codex_disable_response_storage: false,
          codex_network_access: 'disabled',
          codex_goals_enabled: false,
          codex_include_legacy_ws_feature: true,
          codex_extra_config: 'service_tier = "fast"'
        }
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    let configToml = wrapper.findAll('pre code')
      .map((code) => code.text())
      .find((content) => content.includes('model_provider = "OpenAI"'))

    expect(configToml).toContain('model = "gpt-custom"')
    expect(configToml).toContain('review_model = "gpt-review"')
    expect(configToml).toContain('model_reasoning_effort = "medium"')
    expect(configToml).toContain('disable_response_storage = false')
    expect(configToml).toContain('network_access = "disabled"')
    expect(configToml).toContain('service_tier = "fast"')
    expect(configToml).not.toContain('[features]')

    const wsTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.codexCliWs')
    )
    await wsTab!.trigger('click')
    await nextTick()
    configToml = wrapper.findAll('pre code')
      .map((code) => code.text())
      .find((content) => content.includes('supports_websockets = true'))
    expect(configToml).toContain('[features]\nresponses_websockets_v2 = true')
  })

  it('renders centrally configured Claude Code and Gemini CLI defaults', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'anthropic',
        usageConfig: {
          claude_code_default_model: 'claude-custom',
          claude_code_disable_nonessential_traffic: false,
          claude_code_attribution_header: 1,
          gemini_cli_default_model: 'gemini-custom'
        }
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    let codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    expect(codeBlocks.join('\n')).not.toContain('CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC')
    expect(codeBlocks.join('\n')).toContain('CLAUDE_CODE_ATTRIBUTION_HEADER=1')
    expect(codeBlocks.join('\n')).toContain('"model": "claude-custom"')

    await wrapper.setProps({ platform: 'gemini' })
    await nextTick()
    codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    expect(codeBlocks.join('\n')).toContain('GEMINI_MODEL="gemini-custom"')
  })

  it('hides the Codex WebSocket tab when centrally disabled', () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai',
        usageConfig: {
          codex_websocket_enabled: false
        }
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    expect(wrapper.text()).not.toContain('keys.useKeyModal.cliTabs.codexCliWs')
  })

  it('renders Claude Fable 5 OpenCode config with adaptive thinking', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'antigravity'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const opencodeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencode')
    )

    expect(opencodeTab).toBeDefined()
    await opencodeTab!.trigger('click')
    await nextTick()

    const claudeConfig = wrapper.findAll('pre code')
      .map((code) => code.text())
      .find((content) => content.includes('"antigravity-claude"'))

    expect(claudeConfig).toBeDefined()
    const parsed = JSON.parse(claudeConfig!)
    const fable = parsed.provider['antigravity-claude'].models['claude-fable-5']

    expect(fable.name).toBe('Claude Fable 5')
    expect(fable.limit).toEqual({ context: 1048576, output: 128000 })
    expect(fable.options.thinking).toEqual({ type: 'adaptive' })
    expect(fable.options.thinking).not.toHaveProperty('budgetTokens')
  })

  const customTemplateProfile = (overrides: Record<string, unknown> = {}) => ({
    id: 'grok-tools',
    name: 'Grok tools',
    enabled: true,
    priority: 100,
    mode: 'append' as const,
    match: {
      platforms: ['grok'],
      group_ids: [],
      claude_code_only: 'any' as const
    },
    templates: [{
      id: 'grok-curl',
      label: 'Grok cURL',
      description: 'Grok compatible endpoint',
      note: 'Group {{group_name}}',
      kind: 'generic',
      enabled: true,
      sort_order: 1,
      variants: [{
        id: 'default',
        label: 'Default',
        files: [{
          path: '{{platform}}-{{group_id}}.sh',
          content: 'curl {{base_url_v1}}/chat/completions -H "Authorization: Bearer {{api_key}}" # {{group_name}} {{codex_model}}'
        }]
      }]
    }],
    ...overrides
  })

  it('matches an arbitrary Grok platform and renders controlled placeholders', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-grok',
        baseUrl: 'https://gateway.example.com',
        platform: 'grok',
        groupId: 42,
        groupName: 'Grok public',
        usageConfig: { template_profiles: [customTemplateProfile()] }
      },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
          Icon: { template: '<span />' }
        }
      }
    })

    const tab = wrapper.findAll('button').find((button) => button.text().includes('Grok cURL'))
    expect(tab).toBeDefined()
    await tab!.trigger('click')
    await nextTick()

    expect(wrapper.find('.font-mono').text()).toContain('grok-42.sh')
    expect(wrapper.find('pre code').text()).toContain('https://gateway.example.com/v1/chat/completions')
    expect(wrapper.find('pre code').text()).toContain('Bearer sk-grok')
    expect(wrapper.find('pre code').text()).toContain('Grok public gpt-5.5')
  })

  it('only applies a group-ID profile to the selected group', () => {
    const profile = customTemplateProfile({
      match: { platforms: [], group_ids: [99], claude_code_only: 'any' }
    })
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'grok',
        groupId: 42,
        usageConfig: { template_profiles: [profile] }
      },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Icon: { template: '<span />' } } }
    })

    expect(wrapper.text()).not.toContain('Grok cURL')
    expect(wrapper.text()).toContain('keys.useKeyModal.cliTabs.claudeCode')
  })

  it('replace mode removes built-in templates while append mode preserves them', () => {
    const replaceWrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'grok',
        usageConfig: { template_profiles: [customTemplateProfile({ mode: 'replace' })] }
      },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Icon: { template: '<span />' } } }
    })
    expect(replaceWrapper.text()).toContain('Grok cURL')
    expect(replaceWrapper.text()).not.toContain('keys.useKeyModal.cliTabs.claudeCode')
    expect(replaceWrapper.text()).not.toContain('keys.useKeyModal.cliTabs.opencode')

    const appendWrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'grok',
        usageConfig: { template_profiles: [customTemplateProfile()] }
      },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Icon: { template: '<span />' } } }
    })
    expect(appendWrapper.text()).toContain('Grok cURL')
    expect(appendWrapper.text()).toContain('keys.useKeyModal.cliTabs.claudeCode')
    expect(appendWrapper.text()).toContain('keys.useKeyModal.cliTabs.opencode')
  })

  it('enforces claude_code_only after all custom profile rules', () => {
    const claudeProfile = customTemplateProfile({
      match: { platforms: ['openai'], group_ids: [], claude_code_only: 'any' },
      templates: [
        customTemplateProfile().templates[0],
        {
          id: 'custom-claude',
          label: 'Custom Claude',
          kind: 'claude_code',
          enabled: true,
          sort_order: 2,
          variants: [{ id: 'default', label: 'Default', files: [{ path: 'Terminal', content: 'claude {{api_key}}' }] }]
        }
      ]
    })
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'openai',
        allowMessagesDispatch: true,
        claudeCodeOnly: true,
        usageConfig: { template_profiles: [claudeProfile] }
      },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Icon: { template: '<span />' } } }
    })

    expect(wrapper.text()).toContain('Custom Claude')
    expect(wrapper.text()).toContain('keys.useKeyModal.cliTabs.claudeCode')
    expect(wrapper.text()).not.toContain('Grok cURL')
    expect(wrapper.text()).not.toContain('keys.useKeyModal.cliTabs.codexCli')
    expect(wrapper.text()).not.toContain('keys.useKeyModal.cliTabs.opencode')
  })
})
