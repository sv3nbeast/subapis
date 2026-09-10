import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UsageView from '../UsageView.vue'

const {
  query,
  getStats,
  getDashboardModels,
  getDashboardSnapshotV2,
  exportCsv,
  list,
  getAvailable,
  showError,
  showWarning,
  showSuccess,
  showInfo,
} = vi.hoisted(() => ({
  query: vi.fn(),
  getStats: vi.fn(),
  getDashboardModels: vi.fn(),
  getDashboardSnapshotV2: vi.fn(),
  exportCsv: vi.fn(),
  list: vi.fn(),
  getAvailable: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn(),
}))

const messages: Record<string, string> = {
  'usage.allCompactionTypes': 'All Requests',
  'usage.compactionOnly': 'Compaction Only',
  'admin.dashboard.timeRange': 'Time range',
  'admin.dashboard.granularity': 'Granularity',
  'admin.dashboard.day': 'Day',
  'admin.dashboard.hour': 'Hour',
  'admin.users.columnSettings': 'Columns',
  'admin.usage.group': 'Group',
  'admin.usage.billingType': 'Billing type',
  'admin.usage.billingMode': 'Billing Mode',
  'admin.usage.allTypes': 'All types',
  'admin.usage.allBillingTypes': 'All billing types',
  'admin.usage.billingTypeBalance': 'Balance',
  'admin.usage.billingTypeSubscription': 'Subscription',
  'admin.usage.allBillingModes': 'All billing modes',
  'admin.usage.billingModeToken': 'Token',
  'admin.usage.billingModePerRequest': 'Per request',
  'admin.usage.billingModeImage': 'Image',
  'admin.usage.allGroups': 'All groups',
  'admin.usage.allModels': 'All models',
  'admin.usage.inputTokens': 'Input Tokens',
  'admin.usage.outputTokens': 'Output Tokens',
  'admin.usage.cacheReadTokens': 'Cache Read Tokens',
  'admin.usage.cacheCreationTokens': 'Cache Creation Tokens',
  'admin.usage.ipAddress': 'IP Address',
  'usage.allApiKeys': 'All API Keys',
  'usage.apiKeyFilter': 'API Key',
  'usage.model': 'Model',
  'usage.reasoningEffort': 'Reasoning Effort',
  'usage.inboundEndpoint': 'Inbound Endpoint',
  'usage.type': 'Type',
  'usage.ws': 'WS',
  'usage.stream': 'Stream',
  'usage.sync': 'Sync',
  'usage.rate': 'Rate Multiplier',
  'usage.userBilled': 'Billed Cost',
  'usage.original': 'Original Cost',
  'usage.firstToken': 'First Token',
  'usage.duration': 'Duration',
  'usage.exporting': 'Exporting',
  'usage.exportCsv': 'Export CSV',
  'usage.failedToLoad': 'Failed to load',
  'usage.noDataToExport': 'No data',
  'usage.preparingExport': 'Preparing export',
  'usage.exportSuccess': 'Export success',
  'usage.exportFailed': 'Export failed',
  'usage.cancelExport': 'Cancel export',
  'usage.exportCancelled': 'Export cancelled',
  'usage.exportedCount': 'Exported rows',
  'usage.exportTooLarge': 'Export too large',
  'usage.exportInProgress': 'Export in progress',
  'usage.exportRateLimited': 'Rate limited',
  'common.refresh': 'Refresh',
  'common.reset': 'Reset',
}

vi.mock('@/api', () => ({
  usageAPI: {
    query,
    getStats,
    getDashboardModels,
    getDashboardSnapshotV2,
    exportCsv,
  },
  keysAPI: {
    list,
  },
  userGroupsAPI: {
    getAvailable,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    cachedPublicSettings: { allow_user_view_error_requests: false },
    showError,
    showWarning,
    showSuccess,
    showInfo,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const simpleStub = { template: '<div><slot /></div>' }
const chartStub = { template: '<div />' }

const usageLog = {
  id: 1,
  request_id: 'req-user-export',
  actual_cost: 0.092883,
  total_cost: 0.092883,
  rate_multiplier: 1,
  service_tier: 'priority',
  input_cost: 0.020285,
  output_cost: 0.00303,
  cache_creation_cost: 0.000001,
  cache_read_cost: 0.069568,
  input_tokens: 4057,
  output_tokens: 101,
  cache_creation_tokens: 4,
  cache_read_tokens: 278272,
  cache_creation_5m_tokens: 0,
  cache_creation_1h_tokens: 0,
  image_count: 0,
  image_size: null,
  first_token_ms: 12,
  duration_ms: 345,
  created_at: '2026-03-08T00:00:00Z',
  model: 'gpt-5.4',
  reasoning_effort: null,
  ip_address: '203.0.113.10',
  api_key: { name: 'demo-key' },
  billing_mode: 'token',
  request_type: 'sync',
  stream: false,
  native_compaction_v2: false,
}

function mountUsageView() {
  return mount(UsageView, {
    global: {
      stubs: {
        AppLayout: simpleStub,
        Pagination: true,
        Select: true,
        DateRangePicker: true,
        Icon: true,
        UsageStatsCards: chartStub,
        UsageTable: chartStub,
        ModelDistributionChart: chartStub,
        GroupDistributionChart: chartStub,
        EndpointDistributionChart: chartStub,
        TokenUsageTrend: chartStub,
      },
    },
  })
}

function stubDownload() {
  const originalCreateObjectURL = window.URL.createObjectURL
  const originalRevokeObjectURL = window.URL.revokeObjectURL
  const createObjectURL = vi.fn(() => 'blob:usage-export')
  window.URL.createObjectURL = createObjectURL as typeof window.URL.createObjectURL
  window.URL.revokeObjectURL = vi.fn(() => {}) as typeof window.URL.revokeObjectURL
  const downloads: string[] = []
  const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    downloads.push(this.download)
  })
  return {
    createObjectURL,
    clickSpy,
    downloads,
    restore() {
      window.URL.createObjectURL = originalCreateObjectURL
      window.URL.revokeObjectURL = originalRevokeObjectURL
      clickSpy.mockRestore()
    },
  }
}

function abortableExport() {
  return vi.fn((_params: unknown, options: { signal?: AbortSignal }) =>
    new Promise((_resolve, reject) => {
      options.signal?.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')))
    }),
  )
}

describe('user UsageView', () => {
  beforeEach(() => {
    query.mockReset()
    getStats.mockReset()
    getDashboardModels.mockReset()
    getDashboardSnapshotV2.mockReset()
    exportCsv.mockReset()
    list.mockReset()
    getAvailable.mockReset()
    showError.mockReset()
    showWarning.mockReset()
    showSuccess.mockReset()
    showInfo.mockReset()

    query.mockResolvedValue({ items: [usageLog], total: 1, pages: 1 })
    getStats.mockResolvedValue({
      total_requests: 1,
      total_input_tokens: 10,
      total_output_tokens: 20,
      total_cache_tokens: 0,
      total_tokens: 30,
      total_cost: 0.1,
      total_actual_cost: 0.08,
      average_duration_ms: 12,
      endpoints: [],
      upstream_endpoints: [],
      endpoint_paths: [],
    })
    getDashboardModels.mockResolvedValue({
      models: [{ model: 'gpt-5.4', requests: 1, input_tokens: 10, output_tokens: 20, cache_creation_tokens: 0, cache_read_tokens: 0, total_tokens: 30, cost: 0.1, actual_cost: 0.08 }],
      start_date: '2026-03-08',
      end_date: '2026-03-08',
    })
    getDashboardSnapshotV2.mockResolvedValue({
      generated_at: '2026-03-08T00:00:00Z',
      start_date: '2026-03-08',
      end_date: '2026-03-08',
      granularity: 'hour',
      trend: [],
      groups: [],
    })
    list.mockResolvedValue({ items: [{ id: 1, name: 'demo-key' }] })
    getAvailable.mockResolvedValue([{ id: 1, name: 'default' }])
  })

  it('loads logs, stats, model stats, and snapshot on first render', async () => {
    mountUsageView()
    await flushPromises()

    expect(query).toHaveBeenCalled()
    expect(getStats).toHaveBeenCalled()
    expect(getDashboardModels).toHaveBeenCalled()
    expect(getDashboardSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      include_trend: true,
      include_model_stats: false,
      include_group_stats: true,
    }))
    expect(list).toHaveBeenCalledWith(1, 100)
    expect(getAvailable).toHaveBeenCalled()
  })

  it('propagates and resets the native compaction filter across page requests', async () => {
    const wrapper = mountUsageView()
    await flushPromises()

    expect((wrapper.vm as any).compactionOptions).toEqual([
      { value: null, label: 'All Requests' },
      { value: true, label: 'Compaction Only' },
    ])

    query.mockClear()
    getStats.mockClear()
    getDashboardModels.mockClear()
    getDashboardSnapshotV2.mockClear()

    ;(wrapper.vm as any).filters.native_compaction_v2 = true
    ;(wrapper.vm as any).applyFilters()
    await flushPromises()

    expect(query).toHaveBeenCalledWith(
      expect.objectContaining({ native_compaction_v2: true }),
      expect.anything()
    )
    expect(getStats).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: true }))
    expect(getDashboardModels).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: true }))
    expect(getDashboardSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: true }))

    query.mockClear()
    getStats.mockClear()
    getDashboardModels.mockClear()
    getDashboardSnapshotV2.mockClear()

    ;(wrapper.vm as any).resetFilters()
    await flushPromises()

    expect((wrapper.vm as any).filters.native_compaction_v2).toBeNull()
    expect(query).toHaveBeenCalledWith(
      expect.objectContaining({ native_compaction_v2: null }),
      expect.anything()
    )
    expect(getStats).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: null }))
    expect(getDashboardModels).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: null }))
    expect(getDashboardSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({ native_compaction_v2: null }))
  })

  it('exports through the streaming endpoint with the current filters and never pages the list', async () => {
    const wrapper = mountUsageView()
    await flushPromises()
    query.mockClear()
    const download = stubDownload()
    const blob = new Blob(['\uFEFFTime,Model\n2026-03-08 00:00:00,gpt-5.4\n'], { type: 'text/csv;charset=utf-8;' })
    exportCsv.mockResolvedValue({ blob, filename: 'usage_2026-03-01_to_2026-03-08.csv', rows: 1 })
    ;(wrapper.vm as any).filters.native_compaction_v2 = true

    await (wrapper.vm as any).exportToCSV()

    expect(exportCsv).toHaveBeenCalledTimes(1)
    const [params, options] = exportCsv.mock.calls[0]
    expect(params).toEqual(expect.objectContaining({
      start_date: expect.any(String),
      end_date: expect.any(String),
      native_compaction_v2: true,
      sort_by: 'created_at',
      sort_order: 'desc',
    }))
    expect(params).not.toHaveProperty('page')
    expect(params).not.toHaveProperty('page_size')
    expect(options.signal).toBeInstanceOf(AbortSignal)
    expect(typeof options.onProgress).toBe('function')
    expect(query).not.toHaveBeenCalled()
    expect(download.createObjectURL).toHaveBeenCalledWith(blob)
    expect(download.downloads).toEqual(['usage_2026-03-01_to_2026-03-08.csv'])
    expect(showSuccess).toHaveBeenCalledWith('Export success')
    expect((wrapper.vm as any).exporting).toBe(false)

    download.restore()
  })

  it('shows a cancel control while exporting and reports cancellation instead of failure', async () => {
    const wrapper = mountUsageView()
    await flushPromises()
    const download = stubDownload()
    exportCsv.mockImplementation(abortableExport())

    const pending = (wrapper.vm as any).exportToCSV()
    await flushPromises()

    const cancelButton = wrapper.find('[data-testid="usage-export-cancel"]')
    expect(cancelButton.exists()).toBe(true)
    expect(wrapper.find('[data-testid="usage-export"]').attributes('disabled')).toBeDefined()

    const [, options] = exportCsv.mock.calls[0]
    options.onProgress({ receivedBytes: 2048, receivedRows: 12 })
    await flushPromises()
    expect(wrapper.find('[data-testid="usage-export"]').text()).toBe('Exported rows')

    await cancelButton.trigger('click')
    await pending
    await flushPromises()

    expect(showInfo).toHaveBeenCalledWith('Export cancelled')
    expect(showError).not.toHaveBeenCalled()
    expect(download.clickSpy).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="usage-export-cancel"]').exists()).toBe(false)
    expect((wrapper.vm as any).exporting).toBe(false)

    download.restore()
  })

  it('maps server-side export limits to localized messages', async () => {
    const wrapper = mountUsageView()
    await flushPromises()
    const download = stubDownload()

    exportCsv.mockRejectedValueOnce({
      status: 400,
      reason: 'USAGE_EXPORT_TOO_LARGE',
      message: 'Export exceeds the maximum row limit',
      metadata: { limit: '500000' },
    })
    await (wrapper.vm as any).exportToCSV()
    expect(showError).toHaveBeenLastCalledWith('Export too large')

    exportCsv.mockRejectedValueOnce({ status: 429, reason: 'USAGE_EXPORT_IN_PROGRESS', message: 'running' })
    await (wrapper.vm as any).exportToCSV()
    expect(showError).toHaveBeenLastCalledWith('Export in progress')

    exportCsv.mockRejectedValueOnce({ status: 429, code: 'RATE_LIMITED', message: 'slow down' })
    await (wrapper.vm as any).exportToCSV()
    expect(showError).toHaveBeenLastCalledWith('Rate limited')

    exportCsv.mockRejectedValueOnce({ status: 502, message: 'Bad Gateway' })
    await (wrapper.vm as any).exportToCSV()
    expect(showError).toHaveBeenLastCalledWith('Export failed: Bad Gateway')

    expect(download.clickSpy).not.toHaveBeenCalled()
    download.restore()
  })

  it('warns instead of downloading when the export contains no rows', async () => {
    const wrapper = mountUsageView()
    await flushPromises()
    const download = stubDownload()
    exportCsv.mockResolvedValue({ blob: new Blob(['Time\n']), filename: 'usage_all.csv', rows: 0 })

    await (wrapper.vm as any).exportToCSV()

    expect(showWarning).toHaveBeenCalledWith('No data')
    expect(download.clickSpy).not.toHaveBeenCalled()
    download.restore()
  })

  it('skips the request entirely when the current filters match no records', async () => {
    query.mockResolvedValue({ items: [], total: 0, pages: 0 })
    const wrapper = mountUsageView()
    await flushPromises()

    await (wrapper.vm as any).exportToCSV()

    expect(exportCsv).not.toHaveBeenCalled()
    expect(showWarning).toHaveBeenCalledWith('No data')
  })
})
