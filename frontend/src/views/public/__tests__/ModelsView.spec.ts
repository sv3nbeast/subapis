import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelsView from '@/views/public/ModelsView.vue'

const { getPublicModels, authStore, appStore } = vi.hoisted(() => ({
  getPublicModels: vi.fn(),
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
    checkAuth: vi.fn(),
  },
  appStore: {
    siteName: 'Sub2API',
    siteLogo: '',
    cachedPublicSettings: {
      site_name: 'Sub2API',
      site_logo: '',
      server_utc_offset: '+08:00',
    },
  },
}))

vi.mock('@/api/publicModels', () => ({
  default: { getPublicModels },
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => authStore,
  useAppStore: () => appStore,
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, values?: Record<string, unknown>) =>
        values ? `${key}:${Object.values(values).join(',')}` : key,
    }),
  }
})

const RouterLinkStub = {
  props: ['to'],
  template: '<a :data-to="typeof to === \'string\' ? to : to.path"><slot /></a>',
}

function mountView() {
  return mount(ModelsView, {
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        LocaleSwitcher: true,
        Icon: true,
        ModelIcon: true,
      },
    },
  })
}

describe('public ModelsView', () => {
  beforeEach(() => {
    authStore.isAuthenticated = false
    authStore.isAdmin = false
    authStore.checkAuth.mockReset()
    getPublicModels.mockReset()
    getPublicModels.mockResolvedValue({
      groups: [
        {
          name: 'Claude Standard',
          platform: 'anthropic',
          subscription_type: 'standard',
          rate_multiplier: 1,
          peak_rate_enabled: false,
          peak_start: '',
          peak_end: '',
          peak_rate_multiplier: 1,
          models: [{
            name: 'claude-sonnet-4-6',
            platform: 'anthropic',
            pricing: {
              billing_mode: 'token', input_price: 0.000003, output_price: 0.000015,
              cache_write_price: null, cache_write_5m_price: null, cache_write_1h_price: null,
              cache_read_price: 0.0000003, image_output_price: null, per_request_price: null, intervals: [],
            },
          }],
        },
        {
          name: 'Claude Subscription',
          platform: 'anthropic',
          subscription_type: 'subscription',
          rate_multiplier: 0.8,
          peak_rate_enabled: true,
          peak_start: '14:00',
          peak_end: '18:00',
          peak_rate_multiplier: 1.2,
          models: [{
            name: 'claude-sonnet-4-6',
            platform: 'anthropic',
            pricing: {
              billing_mode: 'token', input_price: 0.000003, output_price: 0.000015,
              cache_write_price: null, cache_write_5m_price: null, cache_write_1h_price: null,
              cache_read_price: 0.0000003, image_output_price: null, per_request_price: null, intervals: [],
            },
          }],
        },
      ],
    })
  })

  it('aggregates duplicate models and expands their public group offers', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(getPublicModels).toHaveBeenCalledOnce()
    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.text()).toContain('claude-sonnet-4-6')
    expect(wrapper.text()).toContain('modelMarket.groupOffers:2')

    const offerButton = wrapper.findAll('button').find((button) => button.text().includes('modelMarket.groupOffers'))
    expect(offerButton).toBeTruthy()
    await offerButton!.trigger('click')
    expect(wrapper.text()).toContain('Claude Standard')
    expect(wrapper.text()).toContain('Claude Subscription')
    expect(wrapper.text()).toContain('UTC+08:00')
  })

  it('filters models by search text', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('input').setValue('missing-model')
    expect(wrapper.findAll('article')).toHaveLength(0)
    expect(wrapper.text()).toContain('modelMarket.empty.title')
  })

  it('shows public login and registration calls to action when signed out', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-to="/login"]').exists()).toBe(true)
    expect(wrapper.find('[data-to="/register"]').exists()).toBe(true)
    expect(wrapper.find('[data-to="/available-channels"]').exists()).toBe(false)
  })
})
