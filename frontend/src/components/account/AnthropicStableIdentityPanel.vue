<template>
  <section
    class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800"
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
          <span :class="statusBadgeClass">
            {{ statusLabel }}
          </span>
        </div>
        <p class="mt-1 max-w-3xl text-xs leading-5 text-gray-600 dark:text-gray-300">
          {{ t('admin.accounts.anthropic.stableIdentity.description') }}
        </p>
      </div>
      <button
        type="button"
        class="rounded-md p-2 text-gray-500 transition-colors duration-150 hover:bg-gray-100 hover:text-gray-900 active:scale-[0.96] disabled:cursor-not-allowed disabled:opacity-50 dark:text-gray-300 dark:hover:bg-dark-700 dark:hover:text-white"
        :disabled="loading || saving"
        :title="t('common.refresh')"
        @click="load"
      >
        <Icon name="refresh" size="sm" :class="loading && 'animate-spin'" />
      </button>
    </div>

    <div v-if="loadError" class="border-t border-red-200 bg-red-50 px-4 py-3 dark:border-red-900/60 dark:bg-red-950/30">
      <div class="flex items-start gap-2 text-xs text-red-700 dark:text-red-300">
        <Icon name="exclamationCircle" size="sm" class="mt-0.5 shrink-0" />
        <span>{{ loadError }}</span>
      </div>
    </div>

    <template v-else-if="!loading">
      <div
        v-if="oauthPassthroughEnabled"
        class="border-t border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-5 text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200 sm:px-5"
      >
        <div class="flex items-start gap-2">
          <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" />
          <p>{{ t('admin.accounts.anthropic.stableIdentity.passthroughConflict') }}</p>
        </div>
      </div>
      <div
        v-if="status?.blocked"
        class="border-t border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-5 text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200 sm:px-5"
      >
        <div class="flex items-start gap-2">
          <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" />
          <div>
            <p class="font-medium">{{ t('admin.accounts.anthropic.stableIdentity.blockedTitle') }}</p>
            <p class="mt-0.5">{{ status.blocked_reason || t('admin.accounts.anthropic.stableIdentity.blockedFallback') }}</p>
          </div>
        </div>
      </div>

      <div v-if="status?.enabled" class="grid grid-cols-2 border-t border-gray-200 dark:border-dark-600 sm:grid-cols-4">
        <div class="border-b border-r border-gray-200 px-4 py-3 dark:border-dark-600 sm:border-b-0 sm:px-5">
          <p class="text-[11px] text-gray-500 dark:text-gray-400">{{ t('admin.accounts.anthropic.stableIdentity.device') }}</p>
          <p class="mt-1 font-mono text-xs font-medium text-gray-950 dark:text-white">
            {{ status.device_fingerprint ? `${status.device_fingerprint}…` : '—' }}
          </p>
        </div>
        <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-600 sm:border-b-0 sm:border-r sm:px-5">
          <p class="text-[11px] text-gray-500 dark:text-gray-400">{{ t('admin.accounts.anthropic.stableIdentity.generation') }}</p>
          <p class="mt-1 text-xs font-semibold tabular-nums text-gray-950 dark:text-white">{{ status.generation }}</p>
        </div>
        <div class="border-r border-gray-200 px-4 py-3 dark:border-dark-600 sm:px-5">
          <p class="text-[11px] text-gray-500 dark:text-gray-400">{{ t('admin.accounts.anthropic.stableIdentity.groups') }}</p>
          <p class="mt-1 text-xs font-semibold tabular-nums text-gray-950 dark:text-white">{{ status.group_ids.length }}</p>
        </div>
        <div class="px-4 py-3 sm:px-5">
          <p class="text-[11px] text-gray-500 dark:text-gray-400">{{ t('admin.accounts.anthropic.stableIdentity.keys') }}</p>
          <p class="mt-1 text-xs font-semibold tabular-nums text-gray-950 dark:text-white">{{ status.api_key_ids.length }}</p>
        </div>
      </div>

      <div class="space-y-5 border-t border-gray-200 px-4 py-4 dark:border-dark-600 sm:px-5">
        <div>
          <div class="flex flex-wrap items-end justify-between gap-2">
            <div>
              <label class="text-xs font-semibold text-gray-900 dark:text-gray-100">
                {{ t('admin.accounts.anthropic.stableIdentity.existingGroups') }}
              </label>
              <p class="mt-1 text-[11px] leading-4 text-gray-500 dark:text-gray-400">
                {{ t('admin.accounts.anthropic.stableIdentity.existingGroupsHint') }}
              </p>
            </div>
            <span class="text-[11px] tabular-nums text-gray-500 dark:text-gray-400">
              {{ t('common.selectedCount', { count: selectedGroupIDs.length }) }}
            </span>
          </div>
          <div class="mt-2 grid max-h-36 grid-cols-1 gap-1.5 overflow-y-auto rounded-lg border border-gray-200 bg-gray-50 p-2 dark:border-dark-600 dark:bg-dark-900/40 sm:grid-cols-2">
            <label
              v-for="group in availableGroups"
              :key="group.id"
              class="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-xs text-gray-800 transition-colors duration-150 hover:bg-white active:scale-[0.995] dark:text-gray-100 dark:hover:bg-dark-700"
            >
              <input
                type="checkbox"
                class="h-3.5 w-3.5 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500"
                :checked="selectedGroupIDs.includes(group.id)"
                :disabled="saving || keysLoading"
                @change="toggleGroup(group.id, ($event.target as HTMLInputElement).checked)"
              />
              <span class="min-w-0 flex-1 truncate font-medium">{{ group.name }}</span>
              <span class="shrink-0 text-[10px] tabular-nums text-gray-500 dark:text-gray-400">
                {{ group.account_count || 0 }}
              </span>
            </label>
            <p v-if="availableGroups.length === 0" class="py-3 text-center text-xs text-gray-500 dark:text-gray-400 sm:col-span-2">
              {{ t('admin.accounts.anthropic.stableIdentity.noGroups') }}
            </p>
          </div>
        </div>

        <div>
          <div class="flex flex-wrap items-end justify-between gap-2">
            <div>
              <label class="text-xs font-semibold text-gray-900 dark:text-gray-100">
                {{ t('admin.accounts.anthropic.stableIdentity.routeKeys') }}
              </label>
              <p class="mt-1 text-[11px] leading-4 text-gray-500 dark:text-gray-400">
                {{ t('admin.accounts.anthropic.stableIdentity.routeKeysHint') }}
              </p>
            </div>
            <button
              v-if="availableKeys.length > 0"
              type="button"
              class="rounded px-1.5 py-1 text-[11px] font-medium text-primary-700 transition-colors duration-150 hover:bg-primary-50 active:scale-[0.97] dark:text-primary-300 dark:hover:bg-primary-950/40"
              :disabled="saving || keysLoading"
              @click="toggleAllKeys"
            >
              {{ allKeysSelected
                ? t('admin.accounts.anthropic.stableIdentity.deselectAll')
                : t('common.selectAll') }}
            </button>
          </div>
          <div class="mt-2 max-h-44 overflow-y-auto rounded-lg border border-gray-200 dark:border-dark-600">
            <div v-if="keysLoading" class="flex items-center justify-center gap-2 px-3 py-7 text-xs text-gray-500 dark:text-gray-400">
              <Icon name="refresh" size="sm" class="animate-spin" />
              {{ t('common.loading') }}
            </div>
            <template v-else-if="availableKeys.length > 0">
              <label
                v-for="item in availableKeys"
                :key="item.id"
                class="flex cursor-pointer items-center gap-3 border-b border-gray-100 px-3 py-2.5 text-xs last:border-b-0 hover:bg-gray-50 active:bg-gray-100 dark:border-dark-700 dark:hover:bg-dark-700/70 dark:active:bg-dark-700"
              >
                <input
                  type="checkbox"
                  class="h-3.5 w-3.5 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500"
                  :checked="selectedAPIKeyIDs.includes(item.id)"
                  :disabled="saving"
                  @change="toggleKey(item.id, ($event.target as HTMLInputElement).checked)"
                />
                <span class="min-w-0 flex-1">
                  <span class="block truncate font-medium text-gray-900 dark:text-gray-100">{{ item.name || `#${item.id}` }}</span>
                  <span class="mt-0.5 block truncate text-[10px] text-gray-500 dark:text-gray-400">
                    {{ groupName(item.group_id) }}<template v-if="item.user?.email"> · {{ item.user.email }}</template>
                  </span>
                </span>
                <span class="shrink-0 font-mono text-[10px] text-gray-400 dark:text-gray-500">#{{ item.id }}</span>
              </label>
            </template>
            <p v-else class="px-3 py-7 text-center text-xs text-gray-500 dark:text-gray-400">
              {{ selectedGroupIDs.length === 0
                ? t('admin.accounts.anthropic.stableIdentity.selectGroupFirst')
                : t('admin.accounts.anthropic.stableIdentity.noKeys') }}
            </p>
          </div>
          <p v-if="keysError" class="mt-1.5 text-xs text-red-600 dark:text-red-300">{{ keysError }}</p>
        </div>

        <div class="rounded-lg bg-gray-50 px-3 py-2.5 text-[11px] leading-5 text-gray-600 dark:bg-dark-900/40 dark:text-gray-300">
          <div class="flex items-start gap-2">
            <Icon name="infoCircle" size="sm" class="mt-0.5 shrink-0" />
            <p>{{ t('admin.accounts.anthropic.stableIdentity.scopeHint') }}</p>
          </div>
        </div>

        <p v-if="actionError" class="text-xs text-red-600 dark:text-red-300" role="alert">{{ actionError }}</p>

        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 pt-4 dark:border-dark-600">
          <div class="flex flex-wrap gap-2">
            <button
              type="button"
              class="btn btn-primary active:scale-[0.98]"
              :disabled="saving || keysLoading || !canConfigure"
              @click="configure"
            >
              <Icon v-if="savingAction === 'configure'" name="refresh" size="sm" class="mr-1.5 animate-spin" />
              {{ configureLabel }}
            </button>
            <button
              v-if="status?.enabled && status.state === 'active' && !status.blocked"
              type="button"
              class="btn btn-secondary active:scale-[0.98]"
              :disabled="saving"
              @click="pause"
            >
              {{ t('admin.accounts.anthropic.stableIdentity.pause') }}
            </button>
            <button
              v-if="status?.enabled && (status.state === 'paused' || status.blocked)"
              type="button"
              class="btn btn-secondary active:scale-[0.98]"
              :disabled="saving"
              @click="resume"
            >
              {{ t('admin.accounts.anthropic.stableIdentity.resume') }}
            </button>
          </div>

          <div v-if="status?.enabled" class="flex items-center gap-2">
            <button
              v-if="!confirmDisable"
              type="button"
              class="rounded-md px-2.5 py-1.5 text-xs font-medium text-red-600 transition-colors duration-150 hover:bg-red-50 active:scale-[0.97] dark:text-red-300 dark:hover:bg-red-950/30"
              :disabled="saving"
              @click="confirmDisable = true"
            >
              {{ t('admin.accounts.anthropic.stableIdentity.disable') }}
            </button>
            <template v-else>
              <span class="text-[11px] text-gray-600 dark:text-gray-300">
                {{ t('admin.accounts.anthropic.stableIdentity.disableConfirm') }}
              </span>
              <button
                type="button"
                class="rounded-md bg-red-600 px-2.5 py-1.5 text-xs font-semibold text-white transition-colors duration-150 hover:bg-red-700 active:scale-[0.97] disabled:opacity-50"
                :disabled="saving"
                @click="disable"
              >
                {{ t('common.confirm') }}
              </button>
              <button
                type="button"
                class="rounded-md px-2.5 py-1.5 text-xs text-gray-600 transition-colors duration-150 hover:bg-gray-100 active:scale-[0.97] dark:text-gray-300 dark:hover:bg-dark-700"
                :disabled="saving"
                @click="confirmDisable = false"
              >
                {{ t('common.cancel') }}
              </button>
            </template>
          </div>
        </div>
      </div>
    </template>

    <div v-else class="flex items-center justify-center gap-2 border-t border-gray-200 px-4 py-10 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
      <Icon name="refresh" size="sm" class="animate-spin" />
      {{ t('common.loading') }}
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AnthropicStableIdentityStatus } from '@/api/admin/accounts'
import type { Account, AdminGroup } from '@/types'
import Icon from '@/components/icons/Icon.vue'

interface GroupAPIKey {
  id: number
  name: string
  group_id: number | null
  status: string
  expires_at?: string | null
  user?: { email?: string }
}

const props = defineProps<{
  account: Account
  groups: AdminGroup[]
  oauthPassthroughEnabled?: boolean
}>()

const emit = defineEmits<{
  changed: [account: Account]
  'status-change': [status: AnthropicStableIdentityStatus]
}>()

const { t } = useI18n()
const status = ref<AnthropicStableIdentityStatus | null>(null)
const loading = ref(true)
const savingAction = ref<'configure' | 'pause' | 'resume' | 'disable' | ''>('')
const loadError = ref('')
const actionError = ref('')
const keysError = ref('')
const keysLoading = ref(false)
const selectedGroupIDs = ref<number[]>([])
const selectedAPIKeyIDs = ref<number[]>([])
const availableKeys = ref<GroupAPIKey[]>([])
const confirmDisable = ref(false)
let keyLoadRevision = 0
let statusLoadRevision = 0

const saving = computed(() => savingAction.value !== '')
const availableGroups = computed(() =>
  props.groups.filter((group) => group.platform === 'anthropic' && group.status === 'active')
)
const availableGroupIDs = computed(() => new Set(availableGroups.value.map((group) => group.id)))
const allKeysSelected = computed(
  () => availableKeys.value.length > 0 && availableKeys.value.every((key) => selectedAPIKeyIDs.value.includes(key.id))
)
const canConfigure = computed(
  () =>
    !props.oauthPassthroughEnabled &&
    selectedGroupIDs.value.length > 0 &&
    selectedAPIKeyIDs.value.length > 0 &&
    !keysError.value
)

const statusLabel = computed(() => {
  if (loading.value) return t('common.loading')
  if (status.value?.blocked) return t('admin.accounts.anthropic.stableIdentity.statusBlocked')
  if (!status.value?.enabled) return t('admin.accounts.anthropic.stableIdentity.statusOff')
  if (status.value.state === 'paused') return t('admin.accounts.anthropic.stableIdentity.statusPaused')
  return t('admin.accounts.anthropic.stableIdentity.statusActive')
})

const statusBadgeClass = computed(() => {
  const base = 'inline-flex rounded-full px-2 py-0.5 text-[10px] font-semibold'
  if (status.value?.blocked) return `${base} bg-red-100 text-red-700 dark:bg-red-950/50 dark:text-red-300`
  if (!status.value?.enabled) return `${base} bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300`
  if (status.value.state === 'paused') return `${base} bg-amber-100 text-amber-800 dark:bg-amber-950/50 dark:text-amber-300`
  return `${base} bg-emerald-100 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300`
})

const configureLabel = computed(() => {
  if (!status.value?.enabled) return t('admin.accounts.anthropic.stableIdentity.enable')
  if (status.value.blocked || status.value.state === 'paused') {
    return t('admin.accounts.anthropic.stableIdentity.saveAndResume')
  }
  return t('admin.accounts.anthropic.stableIdentity.apply')
})

const errorMessage = (error: unknown, fallback: string) => {
  if (error && typeof error === 'object') {
    const candidate = error as { message?: unknown; response?: { data?: { message?: unknown } } }
    const responseMessage = candidate.response?.data?.message
    if (typeof responseMessage === 'string' && responseMessage.trim()) return responseMessage
    if (typeof candidate.message === 'string' && candidate.message.trim()) return candidate.message
  }
  return fallback
}

const groupName = (groupID: number | null) =>
  props.groups.find((group) => group.id === groupID)?.name || (groupID ? `#${groupID}` : '—')

const isUsableKey = (key: GroupAPIKey, groupID: number) => {
  if (key.status !== 'active' || key.group_id !== groupID) return false
  if (!key.expires_at) return true
  const expiresAt = Date.parse(key.expires_at)
  return Number.isNaN(expiresAt) || expiresAt > Date.now()
}

const loadKeys = async (preferred?: number[], autoSelectGroupIDs: number[] = []) => {
  const revision = ++keyLoadRevision
  keysLoading.value = true
  keysError.value = ''
  const groupIDs = [...selectedGroupIDs.value]
  if (groupIDs.length === 0) {
    availableKeys.value = []
    selectedAPIKeyIDs.value = []
    keysLoading.value = false
    return
  }
  try {
    const rows: GroupAPIKey[] = []
    const seenKeyIDs = new Set<number>()
    for (const groupID of groupIDs) {
      let page = 1
      let pages = 1
      do {
        const result = await adminAPI.groups.getGroupApiKeys(groupID, page, 1000)
        pages = Math.max(1, result.pages || 1)
        for (const raw of result.items as GroupAPIKey[]) {
          if (isUsableKey(raw, groupID) && !seenKeyIDs.has(raw.id)) {
            seenKeyIDs.add(raw.id)
            rows.push(raw)
          }
        }
        page += 1
      } while (page <= pages)
    }
    if (revision !== keyLoadRevision) return
    rows.sort((a, b) => (a.name || '').localeCompare(b.name || '') || a.id - b.id)
    availableKeys.value = rows
    const preferredSet = new Set(preferred ?? rows.map((row) => row.id))
    const autoSelectGroups = new Set(autoSelectGroupIDs)
    selectedAPIKeyIDs.value = rows
      .filter((row) => preferredSet.has(row.id) || (row.group_id != null && autoSelectGroups.has(row.group_id)))
      .map((row) => row.id)
  } catch (error) {
    if (revision !== keyLoadRevision) return
    availableKeys.value = []
    selectedAPIKeyIDs.value = []
    keysError.value = errorMessage(error, t('admin.accounts.anthropic.stableIdentity.keysLoadFailed'))
  } finally {
    if (revision === keyLoadRevision) keysLoading.value = false
  }
}

const load = async () => {
  const revision = ++statusLoadRevision
  const accountID = props.account.id
  loading.value = true
  loadError.value = ''
  actionError.value = ''
  confirmDisable.value = false
  try {
    const nextStatus = await adminAPI.accounts.getAnthropicStableIdentity(accountID)
    if (revision !== statusLoadRevision || props.account.id !== accountID) return
    status.value = nextStatus
    emit('status-change', nextStatus)
    const preferredGroups = nextStatus.enabled
      ? nextStatus.group_ids
      : (props.account.group_ids || []).filter((id) => availableGroupIDs.value.has(id))
    selectedGroupIDs.value = preferredGroups.filter((id) => availableGroupIDs.value.has(id))
    await loadKeys(nextStatus.enabled ? nextStatus.api_key_ids : undefined)
  } catch (error) {
    if (revision !== statusLoadRevision || props.account.id !== accountID) return
    loadError.value = errorMessage(error, t('admin.accounts.anthropic.stableIdentity.loadFailed'))
  } finally {
    if (revision === statusLoadRevision && props.account.id === accountID) loading.value = false
  }
}

const syncAfterMutation = async (nextStatus: AnthropicStableIdentityStatus) => {
  status.value = nextStatus
  emit('status-change', nextStatus)
  const updatedAccount = await adminAPI.accounts.getById(props.account.id)
  emit('changed', updatedAccount)
  return updatedAccount
}

const configure = async () => {
  if (!canConfigure.value || saving.value) return
  savingAction.value = 'configure'
  actionError.value = ''
  confirmDisable.value = false
  try {
    const nextStatus = await adminAPI.accounts.configureAnthropicStableIdentity(props.account.id, {
      group_ids: [...selectedGroupIDs.value],
      api_key_ids: [...selectedAPIKeyIDs.value]
    })
    await syncAfterMutation(nextStatus)
  } catch (error) {
    actionError.value = errorMessage(error, t('admin.accounts.anthropic.stableIdentity.configureFailed'))
  } finally {
    savingAction.value = ''
  }
}

const pause = async () => {
  if (saving.value) return
  savingAction.value = 'pause'
  actionError.value = ''
  try {
    await syncAfterMutation(await adminAPI.accounts.pauseAnthropicStableIdentity(props.account.id))
  } catch (error) {
    actionError.value = errorMessage(error, t('admin.accounts.anthropic.stableIdentity.pauseFailed'))
  } finally {
    savingAction.value = ''
  }
}

const resume = async () => {
  if (saving.value) return
  savingAction.value = 'resume'
  actionError.value = ''
  try {
    await syncAfterMutation(await adminAPI.accounts.resumeAnthropicStableIdentity(props.account.id))
  } catch (error) {
    actionError.value = errorMessage(error, t('admin.accounts.anthropic.stableIdentity.resumeFailed'))
  } finally {
    savingAction.value = ''
  }
}

const disable = async () => {
  if (saving.value) return
  savingAction.value = 'disable'
  actionError.value = ''
  try {
    const nextStatus = await adminAPI.accounts.disableAnthropicStableIdentity(props.account.id)
    const updatedAccount = await syncAfterMutation(nextStatus)
    selectedGroupIDs.value = (updatedAccount.group_ids || []).filter((id) => availableGroupIDs.value.has(id))
    await loadKeys()
    confirmDisable.value = false
  } catch (error) {
    actionError.value = errorMessage(error, t('admin.accounts.anthropic.stableIdentity.disableFailed'))
  } finally {
    savingAction.value = ''
  }
}

const toggleGroup = async (groupID: number, checked: boolean) => {
  if (keysLoading.value) return
  const previousKeyIDs = [...selectedAPIKeyIDs.value]
  selectedGroupIDs.value = checked
    ? [...selectedGroupIDs.value, groupID].sort((a, b) => a - b)
    : selectedGroupIDs.value.filter((id) => id !== groupID)
  // Keep explicit selections from the groups that remain. Keys in a newly
  // added group start selected so the simple path stays one-click, while a
  // prior manual deselection is never lost merely because membership changed.
  await loadKeys(previousKeyIDs, checked ? [groupID] : [])
}

const toggleKey = (keyID: number, checked: boolean) => {
  selectedAPIKeyIDs.value = checked
    ? [...selectedAPIKeyIDs.value, keyID].sort((a, b) => a - b)
    : selectedAPIKeyIDs.value.filter((id) => id !== keyID)
}

const toggleAllKeys = () => {
  selectedAPIKeyIDs.value = allKeysSelected.value ? [] : availableKeys.value.map((key) => key.id)
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
  label {
    transition-duration: 0.01ms !important;
  }
}
</style>
