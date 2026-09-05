import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelsView from '../ChannelsView.vue'

const api = vi.hoisted(() => ({
  list: vi.fn(), getAll: vi.fn(), groups: vi.fn(), update: vi.fn(),
  showError: vi.fn(), showSuccess: vi.fn()
}))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    channels: { list: api.list, getAll: api.getAll, update: api.update },
    groups: { getAll: api.groups },
    settings: { getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false }) }
  }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => api }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

describe('ChannelsView CN platform round trips', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.getAll.mockResolvedValue([])
    api.update.mockResolvedValue({})
  })

  it.each(['kimi', 'zhipu', 'deepseek'].flatMap(platform =>
    [false, true].map(composite => ({ platform, composite }))
  ))('retains $platform pricing/mapping with composite=$composite', async ({ platform, composite }) => {
    const pricing = {
      platform, models: ['public-cn'], enabled: true, billing_mode: 'token',
      input_price: 0.000002, output_price: 0.000006, cache_read_price: 0.0000002,
      cache_write_price: null, intervals: []
    }
    const channel = {
      id: 20, name: 'CN channel', status: 'active', group_ids: [99],
      model_mapping: { [platform]: { 'public-cn': 'native-cn' } },
      model_pricing: [pricing],
      account_stats_pricing_rules: [{
        name: 'Account cost', group_ids: [99], account_ids: [], pricing: [{ ...pricing, input_price: 0.000001 }]
      }]
    }
    api.list.mockResolvedValue({ items: [channel], total: 1 })
    api.groups.mockResolvedValue([{ id: 99, name: 'Target group', platform: composite ? 'composite' : platform }])
    const wrapper = mount(ChannelsView, { global: { stubs: {
      AppLayout: { template: '<main><slot /></main>' },
      TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/></div>' },
      DataTable: {
        props: ['data'], template: '<div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row"/></div>'
      },
      BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' },
      PricingEntryCard: true, Select: true, Icon: true, PlatformIcon: true,
      Toggle: true, Pagination: true, ConfirmDialog: true, EmptyState: true
    } } })
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.groups.platforms.' + platform)
    expect(wrapper.text()).toContain('Target group')
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(api.showError).not.toHaveBeenCalled()
    expect(api.update).toHaveBeenCalledWith(20, expect.objectContaining({
      group_ids: [99],
      model_mapping: { [platform]: { 'public-cn': 'native-cn' } },
      model_pricing: [expect.objectContaining(pricing)],
      account_stats_pricing_rules: [expect.objectContaining({
        name: 'Account cost', group_ids: [99],
        pricing: [expect.objectContaining({ platform, input_price: 0.000001 })]
      })]
    }))
    wrapper.unmount()
  })
})
