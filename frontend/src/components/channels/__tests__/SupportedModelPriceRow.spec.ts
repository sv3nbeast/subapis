import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import SupportedModelPriceRow from '../SupportedModelPriceRow.vue'
import type { UserSupportedModel, UserSupportedModelPricing } from '@/api/channels'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const basePricing: UserSupportedModelPricing = {
  billing_mode: 'token',
  input_price: 4e-6,
  output_price: 20e-6,
  cache_write_price: 5e-6,
  cache_write_5m_price: null,
  cache_write_1h_price: null,
  cache_read_price: 0.4e-6,
  image_input_price: null,
  image_output_price: null,
  per_request_price: null,
  intervals: [],
}

/** 后端按实收口径折出的 Sol 两档：渠道基础价 + 目录 272K 翻倍档（输入/缓存 2x、输出 1.5x）。 */
const solLadder: UserSupportedModelPricing = {
  ...basePricing,
  intervals: [
    // 故意乱序，组件须按 min_tokens 升序展示
    {
      min_tokens: 272000, max_tokens: null, tier_label: '>272K',
      input_price: 8e-6, output_price: 30e-6, cache_write_price: 10e-6, cache_write_5m_price: null, cache_write_1h_price: null,
      cache_read_price: 0.8e-6, per_request_price: null,
    },
    {
      min_tokens: 0, max_tokens: 272000, tier_label: '',
      input_price: 4e-6, output_price: 20e-6, cache_write_price: 5e-6, cache_write_5m_price: null, cache_write_1h_price: null,
      cache_read_price: 0.4e-6, per_request_price: null,
    },
  ],
}

function mountRow(pricing: UserSupportedModelPricing | null, longContextEnabled?: boolean) {
  const model: UserSupportedModel = { name: 'gpt-5.6-sol', platform: 'openai', pricing }
  return mount(SupportedModelPriceRow, {
    props: { model, noPricingLabel: 'no-pricing', ...(longContextEnabled === undefined ? {} : { longContextEnabled }) },
  })
}

describe('SupportedModelPriceRow long-context tiers', () => {
  it('lists every tier with its label in the input/output/cache columns when the group applies the ladder', () => {
    const wrapper = mountRow(solLadder, true)

    expect(wrapper.find('[data-testid="long-context-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="long-context-disabled-hint"]').exists()).toBe(false)

    const input = wrapper.find('[data-testid="price-input"]').text()
    expect(input.indexOf('≤272K')).toBeLessThan(input.indexOf('>272K'))
    expect(input).toContain('≤272K$4')
    expect(input).toContain('>272K$8')
    expect(wrapper.find('[data-testid="price-output"]').text()).toContain('>272K$30')
    expect(wrapper.find('[data-testid="price-cacheRead"]').text()).toContain('>272K$0.8')
    expect(wrapper.find('[data-testid="price-cacheWrite"]').text()).toContain('>272K$10')
  })

  it('treats a missing group flag as enabled (older backends)', () => {
    const wrapper = mountRow(solLadder)
    expect(wrapper.find('[data-testid="price-input"]').text()).toContain('>272K$8')
  })

  it('shows only the base tier plus a disabled hint when the group has the ladder switched off', () => {
    const wrapper = mountRow(solLadder, false)

    expect(wrapper.find('[data-testid="long-context-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="long-context-disabled-hint"]').text()).toBe(
      'availableChannels.groupCards.longContextDisabledHint',
    )
    const input = wrapper.find('[data-testid="price-input"]').text()
    expect(input).toBe('$4')
    expect(input).not.toContain('272K')
    expect(wrapper.find('[data-testid="price-output"]').text()).toBe('$20')
  })

  it('keeps the flat layout and no badge for models without a ladder', () => {
    const wrapper = mountRow(basePricing, true)

    expect(wrapper.find('[data-testid="long-context-badge"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="price-input"]').text()).toBe('$4')
    expect(wrapper.find('[data-testid="price-cacheWrite"]').text()).toBe('$5')
    expect(wrapper.text()).not.toContain('intervalHint')
  })

  it('falls back to the tier-count hint for non-token tiers and renders no prices without pricing', () => {
    const perRequest = mountRow({
      ...basePricing,
      billing_mode: 'per_request',
      per_request_price: 0.04,
      intervals: [
        { min_tokens: 0, max_tokens: null, tier_label: '1K', input_price: null, output_price: null, cache_write_price: null, cache_write_5m_price: null, cache_write_1h_price: null, cache_read_price: null, per_request_price: 0.04 },
      ],
    })
    expect(perRequest.text()).toContain('availableChannels.groupCards.intervalHint')
    expect(perRequest.find('[data-testid="long-context-badge"]').exists()).toBe(false)

    const empty = mountRow(null)
    expect(empty.text()).toContain('no-pricing')
    expect(empty.find('[data-testid="price-input"]').text()).toBe('-')
  })
})
