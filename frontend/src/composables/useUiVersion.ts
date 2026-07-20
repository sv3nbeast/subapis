import { computed, ref, toValue, watch, type MaybeRefOrGetter, type Ref } from 'vue'

export type UiVersion = 'legacy' | 'v2'
export type UiV2RolloutMode = 'off' | 'preview' | 'percentage' | 'full'

interface ResolveUiVersionOptions {
  mode: UiV2RolloutMode
  percentage: number
  queryPreference: UiVersion | null
  storedPreference: UiVersion | null
  subject: string
}

const UI_VERSION_QUERY_KEY = 'ui'
const UI_VERSION_STORAGE_PREFIX = 'sub2api:ui-version'

function normalizeUiVersion(value: unknown): UiVersion | null {
  if (value === 'v2') return 'v2'
  if (value === 'legacy' || value === 'v1') return 'legacy'
  return null
}

function normalizeRolloutMode(value: unknown): UiV2RolloutMode {
  if (value === 'off' || value === 'percentage' || value === 'full') return value
  return 'preview'
}

function normalizePercentage(value: unknown): number {
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) return 0
  return Math.min(100, Math.max(0, Math.round(parsed)))
}

export function uiV2CohortBucket(subject: string): number {
  let hash = 2166136261
  for (let index = 0; index < subject.length; index += 1) {
    hash ^= subject.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return (hash >>> 0) % 100
}

export function resolveUiVersion(options: ResolveUiVersionOptions): UiVersion {
  if (options.mode === 'off') return 'legacy'

  if (options.queryPreference) return options.queryPreference
  if (options.storedPreference) return options.storedPreference

  if (options.mode === 'full') return 'v2'
  if (options.mode === 'percentage') {
    return uiV2CohortBucket(options.subject) < options.percentage ? 'v2' : 'legacy'
  }
  return 'legacy'
}

function getStorageKey(subject: string): string {
  return `${UI_VERSION_STORAGE_PREFIX}:${subject || 'anonymous'}`
}

function getQueryPreference(): UiVersion | null {
  if (typeof window === 'undefined') return null
  const value = new URLSearchParams(window.location.search).get(UI_VERSION_QUERY_KEY)
  return normalizeUiVersion(value)
}

function removeUiVersionQuery(): void {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  if (!url.searchParams.has(UI_VERSION_QUERY_KEY)) return
  url.searchParams.delete(UI_VERSION_QUERY_KEY)
  window.history.replaceState(window.history.state, '', `${url.pathname}${url.search}${url.hash}`)
}

export function useUiVersion(subjectValue?: MaybeRefOrGetter<string | number | null | undefined>): {
  uiVersion: Ref<UiVersion>
  useLegacyUi: () => void
  useV2Ui: () => void
} {
  const mode = normalizeRolloutMode(import.meta.env.VITE_UI_V2_ROLLOUT_MODE)
  const percentage = normalizePercentage(import.meta.env.VITE_UI_V2_ROLLOUT_PERCENT)
  const subject = computed(() => String(toValue(subjectValue) ?? 'anonymous'))
  const uiVersion = ref<UiVersion>('legacy')

  const resolveForSubject = (currentSubject: string) => {
    const queryPreference = getQueryPreference()
    const storageKey = getStorageKey(currentSubject)
    const storedPreference = typeof localStorage === 'undefined'
      ? null
      : normalizeUiVersion(localStorage.getItem(storageKey))

    uiVersion.value = resolveUiVersion({
      mode,
      percentage,
      queryPreference,
      storedPreference,
      subject: currentSubject,
    })

    if (typeof localStorage !== 'undefined' && queryPreference) {
      const permittedPreference = mode === 'off' && queryPreference === 'v2' ? 'legacy' : queryPreference
      localStorage.setItem(storageKey, permittedPreference)
    }
  }

  watch(subject, resolveForSubject, { immediate: true, flush: 'sync' })

  const setUiVersion = (version: UiVersion) => {
    const nextVersion = mode === 'off' && version === 'v2' ? 'legacy' : version
    uiVersion.value = nextVersion
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(getStorageKey(subject.value), nextVersion)
    }
    removeUiVersionQuery()
  }

  return {
    uiVersion,
    useLegacyUi: () => setUiVersion('legacy'),
    useV2Ui: () => setUiVersion('v2'),
  }
}
