import { beforeEach, describe, expect, it, vi } from 'vitest'
import { resolveKiroAuthMethod, useKiroOAuth } from '../useKiroOAuth'

const { exchangeCodeMock, refreshTokenMock } = vi.hoisted(() => ({
  exchangeCodeMock: vi.fn(),
  refreshTokenMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn() })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    kiro: {
      generateAuthUrl: vi.fn(),
      generateIDCAuthUrl: vi.fn(),
      exchangeCode: exchangeCodeMock,
      refreshToken: refreshTokenMock,
      importToken: vi.fn()
    }
  }
}))

describe('useKiroOAuth', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('keeps one session state across the two External IdP stages', async () => {
    exchangeCodeMock.mockResolvedValueOnce({
      auth_url: 'https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize',
      session_id: 'external-session',
      state: 'idp-state'
    })
    const oauth = useKiroOAuth()

    const first = await oauth.exchangeAuthCode({
      code: 'portal-descriptor',
      sessionId: 'external-session',
      state: 'portal-state'
    })

    expect(first).toBeNull()
    expect(oauth.authUrl.value).toContain('microsoftonline.com')
    expect(oauth.sessionId.value).toBe('external-session')
    expect(oauth.state.value).toBe('idp-state')
    expect(oauth.externalIDPStage.value).toBe('idp')

    exchangeCodeMock.mockResolvedValueOnce({
      access_token: 'access-token',
      refresh_token: 'refresh-token',
      auth_method: 'external_idp'
    })
    const final = await oauth.exchangeAuthCode({
      code: 'final-code',
      sessionId: oauth.sessionId.value,
      state: oauth.state.value
    })
    expect(final?.access_token).toBe('access-token')
  })

  it('preserves External IdP refresh metadata', async () => {
    refreshTokenMock.mockResolvedValue({ access_token: 'refreshed' })
    const oauth = useKiroOAuth()

    await oauth.validateRefreshToken({
      refreshToken: 'refresh-token',
      authMethod: 'external_idp',
      clientId: 'client-id',
      issuerUrl: 'https://login.microsoftonline.com/tenant/v2.0',
      tokenEndpoint: 'https://login.microsoftonline.com/tenant/oauth2/v2.0/token',
      scopes: 'openid offline_access'
    })

    expect(refreshTokenMock).toHaveBeenCalledWith(
      expect.objectContaining({
        issuer_url: 'https://login.microsoftonline.com/tenant/v2.0',
        token_endpoint: 'https://login.microsoftonline.com/tenant/oauth2/v2.0/token',
        scopes: 'openid offline_access'
      })
    )
  })
})

describe('resolveKiroAuthMethod', () => {
  it('normalizes legacy aliases', () => {
    expect(resolveKiroAuthMethod({ auth_method: 'builder-id' })).toBe('idc')
    expect(resolveKiroAuthMethod({ auth_method: 'AWSIDC' })).toBe('idc')
    expect(resolveKiroAuthMethod({ auth_method: 'externalidp' })).toBe('external_idp')
  })

  it('infers legacy credentials with External IdP metadata first', () => {
    expect(
      resolveKiroAuthMethod({
        auth_method: 'unknown',
        client_id: 'client-id',
        client_secret: 'client-secret',
        token_endpoint: 'https://login.microsoftonline.com/tenant/oauth2/v2.0/token'
      })
    ).toBe('external_idp')
    expect(
      resolveKiroAuthMethod({
        client_id: 'client-id',
        token_endpoint: 'https://login.microsoftonline.com/tenant/oauth2/v2.0/token'
      })
    ).toBe('external_idp')
    expect(resolveKiroAuthMethod({ provider: 'BuilderId' })).toBe('idc')
  })
})
