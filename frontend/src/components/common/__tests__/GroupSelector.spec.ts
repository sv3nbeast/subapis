import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

import GroupSelector from '../GroupSelector.vue'
import type { AdminGroup, GroupPlatform } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'common.selectedCount') return `selected ${params?.count ?? 0}`
        if (key === 'admin.groups.rateAndAccounts') return 'rate and accounts'
        return key
      }
    })
  }
})

const GroupBadgeStub = defineComponent({
  name: 'GroupBadge',
  props: {
    name: String,
    platform: String
  },
  template: '<span data-testid="group-badge" :data-platform="platform">{{ name }}</span>'
})

const IconStub = defineComponent({
  name: 'Icon',
  template: '<span />'
})

function group(id: number, name: string, platform: GroupPlatform): AdminGroup {
  return {
    id,
    name,
    description: null,
    platform,
    rate_multiplier: 1,
    is_exclusive: false,
    status: 'active',
    subscription_type: 'standard',
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    allow_image_generation: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    image_price_1k: null,
    image_price_2k: null,
    image_price_4k: null,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    require_oauth_only: false,
    require_privacy_set: false,
    kiro_cache_emulation_enabled: false,
    kiro_auto_sticky_enabled: false,
    kiro_sticky_session_ttl_seconds: 0,
    kiro_cache_emulation_ratio: 0,
    created_at: '2026-06-11T00:00:00Z',
    updated_at: '2026-06-11T00:00:00Z',
    model_routing: null,
    model_routing_enabled: false,
    mcp_xml_inject: false,
    sort_order: id
  }
}

const groups = [
  group(1, 'Claude AWS', 'anthropic'),
  group(2, 'Kiro Native', 'kiro'),
  group(3, 'Droid Native', 'droid'),
  group(4, 'Antigravity', 'antigravity'),
  group(5, 'Gemini', 'gemini'),
  group(6, 'OpenAI', 'openai')
]

function mountSelector(platform: GroupPlatform, mixedScheduling: boolean) {
  return mount(GroupSelector, {
    props: {
      modelValue: [],
      groups,
      platform,
      mixedScheduling,
      searchable: false
    },
    global: {
      stubs: {
        GroupBadge: GroupBadgeStub,
        Icon: IconStub
      }
    }
  })
}

describe('GroupSelector mixed scheduling filtering', () => {
  it('shows Anthropic groups for Kiro when mixed scheduling is enabled', () => {
    const wrapper = mountSelector('kiro', true)

    expect(wrapper.text()).toContain('Claude AWS')
    expect(wrapper.text()).toContain('Kiro Native')
    expect(wrapper.text()).not.toContain('Droid Native')
    expect(wrapper.text()).not.toContain('Gemini')
    expect(wrapper.text()).not.toContain('OpenAI')
  })

  it('shows Anthropic groups for Droid when mixed scheduling is enabled', () => {
    const wrapper = mountSelector('droid', true)

    expect(wrapper.text()).toContain('Claude AWS')
    expect(wrapper.text()).toContain('Droid Native')
    expect(wrapper.text()).not.toContain('Kiro Native')
    expect(wrapper.text()).not.toContain('Gemini')
    expect(wrapper.text()).not.toContain('OpenAI')
  })

  it('preserves Antigravity mixed scheduling access to Anthropic and Gemini groups', () => {
    const wrapper = mountSelector('antigravity', true)

    expect(wrapper.text()).toContain('Claude AWS')
    expect(wrapper.text()).toContain('Antigravity')
    expect(wrapper.text()).toContain('Gemini')
    expect(wrapper.text()).not.toContain('Kiro Native')
    expect(wrapper.text()).not.toContain('Droid Native')
    expect(wrapper.text()).not.toContain('OpenAI')
  })

  it('keeps Kiro limited to Kiro groups when mixed scheduling is disabled', () => {
    const wrapper = mountSelector('kiro', false)

    expect(wrapper.text()).toContain('Kiro Native')
    expect(wrapper.text()).not.toContain('Claude AWS')
    expect(wrapper.text()).not.toContain('Droid Native')
  })
})
