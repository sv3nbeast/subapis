import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AppLayout from '../AppLayout.vue'

const { setReplayCallback } = vi.hoisted(() => ({
  setReplayCallback: vi.fn(),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ sidebarCollapsed: false, mobileOpen: false }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { id: 7, role: 'user' } }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ setReplayCallback }),
}))

vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({ replayTour: vi.fn() }),
}))

describe('AppLayout density', () => {
  beforeEach(() => {
    document.documentElement.classList.remove('app-density-compact')
    document.documentElement.classList.remove('ui-v2-active')
    localStorage.clear()
    window.history.replaceState({}, '', '/')
    setReplayCallback.mockClear()
  })

  const mountLayout = () => mount(AppLayout, {
    slots: {
      default: '<div data-testid="business-content">business content</div>',
    },
    global: {
      stubs: {
        AppHeader: {
          props: ['uiVersion'],
          emits: ['useLegacyUi'],
          template: '<button data-testid="legacy-switch" @click="$emit(\'useLegacyUi\')">{{ uiVersion }}</button>',
        },
        AppSidebar: true,
        AppMobileDock: true,
      },
    },
  })

  it('keeps compact density scoped to the app layout lifecycle', async () => {
    const wrapper = mountLayout()

    expect(document.documentElement.classList.contains('app-density-compact')).toBe(true)

    wrapper.unmount()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(document.documentElement.classList.contains('app-density-compact')).toBe(false)
  })

  it('keeps compact density across sequential route layout replacement', async () => {
    const outgoingLayout = mountLayout()

    outgoingLayout.unmount()

    expect(document.documentElement.classList.contains('app-density-compact')).toBe(true)

    const incomingLayout = mountLayout()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(document.documentElement.classList.contains('app-density-compact')).toBe(true)

    incomingLayout.unmount()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(document.documentElement.classList.contains('app-density-compact')).toBe(false)
  })

  it('keeps root metrics while an interrupted route update leaves the shell rendered', async () => {
    window.history.replaceState({}, '', '/dashboard?ui=v2')
    const wrapper = mountLayout()
    const retainedShell = document.createElement('div')
    retainedShell.className = 'app-shell ui-v2'
    document.body.appendChild(retainedShell)

    wrapper.unmount()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(document.documentElement.classList.contains('app-density-compact')).toBe(true)
    expect(document.documentElement.classList.contains('ui-v2-active')).toBe(true)

    retainedShell.remove()
  })

  it('keeps the legacy shell and original slot content by default', () => {
    const wrapper = mountLayout()

    expect(wrapper.attributes('data-ui-version')).toBe('legacy')
    expect(wrapper.classes()).not.toContain('ui-v2')
    expect(wrapper.get('[data-testid="business-content"]').text()).toBe('business content')

    wrapper.unmount()
  })

  it('enables v2 only for an explicit preview and can return immediately', async () => {
    window.history.replaceState({}, '', '/dashboard?ui=v2')
    const wrapper = mountLayout()

    expect(wrapper.attributes('data-ui-version')).toBe('v2')
    expect(wrapper.classes()).toContain('ui-v2')
    expect(document.documentElement.classList.contains('ui-v2-active')).toBe(true)
    expect(localStorage.getItem('sub2api:ui-version:7')).toBe('v2')

    await wrapper.get('[data-testid="legacy-switch"]').trigger('click')

    expect(wrapper.attributes('data-ui-version')).toBe('legacy')
    expect(wrapper.classes()).not.toContain('ui-v2')
    expect(document.documentElement.classList.contains('ui-v2-active')).toBe(false)
    expect(localStorage.getItem('sub2api:ui-version:7')).toBe('legacy')
    expect(window.location.search).toBe('')

    wrapper.unmount()
  })
})
