import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

import type { Account } from '@/types'

const { getStatusMock, configureMock, disableMock, getAccountMock } = vi.hoisted(
  () => ({
    getStatusMock: vi.fn(),
    configureMock: vi.fn(),
    disableMock: vi.fn(),
    getAccountMock: vi.fn()
  })
)

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAnthropicStableIdentity: getStatusMock,
      configureAnthropicStableIdentity: configureMock,
      getById: getAccountMock,
      disableAnthropicStableIdentity: disableMock
    }
  }
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

import AnthropicStableIdentityPanel from '../AnthropicStableIdentityPanel.vue'

const IconStub = defineComponent({
  name: 'Icon',
  template: '<span />'
})

const account = {
  id: 81,
  name: 'existing-group-oauth',
  platform: 'anthropic',
  type: 'oauth',
  group_ids: [11]
} as unknown as Account

const offStatus = {
  account_id: 81,
  enabled: false,
  state: 'off' as const,
  blocked: false,
  group_ids: [11],
  generation: 0,
  profile_id: '',
  device_configured: false,
  concurrency: 4,
  schedulable: true,
  requires_restart: false
}

const onStatus = {
  ...offStatus,
  enabled: true,
  state: 'active' as const,
  group_ids: [11, 13],
  generation: 1,
  device_fingerprint: 'abcdef123456',
  device_configured: true,
  concurrency: 1,
  schedulable: false
}

const mountPanel = () =>
  mount(AnthropicStableIdentityPanel, {
    props: { account },
    global: { stubs: { Icon: IconStub } }
  })

describe('AnthropicStableIdentityPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getAccountMock.mockResolvedValue(account)
  })

  it('enables stable mode with one account-level switch and no routing selectors', async () => {
    getStatusMock.mockResolvedValue(offStatus)
    configureMock.mockResolvedValue(onStatus)
    const wrapper = mountPanel()
    await flushPromises()

    const toggle = wrapper.get('button[role="switch"]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    await flushPromises()

    expect(configureMock).toHaveBeenCalledTimes(1)
    expect(configureMock).toHaveBeenCalledWith(81)
    expect(getAccountMock).toHaveBeenCalledWith(81)
    expect(toggle.attributes('aria-checked')).toBe('true')
    expect(wrapper.text()).toContain('abcdef123456')
  })

  it('disables stable mode from the same switch', async () => {
    getStatusMock.mockResolvedValue(onStatus)
    disableMock.mockResolvedValue(offStatus)
    const wrapper = mountPanel()
    await flushPromises()

    const toggle = wrapper.get('button[role="switch"]')
    expect(toggle.attributes('aria-checked')).toBe('true')
    await toggle.trigger('click')
    await flushPromises()

    expect(disableMock).toHaveBeenCalledWith(81)
    expect(configureMock).not.toHaveBeenCalled()
    expect(toggle.attributes('aria-checked')).toBe('false')
  })

  it('surfaces the backend reason without changing the displayed state', async () => {
    getStatusMock.mockResolvedValue(offStatus)
    configureMock.mockRejectedValue({ response: { data: { detail: 'add one Anthropic group first' } } })
    const wrapper = mountPanel()
    await flushPromises()

    await wrapper.get('button[role="switch"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('add one Anthropic group first')
    expect(wrapper.get('button[role="switch"]').attributes('aria-checked')).toBe('false')
  })
})
