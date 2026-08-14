<template>
  <section
    class="rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800"
    :aria-busy="loading || saving"
  >
    <div class="flex items-start gap-3 px-4 py-4 sm:px-5">
      <span
        class="mt-0.5 inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-gray-950 text-white dark:bg-white dark:text-gray-950"
      >
        <Icon name="shield" size="md" :stroke-width="1.8" />
      </span>

      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <h3 class="text-sm font-semibold text-gray-950 dark:text-white">
            {{ t('admin.accounts.anthropic.stableIdentity.title') }}
          </h3>
          <span :class="statusBadgeClass">{{ statusLabel }}</span>
        </div>
        <p class="mt-1 max-w-3xl text-xs leading-5 text-gray-600 dark:text-gray-300">
          {{ t('admin.accounts.anthropic.stableIdentity.description') }}
        </p>
      </div>

      <button
        type="button"
        role="switch"
        :aria-checked="enabled"
        :aria-label="t('admin.accounts.anthropic.stableIdentity.title')"
        :disabled="loading || saving"
        :class="[
          'relative mt-1 inline-flex h-6 w-11 shrink-0 rounded-full border-2 border-transparent transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 active:scale-[0.97] disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-dark-800',
          enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
        ]"
        @click="toggle"
      >
        <span
          :class="[
            'pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow-sm transition-transform duration-200',
            enabled ? 'translate-x-5' : 'translate-x-0'
          ]"
        />
      </button>
    </div>

    <div
      v-if="status?.blocked"
      class="border-t border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-5 text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200 sm:px-5"
    >
      <div class="flex items-start gap-2">
        <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" />
        <div>
          <p class="font-medium">{{ t('admin.accounts.anthropic.stableIdentity.blockedTitle') }}</p>
          <p class="mt-0.5">
            {{ status.blocked_reason || t('admin.accounts.anthropic.stableIdentity.blockedFallback') }}
          </p>
        </div>
      </div>
    </div>

    <div
      v-if="enabled"
      class="grid grid-cols-3 border-t border-gray-200 dark:border-dark-600"
    >
      <div class="border-r border-gray-200 px-4 py-3 dark:border-dark-600 sm:px-5">
        <p class="text-[11px] text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.anthropic.stableIdentity.device') }}
        </p>
        <p class="mt-1 font-mono text-xs font-medium text-gray-950 dark:text-white">
          {{ status?.device_fingerprint ? `${status.device_fingerprint}…` : '—' }}
        </p>
      </div>
      <div class="border-r border-gray-200 px-4 py-3 dark:border-dark-600 sm:px-5">
        <p class="text-[11px] text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.anthropic.stableIdentity.generation') }}
        </p>
        <p class="mt-1 text-xs font-semibold tabular-nums text-gray-950 dark:text-white">
          {{ status?.generation || 0 }}
        </p>
      </div>
      <div class="px-4 py-3 sm:px-5">
        <p class="text-[11px] text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.anthropic.stableIdentity.groups') }}
        </p>
        <p class="mt-1 text-xs font-semibold tabular-nums text-gray-950 dark:text-white">
          {{ status?.group_ids.length || 0 }}
        </p>
      </div>
    </div>

    <div
      v-if="loadError || actionError"
      class="border-t border-red-200 bg-red-50 px-4 py-3 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300 sm:px-5"
      role="alert"
    >
      {{ loadError || actionError }}
    </div>

    <div
      v-if="saving"
      class="flex items-center gap-2 border-t border-gray-200 px-4 py-2.5 text-[11px] text-gray-500 dark:border-dark-600 dark:text-gray-400 sm:px-5"
    >
      <Icon name="refresh" size="sm" class="animate-spin" />
      {{ t('common.saving') }}
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AnthropicStableIdentityStatus } from '@/api/admin/accounts'
import type { Account } from '@/types'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  account: Account
}>()

const emit = defineEmits<{
  changed: [account: Account]
  'status-change': [status: AnthropicStableIdentityStatus]
}>()

const { t } = useI18n()
const status = ref<AnthropicStableIdentityStatus | null>(null)
const loading = ref(true)
const saving = ref(false)
const loadError = ref('')
const actionError = ref('')
let loadRevision = 0

const enabled = computed(() => status.value?.enabled === true)

const statusLabel = computed(() => {
  if (loading.value) return t('common.loading')
  if (status.value?.blocked) return t('admin.accounts.anthropic.stableIdentity.statusBlocked')
  if (!enabled.value) return t('admin.accounts.anthropic.stableIdentity.statusOff')
  if (status.value?.state === 'paused') return t('admin.accounts.anthropic.stableIdentity.statusPaused')
  return t('admin.accounts.anthropic.stableIdentity.statusActive')
})

const statusBadgeClass = computed(() => {
  const base = 'inline-flex rounded-full px-2 py-0.5 text-[10px] font-semibold'
  if (status.value?.blocked) return `${base} bg-red-100 text-red-700 dark:bg-red-950/50 dark:text-red-300`
  if (!enabled.value) return `${base} bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300`
  if (status.value?.state === 'paused') {
    return `${base} bg-amber-100 text-amber-800 dark:bg-amber-950/50 dark:text-amber-300`
  }
  return `${base} bg-emerald-100 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300`
})

const errorMessage = (error: unknown, fallback: string) => {
  if (error && typeof error === 'object') {
    const candidate = error as { message?: unknown; response?: { data?: { message?: unknown; detail?: unknown } } }
    const responseMessage = candidate.response?.data?.message ?? candidate.response?.data?.detail
    if (typeof responseMessage === 'string' && responseMessage.trim()) return responseMessage
    if (typeof candidate.message === 'string' && candidate.message.trim()) return candidate.message
  }
  return fallback
}

const applyStatus = (nextStatus: AnthropicStableIdentityStatus) => {
  status.value = nextStatus
  emit('status-change', nextStatus)
}

const load = async () => {
  const revision = ++loadRevision
  const accountID = props.account.id
  loading.value = true
  loadError.value = ''
  actionError.value = ''
  try {
    const nextStatus = await adminAPI.accounts.getAnthropicStableIdentity(accountID)
    if (revision !== loadRevision || props.account.id !== accountID) return
    applyStatus(nextStatus)
  } catch (error) {
    if (revision !== loadRevision || props.account.id !== accountID) return
    loadError.value = errorMessage(error, t('admin.accounts.anthropic.stableIdentity.loadFailed'))
  } finally {
    if (revision === loadRevision && props.account.id === accountID) loading.value = false
  }
}

const syncAccount = async () => {
  const updatedAccount = await adminAPI.accounts.getById(props.account.id)
  emit('changed', updatedAccount)
}

const toggle = async () => {
  if (loading.value || saving.value) return
  saving.value = true
  actionError.value = ''
  try {
    const nextStatus = enabled.value
      ? await adminAPI.accounts.disableAnthropicStableIdentity(props.account.id)
      : await adminAPI.accounts.configureAnthropicStableIdentity(props.account.id)
    applyStatus(nextStatus)
    await syncAccount()
  } catch (error) {
    actionError.value = errorMessage(
      error,
      enabled.value
        ? t('admin.accounts.anthropic.stableIdentity.disableFailed')
        : t('admin.accounts.anthropic.stableIdentity.configureFailed')
    )
  } finally {
    saving.value = false
  }
}

watch(
  () => props.account.id,
  () => void load(),
  { immediate: true }
)
</script>

<style scoped>
@media (prefers-reduced-motion: reduce) {
  button,
  span {
    transition-duration: 0.01ms !important;
  }
}
</style>
