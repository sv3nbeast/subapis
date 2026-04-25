import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAdminSettingsStore } from '../adminSettings'

const getSettings = vi.hoisted(() => vi.fn())
const getPaymentConfig = vi.hoisted(() => vi.fn())

vi.mock('@/api', () => ({
  adminAPI: {
    settings: {
      getSettings,
    },
    payment: {
      getConfig: getPaymentConfig,
    },
  },
}))

describe('adminSettings store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    getSettings.mockReset()
    getPaymentConfig.mockReset()
  })

  it('keeps admin settings loaded when legacy backend lacks payment config endpoint', async () => {
    getSettings.mockResolvedValue({
      ops_monitoring_enabled: false,
      ops_realtime_monitoring_enabled: true,
      ops_query_mode_default: 'raw',
      custom_menu_items: [],
      payment_enabled: true,
    })
    getPaymentConfig.mockRejectedValue({ status: 404 })

    const store = useAdminSettingsStore()
    await store.fetch(true)

    expect(store.loaded).toBe(true)
    expect(store.opsMonitoringEnabled).toBe(false)
    expect(store.opsRealtimeMonitoringEnabled).toBe(true)
    expect(store.opsQueryModeDefault).toBe('raw')
    expect(store.paymentEnabled).toBe(true)
    expect(localStorage.getItem('payment_enabled_cached')).toBe('true')
  })
})
