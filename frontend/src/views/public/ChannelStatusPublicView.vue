<template>
  <div
    class="monitor-public min-h-screen overflow-hidden bg-gradient-to-br from-gray-50 via-primary-50/35 to-cyan-50/25 text-gray-950 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950 dark:text-white"
    :class="{ 'monitor-public-dark': isDark }"
  >
    <div class="monitor-public-bg" aria-hidden="true">
      <div class="monitor-public-blob monitor-public-blob-a"></div>
      <div class="monitor-public-blob monitor-public-blob-b"></div>
      <div class="monitor-public-bg-grid"></div>
    </div>

    <header class="sticky top-0 z-40 px-4 py-3 sm:px-6">
      <nav class="monitor-public-nav mx-auto flex max-w-7xl items-center justify-between gap-4">
        <RouterLink to="/home" class="flex min-w-0 items-center gap-2.5">
          <span class="h-9 w-9 overflow-hidden rounded-xl bg-white shadow-md ring-1 ring-gray-200/70 dark:bg-dark-800 dark:ring-dark-700">
            <img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" class="h-full w-full object-contain" />
          </span>
          <span class="monitor-public-brand truncate text-base font-black tracking-tight text-gray-950 dark:text-white sm:text-lg">
            {{ siteName }}
          </span>
        </RouterLink>

        <div class="flex items-center gap-2 sm:gap-3">
          <RouterLink to="/home" class="monitor-public-nav-link hidden sm:inline-flex">
            {{ t('docsGuide.nav.home') }}
          </RouterLink>
          <RouterLink to="/docs" class="monitor-public-nav-link hidden sm:inline-flex">
            {{ t('home.guide') }}
          </RouterLink>
          <LocaleSwitcher />
          <button
            type="button"
            class="monitor-public-icon-button"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <RouterLink :to="isAuthenticated ? dashboardPath : '/login'" class="monitor-public-dashboard-link">
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </RouterLink>
        </div>
      </nav>
    </header>

    <main class="relative z-10 mx-auto max-w-7xl px-4 pb-16 pt-8 sm:px-6 lg:pb-24 lg:pt-10">
      <section class="monitor-public-head">
        <div class="monitor-public-copy">
          <div class="monitor-public-eyebrow">{{ t('home.statusPreview.eyebrow') }}</div>
          <h1 class="monitor-public-title">{{ t('home.statusPreview.title') }}</h1>
          <p class="monitor-public-description">
            {{ t('home.statusPreview.description') }}
          </p>
        </div>

        <div class="monitor-public-actions">
          <RouterLink to="/docs" class="btn btn-secondary px-5 py-2.5 text-sm">
            <Icon name="book" size="md" />
            {{ t('home.statusPreview.guideLink') }}
          </RouterLink>
        </div>
      </section>

      <section class="monitor-public-overview">
        <div class="monitor-public-overview-main">
          <div class="monitor-public-overview-label">{{ t('channelStatus.title') }}</div>
          <div class="monitor-public-overview-value">{{ overallLabel }}</div>
        </div>

        <div class="monitor-public-overview-meta">
          <div class="monitor-public-overview-chip" :class="overallChipClass">
            <span class="monitor-public-overview-dot"></span>
            {{ overallSummaryLabel }}
          </div>
          <div class="monitor-public-meta-pill">{{ activeWindowLabel }}</div>
          <div class="monitor-public-meta-pill">{{ t('monitorCommon.pollEvery', { n: autoRefresh.intervalSeconds.value }) }}</div>
          <div class="monitor-public-meta-pill">{{ t('common.autoRefresh.countdown', { seconds: countdown }) }}</div>
        </div>
      </section>

      <section v-if="loadError" class="monitor-public-alert">
        <div class="monitor-public-alert-icon">
          <Icon name="exclamationTriangle" size="md" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="monitor-public-alert-title">{{ t('channelStatus.publicLoadFailedTitle') }}</p>
          <p class="monitor-public-alert-description">{{ loadError }}</p>
        </div>
        <button type="button" class="monitor-public-alert-action" :disabled="loading" @click="manualReload">
          <Icon name="refresh" size="sm" />
          {{ t('common.refresh') }}
        </button>
      </section>

      <section class="monitor-public-toolbar">
        <div
          role="tablist"
          class="inline-flex rounded-2xl border border-gray-200/70 bg-white/85 p-1 shadow-sm dark:border-dark-700/70 dark:bg-dark-800/75"
        >
          <button
            v-for="opt in windowOptions"
            :key="opt.value"
            type="button"
            class="monitor-public-tab"
            :class="{ 'is-active': currentWindow === opt.value }"
            @click="handleWindowChange(opt.value)"
          >
            {{ opt.label }}
          </button>
        </div>

        <button type="button" class="monitor-public-refresh" :disabled="loading" @click="manualReload">
          <Icon name="refresh" size="sm" />
          {{ t('common.refresh') }}
        </button>
      </section>

      <section v-if="loading && items.length === 0 && !loadError" class="monitor-public-grid">
        <article
          v-for="idx in 6"
          :key="idx"
          class="monitor-public-card monitor-public-card-skeleton animate-pulse"
        >
          <div class="flex items-start gap-3">
            <div class="h-11 w-11 rounded-2xl bg-primary-100 dark:bg-primary-950/40"></div>
            <div class="flex-1 space-y-2">
              <div class="h-4 w-28 rounded bg-gray-200 dark:bg-dark-700"></div>
              <div class="h-3 w-36 rounded bg-gray-100 dark:bg-dark-800"></div>
            </div>
            <div class="h-7 w-16 rounded-full bg-emerald-100 dark:bg-emerald-500/15"></div>
          </div>
          <div class="mt-5 grid grid-cols-3 gap-2">
            <div class="monitor-public-skeleton-metric"></div>
            <div class="monitor-public-skeleton-metric"></div>
            <div class="monitor-public-skeleton-metric"></div>
          </div>
          <div class="mt-5 h-2 rounded-full bg-gray-100 dark:bg-dark-800"></div>
        </article>
      </section>

      <section v-else-if="!loadError && items.length === 0" class="monitor-public-empty">
        <div class="monitor-public-empty-icon">
          <Icon name="chart" size="lg" />
        </div>
        <div>
          <p class="monitor-public-empty-title">{{ t('channelStatus.empty.title') }}</p>
          <p class="monitor-public-empty-description">{{ t('channelStatus.empty.description') }}</p>
        </div>
      </section>

      <section v-else class="monitor-public-grid">
        <article
          v-for="item in items"
          :key="item.id"
          class="monitor-public-card"
          @click="openDetail(item)"
        >
          <div class="monitor-public-card-glow"></div>

          <div class="monitor-public-card-head">
            <span
              class="monitor-public-provider-mark"
              :class="[providerGradient(item.provider), providerTintClass(item.provider)]"
            >
              <ProviderIcon :provider="item.provider" :size="22" />
            </span>

            <div class="min-w-0 flex-1">
              <div class="monitor-public-card-title-row">
                <div class="min-w-0">
                  <div class="monitor-public-card-name">{{ item.name }}</div>
                  <div class="monitor-public-card-provider-line">
                    <span class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-bold" :class="providerBadgeClass(item.provider)">
                      {{ providerLabel(item.provider) }}
                    </span>
                    <span class="truncate">{{ item.primary_model }}</span>
                  </div>
                </div>

                <span class="monitor-public-status-badge" :class="statusBadgeClass(item.primary_status)">
                  {{ statusLabel(item.primary_status) }}
                </span>
              </div>
            </div>
          </div>

          <div class="monitor-public-metrics">
            <div class="monitor-public-metric-card">
              <span class="monitor-public-metric-label">{{ t('monitorCommon.dialogLatency') }}</span>
              <strong>{{ metricValue(item.primary_latency_ms, 'ms') }}</strong>
            </div>
            <div class="monitor-public-metric-card">
              <span class="monitor-public-metric-label">Ping</span>
              <strong>{{ metricValue(item.primary_ping_latency_ms, 'ms') }}</strong>
            </div>
            <div class="monitor-public-metric-card">
              <span class="monitor-public-metric-label">{{ availabilityShortLabel }}</span>
              <strong>{{ metricPercent(item, currentWindow) }}</strong>
            </div>
          </div>

          <div class="monitor-public-timeline">
            <span
              v-for="(point, index) in timelinePoints(item)"
              :key="`${item.id}-${index}`"
              class="monitor-public-timeline-slot"
              :class="timelineSlotClass(point.status)"
            ></span>
          </div>

          <div class="monitor-public-card-foot">
            <span>{{ extraModelsLabel(item.extra_models.length) }}</span>
            <span>{{ relativeUpdatedAt(item) }}</span>
          </div>
        </article>
      </section>

      <section class="monitor-public-bottom-cta">
        <div>
          <p class="monitor-public-bottom-title">{{ t('docsGuide.bottom.title') }}</p>
          <p class="monitor-public-bottom-description">{{ t('docsGuide.bottom.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <RouterLink to="/docs" class="btn btn-secondary px-5 py-2.5 text-sm">
            <Icon name="book" size="md" />
            {{ t('home.guide') }}
          </RouterLink>
          <RouterLink :to="isAuthenticated ? dashboardPath : '/register'" class="btn btn-primary px-5 py-2.5 text-sm shadow-lg shadow-primary-500/30">
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            <Icon name="arrowRight" size="sm" />
          </RouterLink>
        </div>
      </section>

      <MonitorDetailDialog
        :show="showDetail"
        :monitor-id="detailTarget?.id ?? null"
        :title="detailTitle"
        @close="closeDetail"
      />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'
import MonitorDetailDialog from '@/components/user/MonitorDetailDialog.vue'
import { listPublicChannelMonitors, getPublicChannelMonitorStatus, type PublicMonitorDetail, type PublicMonitorTimelinePoint, type PublicMonitorView } from '@/api/publicChannelMonitor'
import { useAutoRefresh } from '@/composables/useAutoRefresh'
import { useChannelMonitorFormat, providerGradient } from '@/composables/useChannelMonitorFormat'
import { DEFAULT_INTERVAL_SECONDS, PROVIDER_ANTHROPIC, PROVIDER_GEMINI, PROVIDER_OPENAI, STATUS_DEGRADED, STATUS_ERROR, STATUS_FAILED, STATUS_OPERATIONAL } from '@/constants/channelMonitor'
import { normalizeSiteName } from '@/utils/siteBrand'
import { extractApiErrorMessage } from '@/utils/apiError'

type MonitorWindow = '7d' | '15d' | '30d'
type OverallStatus = 'operational' | 'degraded' | 'unavailable'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const { providerBadgeClass, providerLabel, statusBadgeClass, statusLabel, formatRelativeTime } = useChannelMonitorFormat()

const items = ref<PublicMonitorView[]>([])
const loading = ref(false)
const loadError = ref('')
const currentWindow = ref<MonitorWindow>('7d')
const detailCache = reactive<Record<number, PublicMonitorDetail>>({})
const showDetail = ref(false)
const detailTarget = ref<PublicMonitorView | null>(null)
const isDark = ref(document.documentElement.classList.contains('dark'))
let abortController: AbortController | null = null

const siteName = computed(() => normalizeSiteName(appStore.cachedPublicSettings?.site_name || appStore.siteName))
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))

const autoRefresh = useAutoRefresh({
  storageKey: 'channel-status-public-auto-refresh',
  intervals: [30, 60, 120] as const,
  defaultInterval: DEFAULT_INTERVAL_SECONDS,
  onRefresh: () => reload(true),
  shouldPause: () => document.hidden || loading.value,
})
const countdown = autoRefresh.countdown

const windowOptions = computed(() => [
  { value: '7d' as const, label: t('channelStatus.windowTab.7d') },
  { value: '15d' as const, label: t('channelStatus.windowTab.15d') },
  { value: '30d' as const, label: t('channelStatus.windowTab.30d') },
])

const overallStatus = computed<OverallStatus>(() => {
  if (loadError.value && items.value.length === 0) return 'unavailable'
  if (items.value.length === 0) return 'operational'
  for (const it of items.value) {
    if (it.primary_status === STATUS_FAILED || it.primary_status === STATUS_ERROR) return 'degraded'
    if (it.primary_status !== STATUS_OPERATIONAL) return 'degraded'
  }
  return 'operational'
})

const overallLabel = computed(() => t(`channelStatus.overall.${overallStatus.value}`))
const overallSummaryLabel = computed(() => {
  if (overallStatus.value === 'unavailable') return t('monitorCommon.status.error')
  return t(`monitorCommon.status.${overallStatus.value}`)
})
const activeWindowLabel = computed(() => windowOptions.value.find((opt) => opt.value === currentWindow.value)?.label || '7d')
const availabilityShortLabel = computed(() => `${activeWindowLabel.value}${t('monitorCommon.availabilityPrefix') === 'Availability' ? ' Availability' : '可用率'}`)

const overallChipClass = computed(() =>
  overallStatus.value === 'operational'
    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
    : overallStatus.value === 'degraded'
      ? 'bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300'
      : 'bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-300'
)

const detailTitle = computed(() => detailTarget.value?.name || t('channelStatus.detailTitle'))

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

async function reload(silent = false) {
  if (abortController) abortController.abort()
  const ctrl = new AbortController()
  abortController = ctrl
  if (!silent) loading.value = true
  if (!silent) loadError.value = ''
  try {
    const res = await listPublicChannelMonitors({ signal: ctrl.signal })
    if (ctrl.signal.aborted || abortController !== ctrl) return
    items.value = res.items || []
    loadError.value = ''
  } catch (err: unknown) {
    const e = err as { name?: string; code?: string }
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') return
    loadError.value = extractApiErrorMessage(err, t('channelStatus.loadError'))
    if (!silent) appStore.showError(loadError.value)
  } finally {
    if (abortController === ctrl) {
      if (!silent) loading.value = false
      countdown.value = DEFAULT_INTERVAL_SECONDS
      abortController = null
    }
  }
}

async function manualReload() {
  await reload(false)
  if (currentWindow.value !== '7d') {
    await Promise.all(items.value.map((it) => loadDetail(it.id, true)))
  }
}

async function loadDetail(id: number, force = false) {
  if (!force && detailCache[id]) return
  try {
    detailCache[id] = await getPublicChannelMonitorStatus(id)
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('channelStatus.detailLoadError')))
  }
}

async function ensureDetailsForWindow() {
  if (currentWindow.value === '7d') return
  await Promise.all(items.value.map((it) => loadDetail(it.id)))
}

async function handleWindowChange(value: MonitorWindow) {
  currentWindow.value = value
  await ensureDetailsForWindow()
}

function openDetail(row: PublicMonitorView) {
  detailTarget.value = row
  showDetail.value = true
}

function closeDetail() {
  showDetail.value = false
  detailTarget.value = null
}

function providerTintClass(provider: string): string {
  switch (provider) {
    case PROVIDER_OPENAI:
      return 'text-emerald-600 dark:text-emerald-300'
    case PROVIDER_ANTHROPIC:
      return 'text-orange-600 dark:text-orange-300'
    case PROVIDER_GEMINI:
      return 'text-sky-600 dark:text-sky-300'
    default:
      return 'text-gray-500 dark:text-gray-300'
  }
}

function currentAvailability(item: PublicMonitorView, window: MonitorWindow): number | null {
  if (window === '7d') return item.availability_7d ?? null
  const detail = detailCache[item.id]
  if (!detail) return null
  const primary = detail.models.find((m) => m.model === item.primary_model)
  if (!primary) return null
  return window === '15d' ? primary.availability_15d ?? null : primary.availability_30d ?? null
}

function metricPercent(item: PublicMonitorView, window: MonitorWindow): string {
  const value = currentAvailability(item, window)
  if (value == null || Number.isNaN(value)) return '--'
  return `${value.toFixed(2)}%`
}

function metricValue(value: number | null | undefined, unit: string): string {
  if (value == null) return `-- ${unit}`
  return `${Math.round(value)} ${unit}`
}

function timelinePoints(item: PublicMonitorView): PublicMonitorTimelinePoint[] {
  const points = [...(item.timeline || [])].reverse().slice(0, 24)
  if (points.length >= 24) return points
  return [
    ...Array.from({ length: 24 - points.length }, () => ({
      status: '' as any,
      latency_ms: null,
      ping_latency_ms: null,
      checked_at: '',
    })),
    ...points,
  ]
}

function timelineSlotClass(status: string): string {
  switch (status) {
    case STATUS_DEGRADED:
      return 'is-degraded'
    case STATUS_FAILED:
      return 'is-failed'
    case STATUS_ERROR:
      return 'is-error'
    case STATUS_OPERATIONAL:
      return 'is-operational'
    default:
      return 'is-empty'
  }
}

function extraModelsLabel(count: number): string {
  if (count <= 0) return t('monitorCommon.extraModelsEmpty')
  return t('monitorCommon.extraModelsCount', { n: count })
}

function relativeUpdatedAt(item: PublicMonitorView): string {
  const latest = item.timeline?.[0]?.checked_at
  if (!latest) return '--'
  return formatRelativeTime(latest)
}

onMounted(() => {
  void reload(false)
  if (appStore.cachedPublicSettings?.channel_monitor_enabled !== false) {
    autoRefresh.setEnabled(true)
  }
})

onBeforeUnmount(() => {
  if (abortController) abortController.abort()
})
</script>

<style scoped>
.monitor-public {
  position: relative;
}

.monitor-public-bg {
  position: absolute;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
}

.monitor-public-blob {
  position: absolute;
  border-radius: 9999px;
  filter: blur(12px);
}

.monitor-public-blob-a {
  top: 5rem;
  left: -4rem;
  width: 16rem;
  height: 16rem;
  background: radial-gradient(circle, rgba(45, 212, 191, 0.24), transparent 70%);
}

.monitor-public-blob-b {
  top: 16rem;
  right: -5rem;
  width: 20rem;
  height: 20rem;
  background: radial-gradient(circle, rgba(103, 232, 249, 0.2), transparent 72%);
}

.monitor-public-bg-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.22) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.22) 1px, transparent 1px);
  background-size: 2.8rem 2.8rem;
  opacity: 0.15;
}

.monitor-public-nav {
  padding: 0.35rem 0;
}

.monitor-public-brand {
  font-style: italic;
  letter-spacing: -0.045em;
  transform: skewX(-6deg);
}

.monitor-public-nav-link,
.monitor-public-icon-button,
.monitor-public-dashboard-link {
  align-items: center;
  border-radius: 9999px;
  color: #475569;
  display: inline-flex;
  font-size: 0.82rem;
  font-weight: 800;
  justify-content: center;
  min-height: 2.25rem;
  transition:
    background-color 180ms ease,
    box-shadow 180ms ease,
    color 180ms ease,
    transform 180ms ease;
}

.monitor-public-nav-link {
  padding: 0 0.8rem;
}

.monitor-public-icon-button {
  width: 2.25rem;
}

.monitor-public-dashboard-link {
  background: #0f172a;
  color: #fff;
  padding: 0 1rem;
}

.monitor-public-nav-link:hover,
.monitor-public-icon-button:hover {
  background: rgba(255, 255, 255, 0.72);
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.1);
  color: #0f766e;
  transform: translateY(-1px);
}

.monitor-public-dashboard-link:hover {
  background: #1f2937;
  transform: translateY(-1px);
}

.monitor-public-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 1.5rem;
}

.monitor-public-copy {
  max-width: 40rem;
}

.monitor-public-eyebrow {
  display: inline-flex;
  align-items: center;
  border-radius: 9999px;
  border: 1px solid rgba(20, 184, 166, 0.14);
  background: rgba(255, 255, 255, 0.7);
  padding: 0.44rem 0.78rem;
  color: #0f766e;
  font-size: 0.72rem;
  font-weight: 900;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}

.monitor-public-title {
  margin-top: 1rem;
  font-size: clamp(1.9rem, 3.4vw, 2.75rem);
  line-height: 1.02;
  letter-spacing: -0.045em;
  font-weight: 900;
  color: #0f172a;
}

.monitor-public-description {
  margin-top: 0.9rem;
  max-width: 34rem;
  color: #64748b;
  font-size: 0.98rem;
  line-height: 1.75;
}

.monitor-public-overview {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  margin-top: 1.4rem;
  padding: 1rem 1.1rem;
  border-radius: 1.4rem;
  border: 1px solid rgba(20, 184, 166, 0.1);
  background: rgba(255, 255, 255, 0.78);
  box-shadow: 0 14px 34px rgba(15, 23, 42, 0.06);
  backdrop-filter: blur(18px);
}

.monitor-public-alert {
  display: flex;
  align-items: center;
  gap: 0.85rem;
  margin-top: 1rem;
  padding: 0.95rem 1rem;
  border-radius: 1.15rem;
  border: 1px solid rgba(245, 158, 11, 0.24);
  background: rgba(255, 251, 235, 0.82);
  color: #92400e;
}

.monitor-public-alert-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  width: 2.4rem;
  height: 2.4rem;
  border-radius: 0.85rem;
  background: rgba(245, 158, 11, 0.14);
}

.monitor-public-alert-title {
  font-size: 0.9rem;
  font-weight: 900;
}

.monitor-public-alert-description {
  margin-top: 0.18rem;
  color: #a16207;
  font-size: 0.8rem;
  line-height: 1.55;
}

.monitor-public-alert-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  min-height: 2.15rem;
  padding: 0 0.8rem;
  border-radius: 9999px;
  background: #0f172a;
  color: #fff;
  font-size: 0.78rem;
  font-weight: 800;
}

.monitor-public-overview-main {
  min-width: 0;
}

.monitor-public-overview-label {
  color: #64748b;
  font-size: 0.72rem;
  font-weight: 800;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.monitor-public-overview-value {
  margin-top: 0.28rem;
  color: #0f172a;
  font-size: 1.35rem;
  font-weight: 900;
  letter-spacing: -0.04em;
}

.monitor-public-overview-meta {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 0.6rem;
}

.monitor-public-overview-chip,
.monitor-public-meta-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  min-height: 2.15rem;
  padding: 0 0.8rem;
  border-radius: 9999px;
  font-size: 0.74rem;
  font-weight: 800;
}

.monitor-public-meta-pill {
  border: 1px solid rgba(226, 232, 240, 0.88);
  background: rgba(248, 250, 252, 0.85);
  color: #475569;
}

.monitor-public-overview-dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 9999px;
  background: currentColor;
}

.monitor-public-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  margin-top: 1rem;
}

.monitor-public-tab {
  min-height: 2.2rem;
  padding: 0 0.9rem;
  border-radius: 1rem;
  color: #64748b;
  font-size: 0.84rem;
  font-weight: 700;
  transition: all 180ms ease;
}

.monitor-public-tab.is-active {
  background: #ffffff;
  color: #0f172a;
  box-shadow: 0 10px 24px rgba(15, 23, 42, 0.08);
}

.monitor-public-refresh {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  min-height: 2.3rem;
  padding: 0 0.95rem;
  border-radius: 9999px;
  border: 1px solid rgba(226, 232, 240, 0.88);
  background: rgba(255, 255, 255, 0.82);
  color: #475569;
  font-size: 0.82rem;
  font-weight: 700;
}

.monitor-public-grid {
  display: grid;
  gap: 0.85rem;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin-top: 1rem;
}

.monitor-public-card {
  position: relative;
  overflow: hidden;
  border-radius: 1.25rem;
  border: 1px solid rgba(20, 184, 166, 0.1);
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.95), rgba(241, 253, 249, 0.9)),
    rgba(255, 255, 255, 0.92);
  box-shadow: 0 14px 34px rgba(15, 23, 42, 0.07);
  padding: 0.9rem;
  cursor: pointer;
  transition:
    transform 200ms ease,
    box-shadow 200ms ease,
    border-color 200ms ease;
}

.monitor-public-card:hover {
  transform: translateY(-3px);
  box-shadow: 0 24px 52px rgba(15, 23, 42, 0.12);
  border-color: rgba(20, 184, 166, 0.22);
}

.monitor-public-card-glow {
  position: absolute;
  inset: auto -16% -36% auto;
  width: 9rem;
  height: 9rem;
  border-radius: 9999px;
  background: radial-gradient(circle, rgba(103, 232, 249, 0.2), transparent 72%);
  pointer-events: none;
}

.monitor-public-card-head {
  display: flex;
  align-items: flex-start;
  gap: 0.75rem;
}

.monitor-public-provider-mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  width: 2.6rem;
  height: 2.6rem;
  border-radius: 0.9rem;
  box-shadow: 0 14px 26px rgba(20, 184, 166, 0.14);
}

.monitor-public-card-title-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.6rem;
}

.monitor-public-card-name {
  color: #0f172a;
  font-size: 0.95rem;
  line-height: 1.2;
  font-weight: 900;
  letter-spacing: -0.03em;
}

.monitor-public-card-provider-line {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  margin-top: 0.38rem;
  min-width: 0;
  color: #64748b;
  font-size: 0.75rem;
  font-weight: 600;
}

.monitor-public-status-badge {
  flex: 0 0 auto;
  padding: 0.45rem 0.68rem;
  border-radius: 9999px;
  font-size: 0.68rem;
  line-height: 1;
  font-weight: 800;
}

.monitor-public-metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.55rem;
  margin-top: 0.8rem;
}

.monitor-public-metric-card {
  border-radius: 0.82rem;
  border: 1px solid rgba(20, 184, 166, 0.06);
  background: rgba(248, 250, 252, 0.8);
  padding: 0.58rem 0.64rem;
}

.monitor-public-metric-label {
  display: block;
  color: #64748b;
  font-size: 0.66rem;
  font-weight: 700;
}

.monitor-public-metric-card strong {
  display: inline-flex;
  align-items: flex-end;
  margin-top: 0.34rem;
  color: #052f2c;
  font-size: 0.92rem;
  line-height: 1;
  font-weight: 900;
  letter-spacing: -0.04em;
}

.monitor-public-timeline {
  display: grid;
  grid-template-columns: repeat(24, minmax(0, 1fr));
  gap: 2px;
  margin-top: 0.8rem;
}

.monitor-public-timeline-slot {
  height: 0.42rem;
  border-radius: 9999px;
  background: rgba(203, 213, 225, 0.56);
}

.monitor-public-timeline-slot.is-operational {
  background: linear-gradient(90deg, #14b8a6, #34d399);
}

.monitor-public-timeline-slot.is-degraded {
  background: linear-gradient(90deg, #f59e0b, #fbbf24);
}

.monitor-public-timeline-slot.is-failed,
.monitor-public-timeline-slot.is-error {
  background: linear-gradient(90deg, #f97316, #ef4444);
}

.monitor-public-timeline-slot.is-empty {
  background: rgba(226, 232, 240, 0.82);
}

.monitor-public-card-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-top: 0.58rem;
  color: #6b8b87;
  font-size: 0.68rem;
  font-weight: 700;
}

.monitor-public-empty {
  display: flex;
  align-items: center;
  gap: 1rem;
  margin-top: 1rem;
  padding: 1.3rem;
  border-radius: 1.4rem;
  border: 1px dashed rgba(20, 184, 166, 0.22);
  background: rgba(255, 255, 255, 0.7);
}

.monitor-public-empty-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 3rem;
  height: 3rem;
  border-radius: 1rem;
  color: #0f766e;
  background: rgba(20, 184, 166, 0.12);
}

.monitor-public-empty-title {
  color: #0f172a;
  font-size: 1rem;
  font-weight: 900;
}

.monitor-public-empty-description {
  margin-top: 0.35rem;
  color: #64748b;
  font-size: 0.88rem;
  line-height: 1.7;
}

.monitor-public-card-skeleton {
  pointer-events: none;
}

.monitor-public-skeleton-metric {
  height: 4rem;
  border-radius: 1rem;
  background: rgba(226, 232, 240, 0.44);
}

.monitor-public-bottom-cta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  margin-top: 1.4rem;
  padding: 1.15rem 1.2rem;
  border-radius: 1.45rem;
  border: 1px solid rgba(20, 184, 166, 0.08);
  background: rgba(255, 255, 255, 0.72);
}

.monitor-public-dark .monitor-public-blob-a {
  background: radial-gradient(circle, rgba(20, 184, 166, 0.16), transparent 70%);
}

.monitor-public-dark .monitor-public-blob-b {
  background: radial-gradient(circle, rgba(56, 189, 248, 0.12), transparent 72%);
}

.monitor-public-dark .monitor-public-bg-grid {
  background-image:
    linear-gradient(rgba(45, 212, 191, 0.06) 1px, transparent 1px),
    linear-gradient(90deg, rgba(45, 212, 191, 0.06) 1px, transparent 1px);
}

.monitor-public-dark .monitor-public-nav-link,
.monitor-public-dark .monitor-public-icon-button {
  color: #cbd5e1;
}

.monitor-public-dark .monitor-public-nav-link:hover,
.monitor-public-dark .monitor-public-icon-button:hover {
  background: rgba(15, 23, 42, 0.72);
  color: #5eead4;
}

.monitor-public-dark .monitor-public-dashboard-link {
  background: #f8fafc;
  color: #0f172a;
}

.monitor-public-dark .monitor-public-eyebrow,
.monitor-public-dark .monitor-public-overview,
.monitor-public-dark .monitor-public-refresh,
.monitor-public-dark .monitor-public-empty,
.monitor-public-dark .monitor-public-bottom-cta {
  border-color: rgba(45, 212, 191, 0.12);
  background: rgba(15, 23, 42, 0.72);
}

.monitor-public-dark .monitor-public-title,
.monitor-public-dark .monitor-public-overview-value,
.monitor-public-dark .monitor-public-card-name,
.monitor-public-dark .monitor-public-empty-title,
.monitor-public-dark .monitor-public-bottom-title {
  color: #f8fafc;
}

.monitor-public-dark .monitor-public-description,
.monitor-public-dark .monitor-public-overview-label,
.monitor-public-dark .monitor-public-card-provider-line,
.monitor-public-dark .monitor-public-metric-label,
.monitor-public-dark .monitor-public-empty-description,
.monitor-public-dark .monitor-public-bottom-description {
  color: #94a3b8;
}

.monitor-public-dark .monitor-public-meta-pill,
.monitor-public-dark .monitor-public-tab,
.monitor-public-dark .monitor-public-card-foot {
  border-color: rgba(51, 65, 85, 0.9);
  background: rgba(15, 23, 42, 0.58);
  color: #cbd5e1;
}

.monitor-public-dark .monitor-public-tab.is-active {
  background: rgba(20, 184, 166, 0.18);
  color: #ccfbf1;
  box-shadow: none;
}

.monitor-public-dark .monitor-public-card {
  border-color: rgba(45, 212, 191, 0.1);
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.94), rgba(8, 47, 73, 0.58)),
    rgba(15, 23, 42, 0.92);
  box-shadow: 0 18px 46px rgba(0, 0, 0, 0.28);
}

.monitor-public-dark .monitor-public-metric-card,
.monitor-public-dark .monitor-public-skeleton-metric {
  border-color: rgba(45, 212, 191, 0.08);
  background: rgba(2, 6, 23, 0.42);
}

.monitor-public-dark .monitor-public-metric-card strong {
  color: #e0f2fe;
}

.monitor-public-dark .monitor-public-timeline-slot.is-empty {
  background: rgba(51, 65, 85, 0.82);
}

.monitor-public-dark .monitor-public-alert {
  border-color: rgba(245, 158, 11, 0.24);
  background: rgba(69, 26, 3, 0.42);
  color: #fcd34d;
}

.monitor-public-dark .monitor-public-alert-icon {
  background: rgba(245, 158, 11, 0.15);
}

.monitor-public-dark .monitor-public-alert-description {
  color: #fbbf24;
}

.monitor-public-dark .monitor-public-alert-action {
  background: #f8fafc;
  color: #0f172a;
}

.monitor-public-bottom-title {
  color: #0f172a;
  font-size: 1.05rem;
  font-weight: 900;
  letter-spacing: -0.03em;
}

.monitor-public-bottom-description {
  margin-top: 0.35rem;
  color: #64748b;
  font-size: 0.92rem;
  line-height: 1.7;
}

@media (max-width: 1279px) {
  .monitor-public-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 1023px) {
  .monitor-public-head,
  .monitor-public-overview,
  .monitor-public-toolbar,
  .monitor-public-alert,
  .monitor-public-bottom-cta {
    flex-direction: column;
    align-items: stretch;
  }

  .monitor-public-overview-meta {
    justify-content: flex-start;
  }

  .monitor-public-grid {
    grid-template-columns: 1fr;
  }

  .monitor-public-title {
    font-size: clamp(1.75rem, 7vw, 2.3rem);
  }
}
</style>
