import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AppHeader from '../AppHeader.vue'

const { appStore } = vi.hoisted(() => ({
  appStore: {
    contactInfo: '',
    docUrl: '',
    siteLogo: '',
    siteName: 'Sub2API',
    publicSettingsLoaded: true,
    cachedPublicSettings: null,
    mobileOpen: false,
    serviceStatus: null as any,
    toggleMobileSidebar: vi.fn(),
  },
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('vue-router', () => ({
  useRoute: () => ({ name: 'Dashboard', meta: {}, params: {} }),
  useRouter: () => ({ push: vi.fn() }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
  useAuthStore: () => ({
    isAdmin: false,
    isSimpleMode: false,
    logout: vi.fn(),
    user: {
      id: 7,
      username: 'Sven',
      email: 'sven@example.com',
      role: 'user',
      balance: 0,
      frozen_balance: 0,
    },
  }),
  useOnboardingStore: () => ({ replay: vi.fn() }),
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] }),
}))

vi.mock('@/composables/useBottomSheetGesture', () => ({
  useBottomSheetGesture: () => ({
    beginSheetDrag: vi.fn(),
    moveSheetDrag: vi.fn(),
    endSheetDrag: vi.fn(),
    cancelSheetDrag: vi.fn(),
  }),
}))

describe('AppHeader', () => {
  beforeEach(() => {
    appStore.serviceStatus = null
  })

  it('shows the shared degraded service status in the v2 top bar', () => {
    appStore.serviceStatus = {
      overall_status: 'degraded',
      public_visible: true,
      interval_minutes: 5,
      models: [{
        model: 'claude-sonnet-4',
        display_name: 'Claude',
        current_status: 'degraded',
        uptime_percentage: 98.5,
        hourly_stats: [],
      }],
      last_updated: null,
    }

    const wrapper = mount(AppHeader, {
      props: { uiVersion: 'v2' },
      global: {
        stubs: {
          AnnouncementBell: true,
          Icon: true,
          LocaleSwitcher: true,
          RouterLink: { template: '<a><slot /></a>' },
          SubscriptionProgressMini: true,
        },
      },
    })

    const status = wrapper.get('.ui-v2-topbar-context')
    expect(status.classes()).toContain('is-degraded')
    expect(status.text()).toContain('status.degraded')
  })
})
