import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import MonitorFormDialog from '../MonitorFormDialog.vue'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    cachedPublicSettings: {},
    showError: vi.fn(),
    showSuccess: vi.fn(),
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    channelMonitorTemplate: {
      list: vi.fn().mockResolvedValue({ items: [] }),
    },
    channelMonitor: {
      create: vi.fn(),
      update: vi.fn(),
    },
  },
}))

vi.mock('@/api/keys', () => ({
  keysAPI: { list: vi.fn() },
}))

vi.mock('@/api/groups', () => ({
  userGroupsAPI: { getUserGroupRates: vi.fn() },
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

describe('MonitorFormDialog', () => {
  it('provides Grok as a selectable channel monitor type', async () => {
    const wrapper = mount(MonitorFormDialog, {
      props: { show: true, monitor: null },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>',
          },
          Toggle: true,
          Select: true,
          ModelTagInput: true,
          MonitorKeyPickerDialog: true,
          MonitorAdvancedRequestConfig: true,
          ProviderIcon: true,
        },
      },
    })

    await flushPromises()

    const grokButton = wrapper.findAll('button').find((button) =>
      button.text().includes('monitorCommon.providers.grok'),
    )
    expect(grokButton).toBeTruthy()
    expect(grokButton?.attributes('aria-pressed')).toBe('false')

    await grokButton?.trigger('click')

    expect(grokButton?.attributes('aria-pressed')).toBe('true')
  })
})
