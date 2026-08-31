import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DocsGuideView from '@/views/public/DocsGuideView.vue'

const { authStore, appStore, copyToClipboard } = vi.hoisted(() => ({
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
    checkAuth: vi.fn(),
  },
  appStore: {
    siteName: 'SubAPIs',
    siteLogo: '',
    apiBaseUrl: 'https://fallback.example.com',
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
    cachedPublicSettings: {
      site_name: 'SubAPIs',
      site_logo: '',
      api_base_url: 'https://gateway.example.com/',
      api_key_usage_config: {
        codex_model: 'gpt-current',
        codex_review_model: 'gpt-review',
        codex_reasoning_effort: 'high',
        codex_disable_response_storage: true,
        codex_network_access: 'enabled',
        codex_goals_enabled: true,
        codex_websocket_enabled: true,
        claude_code_attribution_header: 0,
      },
    },
  },
  copyToClipboard: vi.fn(),
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => authStore,
  useAppStore: () => appStore,
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      tm: () => ['first instruction', 'second instruction'],
    }),
  }
})

const RouterLinkStub = {
  props: ['to'],
  template: '<a :data-to="typeof to === \'string\' ? to : to.path"><slot /></a>',
}

function mountView() {
  return mount(DocsGuideView, {
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        PublicLayout: { template: '<div><slot /></div>' },
        LocaleSwitcher: true,
        Icon: true,
      },
    },
  })
}

describe('DocsGuideView', () => {
  beforeEach(() => {
    authStore.checkAuth.mockReset()
    appStore.fetchPublicSettings.mockReset()
    copyToClipboard.mockReset()
  })

  it('uses current public settings for endpoint and client configuration examples', async () => {
    const wrapper = mountView()
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('https://gateway.example.com/v1/messages')
    expect(text).toContain('https://gateway.example.com/v1/responses')
    expect(text).toContain('ANTHROPIC_AUTH_TOKEN')
    expect(text).not.toContain('ANTHROPIC_API_KEY')
    expect(text).toContain('ANTHROPIC_BASE_URL')
    expect(text).toContain('"https://gateway.example.com"')
    expect(text).toContain('model = "gpt-current"')
    expect(text).toContain('review_model = "gpt-review"')
    expect(text).toContain('wire_api = "responses"')
    expect(text).toContain('supports_websockets = true')
    expect(text).not.toContain('/v1beta')
    expect(text).not.toContain('/antigravity')
  })

  it('switches configuration file paths between Unix and Windows', async () => {
    const wrapper = mountView()

    expect(wrapper.text()).toContain('~/.claude/settings.json')
    expect(wrapper.text()).toContain('~/.codex/config.toml')

    await wrapper.get('[role="tablist"] button:last-child').trigger('click')

    expect(wrapper.text()).toContain('%USERPROFILE%\\.claude\\settings.json')
    expect(wrapper.text()).toContain('%USERPROFILE%\\.codex\\config.toml')
    expect(wrapper.text()).toContain('curl.exe')
  })
})
