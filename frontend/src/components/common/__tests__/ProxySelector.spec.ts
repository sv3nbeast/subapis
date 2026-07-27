import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import ProxySelector from '../ProxySelector.vue'
import type { Proxy } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    proxies: {
      testProxy: vi.fn()
    }
  }
}))

const proxies: Proxy[] = [
  {
    id: 7,
    name: 'shared-proxy',
    protocol: 'http',
    host: '127.0.0.1',
    port: 8080,
    username: null,
    status: 'active',
    account_count: 9,
    expires_at: null,
    fallback_mode: 'none',
    expiry_warn_days: 0,
    created_at: '2026-07-24T00:00:00Z',
    updated_at: '2026-07-24T00:00:00Z'
  }
]

function mountSelector(accountCounts?: Record<number, number>) {
  return mount(ProxySelector, {
    props: {
      modelValue: null,
      proxies,
      ...(accountCounts === undefined ? {} : { accountCounts })
    },
    global: {
      stubs: {
        Icon: true
      }
    }
  })
}

async function openSelector(wrapper: ReturnType<typeof mountSelector>) {
  await wrapper.get('.select-trigger').trigger('click')
}

describe('ProxySelector account count scope', () => {
  it('uses the caller-provided platform count instead of the all-platform total', async () => {
    const wrapper = mountSelector({ 7: 2 })
    await openSelector(wrapper)

    expect(wrapper.get('[data-testid="proxy-account-count-7"]').text()).toBe('2')
  })

  it('keeps the global count for callers that do not provide scoped counts', async () => {
    const wrapper = mountSelector()
    await openSelector(wrapper)

    expect(wrapper.get('[data-testid="proxy-account-count-7"]').text()).toBe('9')
  })

  it('does not show a misleading global count while a scoped count is unavailable', async () => {
    const wrapper = mountSelector({})
    await openSelector(wrapper)

    expect(wrapper.find('[data-testid="proxy-account-count-7"]').exists()).toBe(false)
  })
})
