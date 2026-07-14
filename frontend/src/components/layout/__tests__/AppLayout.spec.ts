import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AppLayout from '../AppLayout.vue'

const { setReplayCallback } = vi.hoisted(() => ({
  setReplayCallback: vi.fn(),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ sidebarCollapsed: false }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { role: 'user' } }),
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
    setReplayCallback.mockClear()
  })

  it('scopes compact density to the app layout lifecycle', () => {
    const wrapper = mount(AppLayout, {
      global: {
        stubs: {
          AppHeader: true,
          AppSidebar: true,
        },
      },
    })

    expect(document.documentElement.classList.contains('app-density-compact')).toBe(true)

    wrapper.unmount()

    expect(document.documentElement.classList.contains('app-density-compact')).toBe(false)
  })
})
