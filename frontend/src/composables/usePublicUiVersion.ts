import { computed, ref, toValue, watch, type MaybeRefOrGetter, type Ref } from 'vue'

export type PublicUiVersion = 'legacy' | 'v2'
export type PublicUiRolloutMode = 'off' | 'preview' | 'percentage' | 'full'

interface ResolvePublicUiVersionOptions {
  mode: PublicUiRolloutMode
  percentage: number
  queryPreference: PublicUiVersion | null
  storedPreference: PublicUiVersion | null
  subject: string
}

const PUBLIC_UI_QUERY_KEY = 'public_ui'
const PUBLIC_UI_STORAGE_PREFIX = 'sub2api:public-ui-version'
const PUBLIC_UI_ANONYMOUS_SUBJECT_KEY = 'sub2api:public-ui-anonymous-subject'

function normalizePublicUiVersion(value: unknown): PublicUiVersion | null {
  if (value === 'v2') return 'v2'
  if (value === 'legacy' || value === 'v1') return 'legacy'
  return null
}

function normalizeRolloutMode(value: unknown): PublicUiRolloutMode {
  if (value === 'off' || value === 'percentage' || value === 'full') return value
  return 'preview'
}

function normalizePercentage(value: unknown): number {
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) return 0
  return Math.min(100, Math.max(0, Math.round(parsed)))
}

export function publicUiV2CohortBucket(subject: string): number {
  let hash = 2166136261
  for (let index = 0; index < subject.length; index += 1) {
    hash ^= subject.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return (hash >>> 0) % 100
}

export function resolvePublicUiVersion(options: ResolvePublicUiVersionOptions): PublicUiVersion {
  if (options.mode === 'off') return 'legacy'
  if (options.queryPreference) return options.queryPreference
  if (options.storedPreference) return options.storedPreference
  if (options.mode === 'full') return 'v2'
  if (options.mode === 'percentage') {
    return publicUiV2CohortBucket(options.subject) < options.percentage ? 'v2' : 'legacy'
  }
  return 'legacy'
}

function createAnonymousSubject(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

export function getStablePublicUiSubject(): string {
  if (typeof localStorage === 'undefined') return 'anonymous'
  const stored = localStorage.getItem(PUBLIC_UI_ANONYMOUS_SUBJECT_KEY)?.trim()
  if (stored) return stored

  const subject = createAnonymousSubject()
  localStorage.setItem(PUBLIC_UI_ANONYMOUS_SUBJECT_KEY, subject)
  return subject
}

function getStorageKey(subject: string): string {
  return `${PUBLIC_UI_STORAGE_PREFIX}:${subject}`
}

function getQueryPreference(): PublicUiVersion | null {
  if (typeof window === 'undefined') return null
  return normalizePublicUiVersion(new URLSearchParams(window.location.search).get(PUBLIC_UI_QUERY_KEY))
}

function removePublicUiQuery(): void {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  if (!url.searchParams.has(PUBLIC_UI_QUERY_KEY)) return
  url.searchParams.delete(PUBLIC_UI_QUERY_KEY)
  window.history.replaceState(window.history.state, '', `${url.pathname}${url.search}${url.hash}`)
}

export function usePublicUiVersion(
  subjectValue?: MaybeRefOrGetter<string | number | null | undefined>,
): {
  publicUiVersion: Ref<PublicUiVersion>
  isPublicUiV2: Readonly<Ref<boolean>>
  useLegacyPublicUi: () => void
  useV2PublicUi: () => void
} {
  const mode = normalizeRolloutMode(import.meta.env.VITE_PUBLIC_UI_V2_ROLLOUT_MODE)
  const percentage = normalizePercentage(import.meta.env.VITE_PUBLIC_UI_V2_ROLLOUT_PERCENT)
  const anonymousSubject = getStablePublicUiSubject()
  const subject = computed(() => String(toValue(subjectValue) ?? anonymousSubject))
  const publicUiVersion = ref<PublicUiVersion>('legacy')
  const initialQueryPreference = getQueryPreference()

  const resolveForSubject = (currentSubject: string) => {
    const storageKey = getStorageKey(currentSubject)
    const storedPreference = typeof localStorage === 'undefined'
      ? null
      : normalizePublicUiVersion(localStorage.getItem(storageKey))

    publicUiVersion.value = resolvePublicUiVersion({
      mode,
      percentage,
      queryPreference: initialQueryPreference,
      storedPreference,
      subject: currentSubject,
    })

    if (typeof localStorage !== 'undefined' && initialQueryPreference) {
      const permittedPreference = mode === 'off' && initialQueryPreference === 'v2'
        ? 'legacy'
        : initialQueryPreference
      localStorage.setItem(storageKey, permittedPreference)
    }
  }

  watch(subject, resolveForSubject, { immediate: true, flush: 'sync' })
  removePublicUiQuery()

  const setPublicUiVersion = (version: PublicUiVersion) => {
    const nextVersion = mode === 'off' && version === 'v2' ? 'legacy' : version
    publicUiVersion.value = nextVersion
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(getStorageKey(subject.value), nextVersion)
    }
    removePublicUiQuery()
  }

  return {
    publicUiVersion,
    isPublicUiV2: computed(() => publicUiVersion.value === 'v2'),
    useLegacyPublicUi: () => setPublicUiVersion('legacy'),
    useV2PublicUi: () => setPublicUiVersion('v2'),
  }
}
