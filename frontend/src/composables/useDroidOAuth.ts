import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { DroidAuthUrlResponse, DroidTokenInfo } from '@/api/admin/droid'

export function useDroidOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()

  const authUrl = ref('')
  const sessionId = ref('')
  const loading = ref(false)
  const error = ref('')
  const userCode = ref('')
  const verificationURI = ref('')
  const verificationURIComplete = ref('')
  const expiresIn = ref<number | null>(null)
  const interval = ref<number | null>(null)
  const pending = ref(false)
  const retryAfter = ref<number | null>(null)

  const resetState = () => {
    authUrl.value = ''
    sessionId.value = ''
    loading.value = false
    error.value = ''
    userCode.value = ''
    verificationURI.value = ''
    verificationURIComplete.value = ''
    expiresIn.value = null
    interval.value = null
    pending.value = false
    retryAfter.value = null
  }

  const errorMessage = (err: any) =>
    err?.message ||
    err?.response?.data?.message ||
    err?.response?.data?.detail ||
    t('admin.accounts.oauth.droid.authFailed')

  const applyAuthResponse = (response: DroidAuthUrlResponse) => {
    sessionId.value = response.session_id || ''
    verificationURI.value = response.verification_uri || ''
    verificationURIComplete.value = response.verification_uri_complete || response.verification_uri || ''
    authUrl.value = verificationURIComplete.value
    userCode.value = response.user_code || ''
    expiresIn.value = response.expires_in ?? null
    interval.value = response.interval ?? null
  }

  const generateAuthUrl = async (proxyId: number | null | undefined): Promise<boolean> => {
    loading.value = true
    error.value = ''
    pending.value = false
    retryAfter.value = null
    try {
      const response = await adminAPI.droid.generateAuthUrl({
        proxy_id: proxyId || undefined
      })
      applyAuthResponse(response)
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
    sessionId: string
    proxyId?: number | null
  }): Promise<DroidTokenInfo | null> => {
    loading.value = true
    error.value = ''
    pending.value = false
    retryAfter.value = null
    try {
      const result = await adminAPI.droid.exchangeCode({
        session_id: params.sessionId,
        proxy_id: params.proxyId || undefined
      })
      if ('pending' in result && result.pending) {
        pending.value = true
        retryAfter.value = typeof result.retry_after === 'number' ? result.retry_after : null
        error.value = typeof result.message === 'string' && result.message.trim()
          ? result.message
          : t('admin.accounts.oauth.droid.authorizationPending')
        return null
      }
      return result as DroidTokenInfo
    } catch (err: any) {
      error.value = errorMessage(err)
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const buildCredentials = (tokenInfo: DroidTokenInfo): Record<string, unknown> => ({
    access_token: tokenInfo.access_token,
    refresh_token: tokenInfo.refresh_token,
    expires_at: tokenInfo.expires_at,
    token_type: tokenInfo.token_type
  })

  return {
    authUrl,
    sessionId,
    loading,
    error,
    userCode,
    verificationURI,
    verificationURIComplete,
    expiresIn,
    interval,
    pending,
    retryAfter,
    resetState,
    generateAuthUrl,
    exchangeAuthCode,
    buildCredentials
  }
}
