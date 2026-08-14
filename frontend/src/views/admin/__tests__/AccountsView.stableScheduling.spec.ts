import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'

const {
  listAccounts,
  listWithEtag,
  getBatchTodayStats,
  getAllProxies,
  getAllGroups,
  setSchedulable,
  showInfo
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  setSchedulable: vi.fn(),
  showInfo: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: true }),
      setSchedulable,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn()
    },
    proxies: { getAll: getAllProxies, getAllWithCount: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', isSimpleMode: false })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      te: () => false,
      locale: { value: 'zh-CN' }
    })
  }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-test="'schedulable-cell-' + row.id">
        <slot name="cell-schedulable" :row="row" />
      </div>
    </div>
  `
}

const baseAccount = {
  platform: 'anthropic',
  type: 'oauth',
  status: 'active',
  schedulable: false,
  concurrency: 1,
  priority: 0,
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  created_at: '2026-08-14T00:00:00Z',
  updated_at: '2026-08-14T00:00:00Z'
}

const mountView = () =>
  mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
        },
        DataTable: DataTableStub,
        Pagination: true,
        ConfirmDialog: true,
        AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
        AccountTableFilters: { template: '<div></div>' },
        AccountBulkActionsBar: true,
        AccountActionMenu: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: true,
        AccountStatsModal: true,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: true,
        BulkEditAccountModal: true,
        PlatformTypeBadge: true,
        AccountCapacityCell: true,
        AccountStatusIndicator: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: true,
        AccountUsageCell: true,
        Icon: true
      }
    }
  })

describe('admin AccountsView stable identity scheduling state', () => {
  beforeEach(() => {
    localStorage.clear()
    for (const mock of [
      listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getAllProxies,
      getAllGroups,
      setSchedulable,
      showInfo
    ]) {
      mock.mockReset()
    }
    listAccounts.mockResolvedValue({
      items: [
        {
          ...baseAccount,
          id: 2550,
          name: 'pro-test-0814',
          anthropic_stable_identity_enabled: true,
          anthropic_stable_identity_state: 'active',
          anthropic_stable_identity_blocked: false
        },
        {
          ...baseAccount,
          id: 2551,
          name: 'regular-account',
          schedulable: true
        },
        {
          ...baseAccount,
          id: 2552,
          name: 'paused-stable-account',
          anthropic_stable_identity_enabled: true,
          anthropic_stable_identity_state: 'paused',
          anthropic_stable_identity_blocked: false
        },
        {
          ...baseAccount,
          id: 2553,
          name: 'blocked-stable-account',
          anthropic_stable_identity_enabled: true,
          anthropic_stable_identity_state: 'active',
          anthropic_stable_identity_blocked: true
        }
      ],
      total: 4,
      page: 1,
      page_size: 20,
      pages: 1
    })
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
    setSchedulable.mockResolvedValue({ ...baseAccount, id: 2551, schedulable: false })
  })

  it('shows an active stable-scheduling badge instead of an off generic switch', async () => {
    const wrapper = mountView()
    await flushPromises()

    const stableCell = wrapper.get('[data-test="schedulable-cell-2550"]')
    const badge = stableCell.get('[data-test="stable-scheduling-2550"]')
    expect(badge.attributes('data-state')).toBe('active')
    expect(badge.text()).toContain('admin.accounts.stableSchedulableActive')
    expect(stableCell.find('button').exists()).toBe(false)

    const pausedBadge = wrapper
      .get('[data-test="schedulable-cell-2552"]')
      .get('[data-test="stable-scheduling-2552"]')
    expect(pausedBadge.attributes('data-state')).toBe('paused')
    expect(pausedBadge.text()).toContain('admin.accounts.stableSchedulablePaused')

    const blockedBadge = wrapper
      .get('[data-test="schedulable-cell-2553"]')
      .get('[data-test="stable-scheduling-2553"]')
    expect(blockedBadge.attributes('data-state')).toBe('blocked')
    expect(blockedBadge.text()).toContain('admin.accounts.stableSchedulableBlocked')
    expect(setSchedulable).not.toHaveBeenCalled()

    wrapper.unmount()
  })

  it('keeps the generic scheduling switch working for regular accounts', async () => {
    const wrapper = mountView()
    await flushPromises()

    const regularCell = wrapper.get('[data-test="schedulable-cell-2551"]')
    await regularCell.get('button').trigger('click')
    await flushPromises()

    expect(setSchedulable).toHaveBeenCalledWith(2551, false)
    expect(showInfo).not.toHaveBeenCalled()

    wrapper.unmount()
  })
})
