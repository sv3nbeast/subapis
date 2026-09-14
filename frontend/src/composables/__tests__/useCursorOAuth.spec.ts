import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useCursorOAuth } from '../useCursorOAuth'

const { generateAuthUrlMock, pollMock, showErrorMock } = vi.hoisted(() => ({
  generateAuthUrlMock: vi.fn(),
  pollMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: showErrorMock })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    cursor: {
      generateAuthUrl: generateAuthUrlMock,
      pollAuthorization: pollMock
    }
  }
}))

describe('useCursorOAuth', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it('exposes the authorization link and session after generating', async () => {
    generateAuthUrlMock.mockResolvedValueOnce({
      auth_url: 'https://cursor.com/loginDeepControl?challenge=c&uuid=u',
      session_id: 'sess-1'
    })

    const oauth = useCursorOAuth()
    await expect(oauth.generateAuthUrl(7)).resolves.toBe(true)

    expect(generateAuthUrlMock).toHaveBeenCalledWith({ proxy_id: 7 })
    expect(oauth.authUrl.value).toContain('loginDeepControl')
    expect(oauth.sessionId.value).toBe('sess-1')
    expect(oauth.loading.value).toBe(false)
  })

  it('keeps polling while the user has not finished signing in', async () => {
    vi.useFakeTimers()
    generateAuthUrlMock.mockResolvedValueOnce({ auth_url: 'https://cursor.com/x', session_id: 'sess-2' })
    pollMock
      .mockResolvedValueOnce({ pending: true })
      .mockResolvedValueOnce({ pending: true })
      .mockResolvedValueOnce({ access_token: 'jwt', machine_id: 'a', mac_machine_id: 'b' })

    const oauth = useCursorOAuth()
    await oauth.generateAuthUrl(null)

    const authorization = oauth.awaitAuthorization(null)
    await vi.advanceTimersByTimeAsync(5000)
    const tokenInfo = await authorization

    expect(pollMock).toHaveBeenCalledTimes(3)
    expect(tokenInfo?.access_token).toBe('jwt')
    expect(oauth.polling.value).toBe(false)
  })

  it('stops polling once cancelled so a closed dialog leaves nothing running', async () => {
    vi.useFakeTimers()
    generateAuthUrlMock.mockResolvedValueOnce({ auth_url: 'https://cursor.com/x', session_id: 'sess-3' })
    pollMock.mockResolvedValue({ pending: true })

    const oauth = useCursorOAuth()
    await oauth.generateAuthUrl(null)

    const authorization = oauth.awaitAuthorization(null)
    await vi.advanceTimersByTimeAsync(2000)
    oauth.cancelPolling()
    await vi.advanceTimersByTimeAsync(10000)

    await expect(authorization).resolves.toBeNull()
    const callsAfterCancel = pollMock.mock.calls.length
    await vi.advanceTimersByTimeAsync(10000)
    expect(pollMock.mock.calls.length).toBe(callsAfterCancel)
  })

  it('surfaces poll failures instead of hanging', async () => {
    generateAuthUrlMock.mockResolvedValueOnce({ auth_url: 'https://cursor.com/x', session_id: 'sess-4' })
    pollMock.mockRejectedValueOnce(new Error('upstream exploded'))

    const oauth = useCursorOAuth()
    await oauth.generateAuthUrl(null)

    await expect(oauth.awaitAuthorization(null)).resolves.toBeNull()
    expect(oauth.error.value).toBe('upstream exploded')
    expect(showErrorMock).toHaveBeenCalled()
    expect(oauth.polling.value).toBe(false)
  })

  it('refuses to poll without a session', async () => {
    const oauth = useCursorOAuth()
    await expect(oauth.awaitAuthorization(null)).resolves.toBeNull()
    expect(pollMock).not.toHaveBeenCalled()
    expect(oauth.error.value).toBe('admin.accounts.cursor.oauth.sessionMissing')
  })

  it('builds credentials carrying the generated telemetry ids', () => {
    const oauth = useCursorOAuth()
    const credentials = oauth.buildCredentials({
      access_token: 'jwt',
      refresh_token: 'refresh',
      machine_id: 'a'.repeat(64),
      mac_machine_id: 'b'.repeat(64),
      expires_at: '2026-09-15T00:00:00Z',
      email: 'dev@example.com'
    })

    expect(credentials).toMatchObject({
      access_token: 'jwt',
      refresh_token: 'refresh',
      machine_id: 'a'.repeat(64),
      mac_machine_id: 'b'.repeat(64),
      expires_at: '2026-09-15T00:00:00Z',
      email: 'dev@example.com'
    })
    // 可选字段缺失时不写入空值。
    expect(oauth.buildCredentials({ access_token: 'jwt' })).not.toHaveProperty('refresh_token')
  })

  it('resetState clears the link and stops an in-flight poll', async () => {
    vi.useFakeTimers()
    generateAuthUrlMock.mockResolvedValueOnce({ auth_url: 'https://cursor.com/x', session_id: 'sess-5' })
    pollMock.mockResolvedValue({ pending: true })

    const oauth = useCursorOAuth()
    await oauth.generateAuthUrl(null)
    const authorization = oauth.awaitAuthorization(null)
    await vi.advanceTimersByTimeAsync(2000)

    oauth.resetState()
    await vi.advanceTimersByTimeAsync(10000)

    await expect(authorization).resolves.toBeNull()
    expect(oauth.authUrl.value).toBe('')
    expect(oauth.sessionId.value).toBe('')
    expect(oauth.polling.value).toBe(false)
  })
})
