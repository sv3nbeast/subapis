import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { CursorTokenInfo } from '@/api/admin/cursor'

/** 轮询间隔：Cursor 的登录页通常几秒内完成，2s 既不过载上游也不显得迟钝。 */
const POLL_INTERVAL_MS = 2000
/** 轮询上限，与后端会话 TTL（30 分钟）一致，超时后要求重新发起授权。 */
const POLL_TIMEOUT_MS = 30 * 60 * 1000

export function useCursorOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()

  const authUrl = ref('')
  const sessionId = ref('')
  const loading = ref(false)
  const polling = ref(false)
  const error = ref('')

  // 轮询在组件外持续运行，必须能被显式取消：用户关闭弹窗或切换平台时
  // 若继续轮询，拿到的凭证会写进一个已经不存在的表单。
  let cancelled = false
  let pollTimer: ReturnType<typeof setTimeout> | null = null
  // 取消时必须同时结束正在等待的间隔：只清掉定时器会让那个 promise 永远
  // 不 settle，awaitAuthorization 便挂在 await 上再也不返回。
  let wakeSleeper: (() => void) | null = null

  const clearTimer = () => {
    if (pollTimer !== null) {
      clearTimeout(pollTimer)
      pollTimer = null
    }
    if (wakeSleeper !== null) {
      const wake = wakeSleeper
      wakeSleeper = null
      wake()
    }
  }

  const sleepBetweenPolls = () =>
    new Promise<void>((resolve) => {
      wakeSleeper = resolve
      pollTimer = setTimeout(() => {
        wakeSleeper = null
        pollTimer = null
        resolve()
      }, POLL_INTERVAL_MS)
    })

  const resetState = () => {
    cancelled = true
    clearTimer()
    authUrl.value = ''
    sessionId.value = ''
    loading.value = false
    polling.value = false
    error.value = ''
  }

  const errorMessage = (err: any) =>
    err?.message ||
    err?.response?.data?.message ||
    err?.response?.data?.detail ||
    t('admin.accounts.cursor.oauth.authFailed')

  const generateAuthUrl = async (proxyId: number | null | undefined): Promise<boolean> => {
    cancelled = false
    clearTimer()
    loading.value = true
    error.value = ''
    try {
      const response = await adminAPI.cursor.generateAuthUrl({
        proxy_id: proxyId || undefined
      })
      authUrl.value = response.auth_url || ''
      sessionId.value = response.session_id || ''
      return Boolean(authUrl.value && sessionId.value)
    } catch (err: any) {
      error.value = errorMessage(err)
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  /**
   * 反复轮询直到用户完成登录。返回 null 表示被取消、超时或出错——调用方
   * 据此决定是否继续建号，error 里带有面向用户的原因。
   */
  const awaitAuthorization = async (
    proxyId: number | null | undefined
  ): Promise<CursorTokenInfo | null> => {
    if (!sessionId.value) {
      error.value = t('admin.accounts.cursor.oauth.sessionMissing')
      return null
    }
    cancelled = false
    polling.value = true
    error.value = ''
    const deadline = Date.now() + POLL_TIMEOUT_MS

    try {
      while (!cancelled) {
        if (Date.now() > deadline) {
          error.value = t('admin.accounts.cursor.oauth.timeout')
          appStore.showError(error.value)
          return null
        }
        let result: CursorTokenInfo
        try {
          result = await adminAPI.cursor.pollAuthorization({
            session_id: sessionId.value,
            proxy_id: proxyId || undefined
          })
        } catch (err: any) {
          error.value = errorMessage(err)
          appStore.showError(error.value)
          return null
        }
        if (cancelled) {
          return null
        }
        if (!result.pending) {
          return result
        }
        await sleepBetweenPolls()
      }
      return null
    } finally {
      polling.value = false
      clearTimer()
    }
  }

  /** 取消进行中的轮询，但保留已生成的授权地址，便于用户重试。 */
  const cancelPolling = () => {
    cancelled = true
    clearTimer()
    polling.value = false
  }

  const buildCredentials = (tokenInfo: CursorTokenInfo): Record<string, unknown> => {
    const credentials: Record<string, unknown> = {
      access_token: tokenInfo.access_token,
      machine_id: tokenInfo.machine_id,
      mac_machine_id: tokenInfo.mac_machine_id
    }
    if (tokenInfo.refresh_token) credentials.refresh_token = tokenInfo.refresh_token
    if (tokenInfo.expires_at) credentials.expires_at = tokenInfo.expires_at
    if (tokenInfo.user_id) credentials.user_id = tokenInfo.user_id
    if (tokenInfo.email) credentials.email = tokenInfo.email
    return credentials
  }

  return {
    authUrl,
    sessionId,
    loading,
    polling,
    error,
    resetState,
    generateAuthUrl,
    awaitAuthorization,
    cancelPolling,
    buildCredentials
  }
}
