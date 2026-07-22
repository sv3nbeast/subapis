import { beforeEach, describe, expect, it } from 'vitest'
import { shallowMount } from '@vue/test-utils'
import PublicLayout from '../PublicLayout.vue'

describe('PublicLayout', () => {
  beforeEach(() => {
    localStorage.clear()
    window.history.replaceState({}, '', '/home')
  })

  it('keeps the legacy slot unwrapped by default', () => {
    const wrapper = shallowMount(PublicLayout, {
      slots: { default: '<div class="legacy-probe">Legacy</div>' },
      global: {
        stubs: { PublicHeader: true, PublicFooter: true },
      },
    })

    expect(wrapper.find('.public-ui-v2').exists()).toBe(false)
    expect(wrapper.find('.legacy-probe').exists()).toBe(true)
  })

  it('renders independent public chrome after an explicit preview request', () => {
    window.history.replaceState({}, '', '/home?public_ui=v2')
    const wrapper = shallowMount(PublicLayout, {
      slots: { default: '<div class="v2-probe">V2</div>' },
      global: {
        stubs: { PublicHeader: true, PublicFooter: true },
      },
    })

    expect(wrapper.find('.public-ui-v2').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'PublicHeader' }).exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'PublicFooter' }).exists()).toBe(true)
    expect(wrapper.find('.v2-probe').exists()).toBe(true)
    expect(window.location.search).toBe('')
  })

  it('supports status-only shells without public chrome', () => {
    window.history.replaceState({}, '', '/auth/callback?public_ui=v2')
    const wrapper = shallowMount(PublicLayout, {
      props: { showChrome: false, showFooter: false },
      slots: { default: '<div>Callback</div>' },
      global: {
        stubs: { PublicHeader: true, PublicFooter: true },
      },
    })

    expect(wrapper.find('.public-ui-v2').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'PublicHeader' }).exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'PublicFooter' }).exists()).toBe(false)
  })
})
