<template>
  <div class="monitor-public min-h-screen overflow-hidden bg-gradient-to-br from-gray-50 via-primary-50/40 to-cyan-50/30 text-gray-950 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950 dark:text-white">
    <div class="monitor-public-bg" aria-hidden="true">
      <div class="monitor-public-blob monitor-public-blob-a"></div>
      <div class="monitor-public-blob monitor-public-blob-b"></div>
      <div class="monitor-public-grid"></div>
    </div>

    <header class="sticky top-0 z-40 px-4 py-3 sm:px-6">
      <nav class="monitor-public-nav mx-auto flex max-w-7xl items-center justify-between gap-4">
        <RouterLink to="/home" class="flex min-w-0 items-center gap-2.5">
          <span class="h-9 w-9 overflow-hidden rounded-xl bg-white shadow-md ring-1 ring-gray-200/70 dark:bg-dark-800 dark:ring-dark-700">
            <img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" class="h-full w-full object-contain" />
          </span>
          <span class="truncate text-base font-black tracking-tight text-gray-950 dark:text-white sm:text-lg">
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
      <section class="grid gap-8 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)] lg:items-start">
        <div class="monitor-public-copy">
          <div class="monitor-public-eyebrow">{{ t('home.statusPreview.eyebrow') }}</div>
          <h1 class="mt-5 text-[2.15rem] font-black leading-[1.02] tracking-[-0.05em] text-gray-950 dark:text-white sm:text-[2.7rem]">
            {{ t('home.statusPreview.title') }}
          </h1>
          <p class="mt-4 text-[1.02rem] leading-7 text-gray-600 dark:text-dark-300">
            {{ t('home.statusPreview.description') }}
          </p>

          <div class="mt-6 flex flex-wrap items-center gap-3">
            <RouterLink to="/docs" class="btn btn-secondary px-5 py-2.5 text-sm">
              <Icon name="book" size="md" />
              {{ t('home.statusPreview.guideLink') }}
            </RouterLink>
            <RouterLink :to="isAuthenticated ? dashboardPath : '/register'" class="btn btn-primary px-5 py-2.5 text-sm shadow-lg shadow-primary-500/30">
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
              <Icon name="arrowRight" size="sm" />
            </RouterLink>
          </div>

          <div class="monitor-public-summary-card">
            <div class="flex items-center justify-between gap-3">
              <div>
                <p class="monitor-public-summary-label">{{ t('channelStatus.title') }}</p>
                <p class="monitor-public-summary-value">{{ overallLabel }}</p>
              </div>
              <div class="monitor-public-summary-chip" :class="overallChipClass">
                {{ overallLabel }}
              </div>
            </div>
            <div class="mt-4 flex items-center justify-between gap-3 text-sm text-gray-500 dark:text-dark-300">
              <span>{{ t('monitorCommon.pollEvery', { n: autoRefresh.intervalSeconds.value }) }}</span>
              <span>{{ t('common.autoRefresh.countdown', { seconds: countdown }) }}</span>
            </div>
          </div>
        </div>

        <div class="monitor-public-panel">
          <MonitorHero
            :overall-status="overallStatus"
            :interval-seconds="DEFAULT_INTERVAL_SECONDS"
            :window="currentWindow"
            :loading="loading"
            :auto-refresh="autoRefresh"
            @update:window="handleWindowChange"
            @refresh="manualReload"
          />

          <MonitorCardGrid
            :items="items"
            :window="currentWindow"
            :countdown-seconds="countdown"
            :loading="loading"
            :detail-cache="detailCache"
            @card-click="openDetail"
          />
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
import MonitorHero, { type MonitorWindow, type OverallStatus } from '@/components/user/monitor/MonitorHero.vue'
import MonitorCardGrid from '@/components/user/monitor/MonitorCardGrid.vue'
import MonitorDetailDialog from '@/components/user/MonitorDetailDialog.vue'
import { listPublicChannelMonitors, getPublicChannelMonitorStatus, type PublicMonitorDetail, type PublicMonitorView } from '@/api/publicChannelMonitor'
import { useAutoRefresh } from '@/composables/useAutoRefresh'
import { DEFAULT_INTERVAL_SECONDS, STATUS_OPERATIONAL } from '@/constants/channelMonitor'
import { normalizeSiteName } from '@/utils/siteBrand'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const items = ref<PublicMonitorView[]>([])
const loading = ref(false)
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

const overallStatus = computed<OverallStatus>(() => {
  if (items.value.length === 0) return 'operational'
  for (const it of items.value) {
    if (it.primary_status === 'failed' || it.primary_status === 'error') return 'degraded'
    if (it.primary_status !== STATUS_OPERATIONAL) return 'degraded'
  }
  return 'operational'
})

const overallLabel = computed(() => t(`channelStatus.overall.${overallStatus.value}`))

const overallChipClass = computed(() =>
  overallStatus.value === 'operational'
    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
    : 'bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300'
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
  try {
    const res = await listPublicChannelMonitors({ signal: ctrl.signal })
    if (ctrl.signal.aborted || abortController !== ctrl) return
    items.value = res.items || []
  } catch (err: unknown) {
    const e = err as { name?: string; code?: string }
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(err, t('channelStatus.loadError')))
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
  background: radial-gradient(circle, rgba(45, 212, 191, 0.26), transparent 70%);
}

.monitor-public-blob-b {
  top: 16rem;
  right: -5rem;
  width: 20rem;
  height: 20rem;
  background: radial-gradient(circle, rgba(103, 232, 249, 0.22), transparent 72%);
}

.monitor-public-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.22) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.22) 1px, transparent 1px);
  background-size: 2.8rem 2.8rem;
  opacity: 0.16;
}

.monitor-public-nav-link {
  color: #475569;
  font-size: 0.84rem;
  font-weight: 700;
}

.monitor-public-icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2.3rem;
  height: 2.3rem;
  border-radius: 0.9rem;
  color: #64748b;
}

.monitor-public-dashboard-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 2.35rem;
  padding: 0 0.95rem;
  border-radius: 9999px;
  background: #0f172a;
  color: #fff;
  font-size: 0.8rem;
  font-weight: 700;
}

.monitor-public-copy {
  position: sticky;
  top: 5.8rem;
}

.monitor-public-eyebrow {
  display: inline-flex;
  align-items: center;
  border-radius: 9999px;
  border: 1px solid rgba(20, 184, 166, 0.14);
  background: rgba(255, 255, 255, 0.66);
  padding: 0.48rem 0.82rem;
  color: #0f766e;
  font-size: 0.72rem;
  font-weight: 900;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}

.monitor-public-panel {
  min-width: 0;
  border-radius: 2rem;
  border: 1px solid rgba(20, 184, 166, 0.1);
  background: rgba(255, 255, 255, 0.68);
  box-shadow: 0 24px 64px rgba(15, 23, 42, 0.1);
  backdrop-filter: blur(24px);
  padding: 1.15rem;
}

.monitor-public-summary-card {
  margin-top: 1.5rem;
  border-radius: 1.4rem;
  border: 1px solid rgba(20, 184, 166, 0.1);
  background: rgba(255, 255, 255, 0.72);
  padding: 1rem 1.05rem;
}

.monitor-public-summary-label {
  color: #64748b;
  font-size: 0.76rem;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.monitor-public-summary-value {
  margin-top: 0.35rem;
  color: #0f172a;
  font-size: 1.18rem;
  font-weight: 900;
  letter-spacing: -0.04em;
}

.monitor-public-summary-chip {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 9999px;
  padding: 0.48rem 0.75rem;
  font-size: 0.72rem;
  font-weight: 900;
  letter-spacing: 0.1em;
}

:deep(.monitor-public-panel .empty-state) {
  min-height: 20rem;
}

@media (max-width: 1023px) {
  .monitor-public-copy {
    position: static;
  }

  .monitor-public-panel {
    padding: 0.9rem;
  }
}
</style>
