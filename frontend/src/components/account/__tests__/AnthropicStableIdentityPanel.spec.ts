import { describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

import type { Account, AdminGroup } from '@/types'

const { getStatusMock, configureMock, getAccountMock, getGroupKeysMock } = vi.hoisted(() => ({
  getStatusMock: vi.fn(),
  configureMock: vi.fn(),
  getAccountMock: vi.fn(),
  getGroupKeysMock: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAnthropicStableIdentity: getStatusMock,
      configureAnthropicStableIdentity: configureMock,
      getById: getAccountMock,
      pauseAnthropicStableIdentity: vi.fn(),
      resumeAnthropicStableIdentity: vi.fn(),
      disableAnthropicStableIdentity: vi.fn()
    },
    groups: {
      getGroupApiKeys: getGroupKeysMock
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

const groups = [
  { id: 11, name: 'Claude latest', platform: 'anthropic', status: 'active', account_count: 3 },
  { id: 13, name: 'Claude historical', platform: 'anthropic', status: 'active', account_count: 2 },
  { id: 12, name: 'Kiro', platform: 'kiro', status: 'active', account_count: 2 }
] as unknown as AdminGroup[]

const offStatus = {
  account_id: 81,
  enabled: false,
  state: 'off' as const,
  blocked: false,
  group_ids: [],
  api_key_ids: [],
  generation: 0,
  profile_id: '',
  device_configured: false,
  concurrency: 4,
  schedulable: true,
  requires_restart: false
}

describe('AnthropicStableIdentityPanel', () => {
  it('enables the account in its existing group and selects all active group keys by default', async () => {
    getStatusMock.mockResolvedValue(offStatus)
    getGroupKeysMock.mockResolvedValue({
      items: [
        { id: 101, name: 'Alice', group_id: 11, status: 'active', expires_at: null },
        { id: 102, name: 'Bob', group_id: 11, status: 'active', expires_at: null },
        { id: 103, name: 'Expired', group_id: 11, status: 'active', expires_at: '2020-01-01T00:00:00Z' }
      ],
      pages: 1,
      total: 3
    })
    configureMock.mockResolvedValue({
      ...offStatus,
      enabled: true,
      state: 'active',
      group_ids: [11],
      api_key_ids: [101, 102],
      generation: 1,
      device_configured: true,
      concurrency: 1,
      schedulable: false
    })
    getAccountMock.mockResolvedValue(account)

    const wrapper = mount(AnthropicStableIdentityPanel, {
      props: { account, groups, oauthPassthroughEnabled: false },
      global: { stubs: { Icon: IconStub } }
    })
    await flushPromises()

    const enable = wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.accounts.anthropic.stableIdentity.enable')
    expect(enable).toBeDefined()
    await enable!.trigger('click')
    await flushPromises()

    expect(configureMock).toHaveBeenCalledWith(81, {
      group_ids: [11],
      api_key_ids: [101, 102]
    })
    expect(getGroupKeysMock).toHaveBeenCalledWith(11, 1, 1000)
  })

  it('adds the account to another existing group without losing explicit key selections', async () => {
    getStatusMock.mockResolvedValue(offStatus)
    getGroupKeysMock.mockImplementation(async (groupID: number) => ({
      items: groupID === 11
        ? [
            { id: 101, name: 'Alice', group_id: 11, status: 'active', expires_at: null },
            { id: 102, name: 'Bob', group_id: 11, status: 'active', expires_at: null }
          ]
        : [{ id: 201, name: 'Carol', group_id: 13, status: 'active', expires_at: null }],
      pages: 1,
      total: groupID === 11 ? 2 : 1
    }))
    configureMock.mockResolvedValue({
      ...offStatus,
      enabled: true,
      state: 'active',
      group_ids: [11, 13],
      api_key_ids: [101, 201],
      generation: 1,
      device_configured: true,
      concurrency: 1,
      schedulable: false
    })
    getAccountMock.mockResolvedValue({ ...account, group_ids: [11, 13] })

    const wrapper = mount(AnthropicStableIdentityPanel, {
      props: { account, groups, oauthPassthroughEnabled: false },
      global: { stubs: { Icon: IconStub } }
    })
    await flushPromises()

    const bob = wrapper.findAll('label').find((label) => label.text().includes('Bob'))
    expect(bob).toBeDefined()
    await bob!.get('input[type="checkbox"]').setValue(false)

    const historical = wrapper.findAll('label').find((label) => label.text().includes('Claude historical'))
    expect(historical).toBeDefined()
    await historical!.get('input[type="checkbox"]').setValue(true)
    await flushPromises()

    const enable = wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.accounts.anthropic.stableIdentity.enable')
    await enable!.trigger('click')
    await flushPromises()

    expect(configureMock).toHaveBeenCalledWith(81, {
      group_ids: [11, 13],
      api_key_ids: [101, 201]
    })
  })
})
