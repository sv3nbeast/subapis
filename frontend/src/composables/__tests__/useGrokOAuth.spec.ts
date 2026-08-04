import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => {
      const messages: Record<string, string> = {
        'admin.accounts.oauth.grok.failedToExchangeCode': 'Grok 授权码兑换失败',
        'admin.accounts.oauth.grok.errors.GROK_OAUTH_INVALID_STATE':
          'Grok OAuth state 与当前会话不匹配。请粘贴同一次生成的授权链接返回的回调 URL。'
      }
      return messages[key] ?? key
    }
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    grok: {
      generateAuthUrl: vi.fn(),
      exchangeCode: vi.fn(),
      startDeviceAuthorization: vi.fn(),
      pollDeviceAuthorization: vi.fn(),
      completeDeviceAccount: vi.fn(),
      completeDeviceReauthorization: vi.fn(),
      refreshGrokToken: vi.fn()
    }
  }
}))

import { useGrokOAuth } from '@/composables/useGrokOAuth'
import { adminAPI } from '@/api/admin'

afterEach(() => {
  vi.useRealTimers()
  vi.clearAllMocks()
})

describe('useGrokOAuth device authorization', () => {
  it('waits for the provider interval, keeps tokens server-side, and completes once', async () => {
    vi.useFakeTimers()
    vi.mocked(adminAPI.grok.startDeviceAuthorization).mockResolvedValueOnce({
      session_id: 'device-session',
      user_code: 'ABCD-EFGH',
      verification_uri: 'https://accounts.x.ai/oauth2/device',
      verification_uri_complete: 'https://accounts.x.ai/oauth2/device?user_code=ABCD-EFGH',
      interval_seconds: 5,
      expires_at: Math.floor(Date.now() / 1000) + 1800
    })
    vi.mocked(adminAPI.grok.pollDeviceAuthorization)
      .mockResolvedValueOnce({ status: 'pending', retry_after_seconds: 2 })
      .mockResolvedValueOnce({ status: 'authorized' })
    const onAuthorized = vi.fn().mockResolvedValue(undefined)
    const oauth = useGrokOAuth()

    await expect(
      oauth.startDeviceAuthorization({ proxyId: 7, accountId: 42, onAuthorized })
    ).resolves.toBe(true)
    expect(adminAPI.grok.startDeviceAuthorization).toHaveBeenCalledWith({
      proxy_id: 7,
      account_id: 42
    })
    expect(oauth.deviceUserCode.value).toBe('ABCD-EFGH')
    expect(oauth.deviceVerificationUrl.value).toContain('user_code=ABCD-EFGH')
    expect(oauth.deviceStatus.value).toBe('pending')
    expect(adminAPI.grok.pollDeviceAuthorization).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(5000)
    expect(adminAPI.grok.pollDeviceAuthorization).toHaveBeenCalledTimes(1)
    expect(onAuthorized).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(2000)
    expect(adminAPI.grok.pollDeviceAuthorization).toHaveBeenCalledTimes(2)
    expect(onAuthorized).toHaveBeenCalledOnce()
    expect(onAuthorized).toHaveBeenCalledWith('device-session')
    expect(oauth.deviceStatus.value).toBe('completed')
  })

  it('cancels polling when the operator leaves the device method', async () => {
    vi.useFakeTimers()
    vi.mocked(adminAPI.grok.startDeviceAuthorization).mockResolvedValueOnce({
      session_id: 'device-session',
      user_code: 'ABCD-EFGH',
      verification_uri: 'https://accounts.x.ai/oauth2/device',
      interval_seconds: 5,
      expires_at: Math.floor(Date.now() / 1000) + 1800
    })
    const oauth = useGrokOAuth()

    await oauth.startDeviceAuthorization({ onAuthorized: vi.fn() })
    oauth.cancelDeviceAuthorization()
    await vi.advanceTimersByTimeAsync(10000)

    expect(adminAPI.grok.pollDeviceAuthorization).not.toHaveBeenCalled()
    expect(oauth.deviceStatus.value).toBe('idle')
    expect(oauth.deviceUserCode.value).toBe('')
  })
})

describe('useGrokOAuth.exchangeAuthCode', () => {
  it('shows a state mismatch recovery hint from structured backend errors', async () => {
    vi.mocked(adminAPI.grok.exchangeCode).mockRejectedValueOnce({
      status: 400,
      reason: 'GROK_OAUTH_INVALID_STATE',
      message: 'invalid oauth state'
    })
    const oauth = useGrokOAuth()

    const tokenInfo = await oauth.exchangeAuthCode({
      code: 'code',
      sessionId: 'session-id',
      state: 'wrong-state'
    })

    expect(tokenInfo).toBeNull()
    expect(oauth.error.value).toBe(
      'Grok OAuth state 与当前会话不匹配。请粘贴同一次生成的授权链接返回的回调 URL。'
    )
  })
})

describe('useGrokOAuth.buildCredentials', () => {
  it('persists the backend-selected Grok upstream route', () => {
    const oauth = useGrokOAuth()

    expect(
      oauth.buildCredentials({
        access_token: 'access-token',
        refresh_token: 'refresh-token',
        base_url: 'https://cli-chat-proxy.grok.com/v1'
      })
    ).toEqual({
      access_token: 'access-token',
      refresh_token: 'refresh-token',
      base_url: 'https://cli-chat-proxy.grok.com/v1'
    })
  })
})
