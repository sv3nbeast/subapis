import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
const put = vi.fn()

vi.mock('@/api/client', () => ({
  apiClient: {
    get,
    put,
  },
}))

describe('admin settings runtime normalization', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
  })

  it('normalizes legacy null arrays from getSettings responses', async () => {
    get.mockResolvedValue({
      data: {
        registration_email_suffix_whitelist: null,
        registration_email_suffix_blacklist: null,
        table_page_size_options: null,
        custom_menu_items: null,
        custom_endpoints: null,
        default_subscriptions: null,
        payment_enabled_types: null,
        account_quota_notify_emails: null,
      },
    })

    const { getSettings } = await import('@/api/admin/settings')
    const settings = await getSettings()

    expect(settings.registration_email_suffix_whitelist).toEqual([])
    expect(settings.registration_email_suffix_blacklist).toEqual([])
    expect(settings.table_page_size_options).toEqual([10, 20, 50, 100])
    expect(settings.custom_menu_items).toEqual([])
    expect(settings.custom_endpoints).toEqual([])
    expect(settings.default_subscriptions).toEqual([])
    expect(settings.payment_enabled_types).toEqual([])
    expect(settings.account_quota_notify_emails).toEqual([])
  })

  it('normalizes legacy null arrays from updateSettings responses', async () => {
    put.mockResolvedValue({
      data: {
        registration_email_suffix_whitelist: null,
        registration_email_suffix_blacklist: null,
        table_page_size_options: [],
        custom_menu_items: null,
        custom_endpoints: null,
        default_subscriptions: null,
        payment_enabled_types: null,
        account_quota_notify_emails: null,
      },
    })

    const { updateSettings } = await import('@/api/admin/settings')
    const settings = await updateSettings({})

    expect(settings.registration_email_suffix_whitelist).toEqual([])
    expect(settings.registration_email_suffix_blacklist).toEqual([])
    expect(settings.table_page_size_options).toEqual([10, 20, 50, 100])
    expect(settings.custom_menu_items).toEqual([])
    expect(settings.custom_endpoints).toEqual([])
    expect(settings.default_subscriptions).toEqual([])
    expect(settings.payment_enabled_types).toEqual([])
    expect(settings.account_quota_notify_emails).toEqual([])
  })
})
