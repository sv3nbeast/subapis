import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DashboardView from '../DashboardView.vue'

const { authStore, getDashboardStats, getDashboardTrend, getDashboardModels, query, getMyPlatformQuotas } = vi.hoisted(() => ({
  authStore: {
    user: { id: 7, username: 'Sven', email: 'sven@example.com', balance: 25 },
    isSimpleMode: false,
    refreshUser: vi.fn(),
  },
  getDashboardStats: vi.fn(),
  getDashboardTrend: vi.fn(),
  getDashboardModels: vi.fn(),
  query: vi.fn(),
  getMyPlatformQuotas: vi.fn(),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authStore,
}))

vi.mock('@/composables/useUiVersion', () => ({
  useUiVersion: () => ({ uiVersion: { value: 'v2' } }),
}))

vi.mock('@/api/usage', () => ({
  usageAPI: { getDashboardStats, getDashboardTrend, getDashboardModels, query },
}))

vi.mock('@/api/user', () => ({
  userAPI: { getMyPlatformQuotas },
}))

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: {
    setup(_: unknown, { slots }: { slots: { default?: (props: { uiVersion: 'v2' }) => unknown } }) {
      return () => slots.default?.({ uiVersion: 'v2' })
    },
  },
}))

vi.mock('@/components/common/LoadingSpinner.vue', () => ({
  default: { template: '<div data-testid="loading"></div>' },
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<i></i>' },
}))

vi.mock('@/components/user/dashboard/DashboardRequestTrend.vue', () => ({
  default: { template: '<div data-testid="overview-trend"></div>' },
}))

vi.mock('@/components/user/dashboard/UserDashboardStats.vue', () => ({
  default: {
    props: ['mode'],
    template: '<div data-testid="dashboard-stats" :data-mode="mode"></div>',
  },
}))

vi.mock('@/components/status/ServiceStatusOverview.vue', () => ({
  default: { template: '<div data-testid="service-status"></div>' },
}))

vi.mock('@/components/user/dashboard/UserDashboardRecentUsage.vue', () => ({
  default: { template: '<div data-testid="recent-usage"></div>' },
}))

vi.mock('@/components/user/dashboard/UserDashboardQuickActions.vue', () => ({
  default: { template: '<div data-testid="quick-actions"></div>' },
}))

vi.mock('@/components/user/dashboard/UserDashboardCharts.vue', () => ({
  default: { template: '<div data-testid="analytics-charts"></div>' },
}))

describe('user DashboardView v2', () => {
  beforeEach(() => {
    authStore.refreshUser.mockReset().mockResolvedValue(undefined)
    getDashboardStats.mockReset().mockResolvedValue({ today_requests: 12 })
    getDashboardTrend.mockReset().mockResolvedValue({ trend: [] })
    getDashboardModels.mockReset().mockResolvedValue({ models: [] })
    query.mockReset().mockResolvedValue({ items: [] })
    getMyPlatformQuotas.mockReset().mockResolvedValue({ platform_quotas: [] })
  })

  it('keeps model analytics off the initial path and loads them after selecting Analysis', async () => {
    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
        },
      },
    })

    await flushPromises()

    expect(authStore.refreshUser).not.toHaveBeenCalled()
    expect(getDashboardModels).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="overview-trend"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="analytics-charts"]').exists()).toBe(false)
    expect(query).toHaveBeenCalledWith(expect.objectContaining({ page_size: 5 }))

    await wrapper.get('[data-dashboard-view="analysis"]').trigger('click')
    await flushPromises()

    expect(getDashboardModels).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="overview-trend"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="analytics-charts"]').exists()).toBe(true)
  })
})
