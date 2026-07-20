import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AppMobileDock from '../AppMobileDock.vue'

const { authState, routeState } = vi.hoisted(() => ({
  authState: { isAdmin: false },
  routeState: { path: '/keys' },
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('vue-router', () => ({
  useRoute: () => routeState,
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => ({
      'nav.mainNavigation': 'Main navigation',
      'nav.dashboard': 'Dashboard',
      'nav.apiKeys': 'API keys',
      'nav.accounts': 'Accounts',
      'nav.usage': 'Usage',
    }[key] ?? key),
  }),
}))

const mountDock = () => mount(AppMobileDock, {
  global: {
    stubs: {
      Icon: true,
      RouterLink: {
        props: ['to'],
        template: '<a :href="to"><slot /></a>',
      },
    },
  },
})

describe('AppMobileDock', () => {
  beforeEach(() => {
    authState.isAdmin = false
    routeState.path = '/keys'
  })

  it('keeps the three user shortcuts stable and marks the active route', () => {
    const wrapper = mountDock()

    expect(wrapper.findAll('a').map(link => link.attributes('href'))).toEqual([
      '/dashboard',
      '/keys',
      '/usage',
    ])
    expect(wrapper.get('a[href="/keys"]').classes()).toContain('ui-v2-mobile-dock-item-active')
    expect(wrapper.get('nav').attributes('aria-label')).toBe('Main navigation')
  })

  it('uses the corresponding admin shortcuts', () => {
    authState.isAdmin = true
    routeState.path = '/admin/accounts'
    const wrapper = mountDock()

    expect(wrapper.findAll('a').map(link => link.attributes('href'))).toEqual([
      '/admin/dashboard',
      '/admin/accounts',
      '/admin/usage',
    ])
  })

  it('captures pointer input and always clears the press presentation', async () => {
    const wrapper = mountDock()
    const link = wrapper.get('a[href="/keys"]')
    const element = link.element as HTMLElement
    element.setPointerCapture = vi.fn()
    element.hasPointerCapture = vi.fn(() => true)
    element.releasePointerCapture = vi.fn()

    await link.trigger('pointerdown', { pointerId: 4, pointerType: 'touch', button: 0 })
    expect(element.style.getPropertyValue('--dock-press')).toBe('0.96')
    expect(element.setPointerCapture).toHaveBeenCalledWith(4)

    await link.trigger('pointerup', { pointerId: 4, pointerType: 'touch', button: 0 })
    expect(element.style.getPropertyValue('--dock-press')).toBe('')
    expect(element.releasePointerCapture).toHaveBeenCalledWith(4)
  })
})
