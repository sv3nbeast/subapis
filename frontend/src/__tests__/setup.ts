/**
 * Vitest 测试环境设置
 * 提供全局 mock 和测试工具
 */
import { config } from '@vue/test-utils'
import { beforeEach, vi } from 'vitest'

type StorageState = Record<string, string>

function createStorageMock(initial: StorageState = {}): Storage {
  const state: StorageState = { ...initial }
  return {
    get length() {
      return Object.keys(state).length
    },
    clear() {
      for (const key of Object.keys(state)) {
        delete state[key]
      }
    },
    getItem(key: string) {
      return Object.prototype.hasOwnProperty.call(state, key) ? state[key] : null
    },
    key(index: number) {
      return Object.keys(state)[index] ?? null
    },
    removeItem(key: string) {
      delete state[key]
    },
    setItem(key: string, value: string) {
      state[key] = String(value)
    },
  }
}

function ensureStorage(name: 'localStorage' | 'sessionStorage'): void {
  const storage = globalThis[name]
  if (
    storage &&
    typeof storage.getItem === 'function' &&
    typeof storage.setItem === 'function' &&
    typeof storage.removeItem === 'function' &&
    typeof storage.clear === 'function'
  ) {
    return
  }

  const mock = createStorageMock()
  Object.defineProperty(globalThis, name, {
    configurable: true,
    value: mock,
  })
}

function createMatchMediaMock() {
  return (query: string): MediaQueryList => ({
    matches: /\(min-width:\s*768px\)/.test(query),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(() => false),
  })
}

ensureStorage('localStorage')
ensureStorage('sessionStorage')

// Mock requestIdleCallback (Safari < 15 不支持)
if (typeof globalThis.requestIdleCallback === 'undefined') {
  globalThis.requestIdleCallback = ((callback: IdleRequestCallback) => {
    return window.setTimeout(() => callback({ didTimeout: false, timeRemaining: () => 50 }), 1)
  }) as unknown as typeof requestIdleCallback
}

if (typeof globalThis.cancelIdleCallback === 'undefined') {
  globalThis.cancelIdleCallback = ((id: number) => {
    window.clearTimeout(id)
  }) as unknown as typeof cancelIdleCallback
}

// Mock IntersectionObserver
class MockIntersectionObserver {
  observe = vi.fn()
  disconnect = vi.fn()
  unobserve = vi.fn()
}

globalThis.IntersectionObserver = MockIntersectionObserver as unknown as typeof IntersectionObserver

// Mock ResizeObserver
class MockResizeObserver {
  observe = vi.fn()
  disconnect = vi.fn()
  unobserve = vi.fn()
}

globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver

if (typeof window.matchMedia !== 'function') {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: createMatchMediaMock(),
  })
}

if (!navigator.clipboard) {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: vi.fn().mockResolvedValue(undefined),
      readText: vi.fn().mockResolvedValue(''),
    },
  })
}

beforeEach(() => {
  ensureStorage('localStorage')
  ensureStorage('sessionStorage')

  if (typeof window.matchMedia !== 'function') {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: createMatchMediaMock(),
    })
  }
})

// Vue Test Utils 全局配置
config.global.stubs = {
  // 可以在这里添加全局 stub
}

// 设置全局测试超时
vi.setConfig({ testTimeout: 10000 })
