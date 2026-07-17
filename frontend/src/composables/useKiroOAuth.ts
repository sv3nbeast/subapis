import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { KiroTokenInfo } from '@/api/admin/kiro'

export type KiroAuthMethod = 'social' | 'idc' | 'external_idp'

export function resolveKiroAuthMethod(credentials: Record<string, unknown>): KiroAuthMethod {
  const raw = String(credentials.auth_method || '')
    .trim()
    .toLowerCase()
  if (
    ['idc', 'iam_identity_center', 'builder-id', 'builder_id', 'builderid', 'awsidc'].includes(raw)
  ) {
    return 'idc'
  }
  if (['external_idp', 'external-idp', 'externalidp'].includes(raw)) {
    return 'external_idp'
  }
  if (raw === 'social') return 'social'

  const clientId = String(credentials.client_id || '').trim()
  const provider = String(credentials.provider || '')
    .trim()
    .toLowerCase()
  if (
    clientId &&
    (String(credentials.token_endpoint || '').trim() || String(credentials.issuer_url || '').trim())
  ) {
    return 'external_idp'
  }
  if (provider === 'externalidp') return 'external_idp'
  if (clientId && String(credentials.client_secret || '').trim()) return 'idc'
  if (['builderid', 'enterprise', 'aws'].includes(provider)) return 'idc'
  return 'social'
}

export function useKiroOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()

  const authUrl = ref('')
  const sessionId = ref('')
  const state = ref('')
  const loading = ref(false)
  const error = ref('')
  const externalIDPStage = ref<'portal' | 'idp'>('portal')

  const resetState = () => {
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    loading.value = false
    error.value = ''
    externalIDPStage.value = 'portal'
  }

  const errorMessage = (err: any) =>
    err?.response?.data?.message ||
    err?.response?.data?.detail ||
    err?.message ||
    err?.reason ||
    t('admin.accounts.oauth.authFailed')

  const generateAuthUrl = async (
    proxyId: number | null | undefined,
    provider: 'Google' | 'Github' | 'ExternalIdp' = 'Google'
  ): Promise<boolean> => {
    loading.value = true
    error.value = ''
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    externalIDPStage.value = 'portal'

    try {
      const response = await adminAPI.kiro.generateAuthUrl({
        proxy_id: proxyId || undefined,
        provider
      })
      authUrl.value = response.auth_url
      sessionId.value = response.session_id
      state.value = response.state
      return true
    } catch (err: any) {
      error.value = errorMessage(err)
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  const generateIDCAuthUrl = async (params: {
    proxyId?: number | null
    startUrl?: string
    region?: string
  }): Promise<boolean> => {
    loading.value = true
    error.value = ''
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    externalIDPStage.value = 'portal'

    try {
      const response = await adminAPI.kiro.generateIDCAuthUrl({
        proxy_id: params.proxyId || undefined,
        start_url: params.startUrl,
        region: params.region
      })
      authUrl.value = response.auth_url
      sessionId.value = response.session_id
      state.value = response.state
      return true
    } catch (err: any) {
      error.value = errorMessage(err)
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  const exchangeAuthCode = async (params: {
    code: string
    sessionId: string
    state: string
    callbackPath?: string
    loginOption?: string
    proxyId?: number | null
  }): Promise<KiroTokenInfo | null> => {
    loading.value = true
    error.value = ''
    try {
      const response = await adminAPI.kiro.exchangeCode({
        session_id: params.sessionId,
        state: params.state,
        code: params.code.trim(),
        callback_path: params.callbackPath,
        login_option: params.loginOption,
        proxy_id: params.proxyId || undefined
      })
      if (response.auth_url && response.session_id && response.state && !response.access_token) {
        authUrl.value = response.auth_url
        sessionId.value = response.session_id
        state.value = response.state
        externalIDPStage.value = 'idp'
        return null
      }
      return response
    } catch (err: any) {
      error.value = errorMessage(err)
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const validateRefreshToken = async (payload: {
    refreshToken: string
    authMethod?: string
    provider?: string
    clientId?: string
    clientSecret?: string
    startUrl?: string
    region?: string
    profileArn?: string
    issuerUrl?: string
    tokenEndpoint?: string
    scopes?: string
    proxyId?: number | null
  }): Promise<KiroTokenInfo | null> => {
    loading.value = true
    error.value = ''
    try {
      return await adminAPI.kiro.refreshToken({
        refresh_token: payload.refreshToken.trim(),
        auth_method: payload.authMethod,
        provider: payload.provider,
        client_id: payload.clientId,
        client_secret: payload.clientSecret,
        start_url: payload.startUrl,
        region: payload.region,
        profile_arn: payload.profileArn,
        issuer_url: payload.issuerUrl,
        token_endpoint: payload.tokenEndpoint,
        scopes: payload.scopes,
        proxy_id: payload.proxyId || undefined
      })
    } catch (err: any) {
      error.value = errorMessage(err)
      return null
    } finally {
      loading.value = false
    }
  }

  const importToken = async (
    tokenJSON: string,
    deviceRegistrationJSON?: string
  ): Promise<KiroTokenInfo | null> => {
    loading.value = true
    error.value = ''
    try {
      return await adminAPI.kiro.importToken({
        token_json: tokenJSON,
        device_registration_json: deviceRegistrationJSON
      })
    } catch (err: any) {
      error.value = errorMessage(err)
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const buildCredentials = (tokenInfo: KiroTokenInfo): Record<string, unknown> => ({
    access_token: tokenInfo.access_token,
    refresh_token: tokenInfo.refresh_token,
    profile_arn: tokenInfo.profile_arn,
    expires_at: tokenInfo.expires_at,
    auth_method: tokenInfo.auth_method,
    provider: tokenInfo.provider,
    client_id: tokenInfo.client_id,
    client_secret: tokenInfo.client_secret,
    client_id_hash: tokenInfo.client_id_hash,
    email: tokenInfo.email,
    start_url: tokenInfo.start_url,
    region: tokenInfo.region,
    issuer_url: tokenInfo.issuer_url,
    token_endpoint: tokenInfo.token_endpoint,
    scopes: tokenInfo.scopes
  })

  return {
    authUrl,
    sessionId,
    state,
    loading,
    error,
    externalIDPStage,
    resetState,
    generateAuthUrl,
    generateIDCAuthUrl,
    exchangeAuthCode,
    validateRefreshToken,
    importToken,
    buildCredentials
  }
}
