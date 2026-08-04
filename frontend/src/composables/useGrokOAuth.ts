import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { GrokTokenInfo } from '@/api/admin/grok'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'

export function useGrokOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()

  const authUrl = ref('')
  const sessionId = ref('')
  const state = ref('')
  const loading = ref(false)
  const error = ref('')
  const deviceUserCode = ref('')
  const deviceVerificationUrl = ref('')
  const deviceStatus = ref<'idle' | 'pending' | 'authorized' | 'completing' | 'completed' | 'failed'>('idle')
  const deviceExpiresAt = ref(0)
  let deviceRun = 0
  let devicePollTimer: ReturnType<typeof setTimeout> | null = null

  const stopDevicePolling = () => {
    deviceRun += 1
    if (devicePollTimer) {
      clearTimeout(devicePollTimer)
      devicePollTimer = null
    }
  }

  const resetState = () => {
    stopDevicePolling()
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    loading.value = false
    error.value = ''
    deviceUserCode.value = ''
    deviceVerificationUrl.value = ''
    deviceStatus.value = 'idle'
    deviceExpiresAt.value = 0
  }

  const cancelDeviceAuthorization = () => {
    stopDevicePolling()
    deviceUserCode.value = ''
    deviceVerificationUrl.value = ''
    deviceStatus.value = 'idle'
    deviceExpiresAt.value = 0
  }

  const startDeviceAuthorization = async (params: {
    proxyId?: number | null
    accountId?: number
    onAuthorized: (sessionId: string) => Promise<void>
  }): Promise<boolean> => {
    stopDevicePolling()
    const run = deviceRun
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    deviceUserCode.value = ''
    deviceVerificationUrl.value = ''
    deviceStatus.value = 'idle'
    deviceExpiresAt.value = 0
    error.value = ''
    loading.value = true

    try {
      const payload: { proxy_id?: number; account_id?: number } = {}
      if (params.proxyId) payload.proxy_id = params.proxyId
      if (params.accountId) payload.account_id = params.accountId
      const result = await adminAPI.grok.startDeviceAuthorization(payload)
      if (run !== deviceRun) return false
      sessionId.value = result.session_id
      deviceUserCode.value = result.user_code
      deviceVerificationUrl.value = result.verification_uri_complete || result.verification_uri
      deviceExpiresAt.value = result.expires_at
      deviceStatus.value = 'pending'
      scheduleDevicePoll(run, params.onAuthorized, result.interval_seconds)
      return true
    } catch (err: any) {
      if (run !== deviceRun) return false
      deviceStatus.value = 'failed'
      error.value = extractApiErrorMessage(err, t('admin.accounts.oauth.grok.deviceStartFailed'))
      appStore.showError(error.value)
      return false
    } finally {
      if (run === deviceRun) loading.value = false
    }
  }

  const scheduleDevicePoll = (
    run: number,
    onAuthorized: (sessionId: string) => Promise<void>,
    delaySeconds: number
  ) => {
    if (run !== deviceRun || !sessionId.value) return
    const delay = Math.max(1, Number.isFinite(delaySeconds) ? delaySeconds : 5) * 1000
    devicePollTimer = setTimeout(() => {
      devicePollTimer = null
      void pollDeviceAuthorization(run, onAuthorized)
    }, delay)
  }

  const pollDeviceAuthorization = async (
    run: number,
    onAuthorized: (sessionId: string) => Promise<void>
  ) => {
    const currentSessionId = sessionId.value
    if (run !== deviceRun || !currentSessionId) return
    if (deviceExpiresAt.value > 0 && Date.now() >= deviceExpiresAt.value * 1000) {
      deviceStatus.value = 'failed'
      error.value = t('admin.accounts.oauth.grok.deviceExpired')
      return
    }
    try {
      const result = await adminAPI.grok.pollDeviceAuthorization(currentSessionId)
      if (run !== deviceRun) return
      if (result.status !== 'authorized') {
        scheduleDevicePoll(run, onAuthorized, result.retry_after_seconds || 5)
        return
      }
      deviceStatus.value = 'authorized'
      loading.value = true
      deviceStatus.value = 'completing'
      await onAuthorized(currentSessionId)
      if (run !== deviceRun) return
      deviceStatus.value = 'completed'
    } catch (err: any) {
      if (run !== deviceRun) return
      const status = Number(err?.response?.status || 0)
      if (status === 0 || status === 429 || status >= 500) {
        deviceStatus.value = 'pending'
        scheduleDevicePoll(run, onAuthorized, 5)
      } else {
        deviceStatus.value = 'failed'
        error.value = extractApiErrorMessage(err, t('admin.accounts.oauth.grok.devicePollFailed'))
        appStore.showError(error.value)
      }
    } finally {
      if (run === deviceRun) loading.value = false
    }
  }

  const generateAuthUrl = async (proxyId: number | null | undefined): Promise<boolean> => {
    loading.value = true
    authUrl.value = ''
    sessionId.value = ''
    state.value = ''
    error.value = ''

    try {
      const payload: Record<string, unknown> = {}
      if (proxyId) payload.proxy_id = proxyId

      const response = await adminAPI.grok.generateAuthUrl(payload)
      authUrl.value = response.auth_url
      sessionId.value = response.session_id
      state.value = response.state
      return true
    } catch (err: any) {
      error.value = extractApiErrorMessage(err, t('admin.accounts.oauth.grok.failedToGenerateUrl'))
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
    proxyId?: number | null
  }): Promise<GrokTokenInfo | null> => {
    const code = params.code?.trim()
    if (!code || !params.sessionId || !params.state) {
      error.value = t('admin.accounts.oauth.grok.missingExchangeParams')
      return null
    }

    loading.value = true
    error.value = ''

    try {
      const payload: Record<string, unknown> = {
        session_id: params.sessionId,
        state: params.state,
        code
      }
      if (params.proxyId) payload.proxy_id = params.proxyId

      return await adminAPI.grok.exchangeCode(payload as any)
    } catch (err: any) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.grok.errors',
        t('admin.accounts.oauth.grok.failedToExchangeCode')
      )
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const validateRefreshToken = async (
    refreshToken: string,
    proxyId?: number | null
  ): Promise<GrokTokenInfo | null> => {
    if (!refreshToken.trim()) {
      error.value = t('admin.accounts.oauth.grok.pleaseEnterRefreshToken')
      return null
    }

    loading.value = true
    error.value = ''

    try {
      return await adminAPI.grok.refreshGrokToken(refreshToken.trim(), proxyId)
    } catch (err: any) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.grok.errors',
        t('admin.accounts.oauth.grok.failedToValidateRT')
      )
      return null
    } finally {
      loading.value = false
    }
  }

  const buildCredentials = (tokenInfo: GrokTokenInfo): Record<string, unknown> => {
    const credentials: Record<string, unknown> = {
      access_token: tokenInfo.access_token,
      token_type: tokenInfo.token_type,
      expires_at: tokenInfo.expires_at,
      client_id: tokenInfo.client_id,
      scope: tokenInfo.scope,
      email: tokenInfo.email,
      user_id: tokenInfo.user_id,
      team_id: tokenInfo.team_id,
      identity_key: tokenInfo.identity_key,
      subscription_tier: tokenInfo.subscription_tier,
      entitlement_status: tokenInfo.entitlement_status,
      base_url: tokenInfo.base_url
    }
    if (tokenInfo.refresh_token) credentials.refresh_token = tokenInfo.refresh_token
    if (tokenInfo.id_token) credentials.id_token = tokenInfo.id_token
    return Object.fromEntries(Object.entries(credentials).filter(([, value]) => value !== undefined && value !== ''))
  }

  const buildExtraInfo = (tokenInfo: GrokTokenInfo): Record<string, unknown> => {
    const extra: Record<string, unknown> = {}
    if (tokenInfo.email) extra.email = tokenInfo.email
    if (tokenInfo.subscription_tier) extra.subscription_tier = tokenInfo.subscription_tier
    if (tokenInfo.entitlement_status) extra.entitlement_status = tokenInfo.entitlement_status
    return extra
  }

  return {
    authUrl,
    sessionId,
    state,
    loading,
    error,
    deviceUserCode,
    deviceVerificationUrl,
    deviceStatus,
    deviceExpiresAt,
    resetState,
    cancelDeviceAuthorization,
    generateAuthUrl,
    startDeviceAuthorization,
    exchangeAuthCode,
    validateRefreshToken,
    buildCredentials,
    buildExtraInfo
  }
}
