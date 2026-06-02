import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import UserReAuthAccountModal from '../ReAuthAccountModal.vue'
import AdminReAuthAccountModal from '@/components/admin/account/ReAuthAccountModal.vue'

const {
  applyOAuthCredentialsMock,
  clearErrorMock,
  exchangeKiroAuthCodeMock,
  buildKiroCredentialsMock,
  resetKiroStateMock
} = vi.hoisted(() => ({
  applyOAuthCredentialsMock: vi.fn(),
  clearErrorMock: vi.fn(),
  exchangeKiroAuthCodeMock: vi.fn(),
  buildKiroCredentialsMock: vi.fn(),
  resetKiroStateMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      update: vi.fn(),
      clearError: clearErrorMock,
      applyOAuthCredentials: applyOAuthCredentialsMock,
      exchangeCode: vi.fn()
    }
  }
}))

vi.mock('@/composables/useAccountOAuth', async () => {
  const { ref } = await vi.importActual<typeof import('vue')>('vue')
  return {
    useAccountOAuth: () => ({
      authUrl: ref(''),
      sessionId: ref(''),
      loading: ref(false),
      error: ref(''),
      resetState: vi.fn(),
      generateAuthUrl: vi.fn(),
      buildExtraInfo: vi.fn(() => ({}))
    })
  }
})

vi.mock('@/composables/useOpenAIOAuth', async () => {
  const { ref } = await vi.importActual<typeof import('vue')>('vue')
  return {
    useOpenAIOAuth: () => ({
      authUrl: ref(''),
      sessionId: ref(''),
      oauthState: ref(''),
      loading: ref(false),
      error: ref(''),
      resetState: vi.fn(),
      generateAuthUrl: vi.fn(),
      exchangeAuthCode: vi.fn(),
      buildCredentials: vi.fn(),
      buildExtraInfo: vi.fn(() => ({}))
    })
  }
})

vi.mock('@/composables/useGeminiOAuth', async () => {
  const { ref } = await vi.importActual<typeof import('vue')>('vue')
  return {
    useGeminiOAuth: () => ({
      authUrl: ref(''),
      sessionId: ref(''),
      state: ref(''),
      loading: ref(false),
      error: ref(''),
      resetState: vi.fn(),
      generateAuthUrl: vi.fn(),
      exchangeAuthCode: vi.fn(),
      buildCredentials: vi.fn(),
      buildExtraInfo: vi.fn(() => ({}))
    })
  }
})

vi.mock('@/composables/useAntigravityOAuth', async () => {
  const { ref } = await vi.importActual<typeof import('vue')>('vue')
  return {
    useAntigravityOAuth: () => ({
      authUrl: ref(''),
      sessionId: ref(''),
      state: ref(''),
      loading: ref(false),
      error: ref(''),
      resetState: vi.fn(),
      generateAuthUrl: vi.fn(),
      exchangeAuthCode: vi.fn(),
      buildCredentials: vi.fn()
    })
  }
})

vi.mock('@/composables/useKiroOAuth', async () => {
  const { ref } = await vi.importActual<typeof import('vue')>('vue')
  return {
    useKiroOAuth: () => ({
      authUrl: ref('https://oidc.us-east-1.amazonaws.com/authorize'),
      sessionId: ref('kiro-session'),
      state: ref('kiro-state'),
      loading: ref(false),
      error: ref(''),
      resetState: resetKiroStateMock,
      generateAuthUrl: vi.fn(),
      generateIDCAuthUrl: vi.fn(),
      exchangeAuthCode: exchangeKiroAuthCodeMock,
      buildCredentials: buildKiroCredentialsMock
    })
  }
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<section v-if="show"><slot /><footer><slot name="footer" /></footer></section>'
})

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  props: {
    platform: {
      type: String,
      default: 'anthropic'
    }
  },
  emits: ['generate-url'],
  setup(_props, { expose }) {
    expose({
      authCode: '',
      oauthState: '',
      oauthCallbackPath: '',
      oauthLoginOption: '',
      oauthIssuerURL: '',
      oauthIDCRegion: '',
      projectId: '',
      sessionKey: '',
      inputMethod: 'manual',
      reset: vi.fn()
    })
    return {}
  },
  template: '<div data-testid="oauth-flow" :data-platform="platform"></div>'
})

function makeKiroIDCAccount() {
  return {
    id: 42,
    name: 'Kiro IDC',
    platform: 'kiro',
    type: 'oauth',
    credentials: {
      auth_method: 'idc',
      start_url: 'https://view.awsapps.com/start',
      region: 'us-east-1',
      refresh_token: 'old-refresh'
    },
    extra: {
      privacy_mode: 'set'
    },
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    group_ids: []
  } as any
}

function mountModal(component: typeof UserReAuthAccountModal | typeof AdminReAuthAccountModal) {
  return mount(component, {
    props: {
      show: true,
      account: makeKiroIDCAccount()
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
        Icon: true
      }
    }
  })
}

describe.each([
  ['user account reauth', UserReAuthAccountModal],
  ['admin account reauth', AdminReAuthAccountModal]
])('Kiro %s', (_label, component) => {
  beforeEach(() => {
    vi.clearAllMocks()
    exchangeKiroAuthCodeMock.mockResolvedValue({
      access_token: 'new-access',
      refresh_token: 'new-refresh',
      auth_method: 'idc',
      region: 'us-east-1'
    })
    buildKiroCredentialsMock.mockImplementation((tokenInfo) => tokenInfo)
    applyOAuthCredentialsMock.mockResolvedValue(makeKiroIDCAccount())
    clearErrorMock.mockResolvedValue(makeKiroIDCAccount())
  })

  it('uses the Kiro OAuth copy and completes IDC reauthorization without a pasted code', async () => {
    const wrapper = mountModal(component)

    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    expect(flow.props('platform')).toBe('kiro')
    expect(wrapper.text()).toContain('Kiro')

    const completeButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.accounts.oauth.completeAuth'))
    expect(completeButton).toBeTruthy()
    expect(completeButton!.attributes('disabled')).toBeUndefined()

    await completeButton!.trigger('click')
    await flushPromises()

    expect(exchangeKiroAuthCodeMock).toHaveBeenCalledWith({
      code: '',
      sessionId: 'kiro-session',
      state: 'kiro-state',
      callbackPath: '',
      loginOption: '',
      proxyId: null
    })
    expect(applyOAuthCredentialsMock).toHaveBeenCalledWith(42, {
      type: 'oauth',
      credentials: {
        access_token: 'new-access',
        refresh_token: 'new-refresh',
        auth_method: 'idc',
        region: 'us-east-1'
      }
    })
  })
})
