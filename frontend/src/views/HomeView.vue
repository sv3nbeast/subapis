<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="homeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- Default Home Page -->
  <div
    v-else
    class="home-page relative flex h-screen min-h-screen flex-col overflow-hidden bg-gradient-to-br from-gray-50 via-primary-50/40 to-cyan-50/30 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950"
    :class="{ 'home-dark': isDark }"
  >
    <div class="home-bg" aria-hidden="true">
      <div class="home-blob home-blob-a"></div>
      <div class="home-blob home-blob-b"></div>
      <div class="home-blob home-blob-c"></div>
      <div class="home-grid"></div>
    </div>

    <!-- Header -->
    <header class="fixed inset-x-0 top-0 z-50 px-4 py-3 sm:px-6">
      <nav class="home-nav mx-auto flex items-center justify-between gap-4">
        <router-link to="/home" class="flex min-w-0 items-center gap-2.5">
          <div class="h-9 w-9 overflow-hidden rounded-xl bg-white shadow-md ring-1 ring-gray-200/70 dark:bg-dark-800 dark:ring-dark-700">
            <img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" class="h-full w-full object-contain" />
          </div>
          <span class="home-brand-name truncate text-base font-black tracking-tight text-gray-950 dark:text-white sm:text-lg">
            {{ siteName }}
          </span>
        </router-link>

        <div class="flex items-center gap-3">
          <button
            type="button"
            class="home-nav-action home-announcement-trigger"
            :class="{ 'is-active': unreadHomeAnnouncements.length > 0 }"
            :title="t('home.announcements.modalTitle')"
            :aria-label="t('home.announcements.modalTitle')"
            @click="openAnnouncementModal"
          >
            <Icon name="bell" size="md" />
            <span
              v-if="unreadHomeAnnouncements.length > 0"
              class="home-announcement-badge"
              :aria-label="t('home.announcements.unreadCount', { count: unreadHomeAnnouncements.length })"
            >
              {{ unreadHomeAnnouncements.length > 9 ? '9+' : unreadHomeAnnouncements.length }}
            </span>
          </button>

          <LocaleSwitcher />

          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>

          <button
            @click="toggleTheme"
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>

          <router-link
            v-if="isAuthenticated"
            :to="dashboardPath"
            class="inline-flex items-center gap-1.5 rounded-full bg-gray-900 py-1 pl-1 pr-2.5 transition-colors hover:bg-gray-800 dark:bg-gray-800 dark:hover:bg-gray-700"
          >
            <span
              class="flex h-5 w-5 items-center justify-center rounded-full bg-gradient-to-br from-primary-400 to-primary-600 text-[10px] font-semibold text-white"
            >
              {{ userInitial }}
            </span>
            <span class="text-xs font-medium text-white">{{ t('home.dashboard') }}</span>
            <svg
              class="h-3 w-3 text-gray-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M4.5 19.5l15-15m0 0H8.25m11.25 0v11.25"
              />
            </svg>
          </router-link>
          <div v-else class="home-auth-actions">
            <router-link to="/login" class="home-auth-login">
              {{ t('home.login') }}
            </router-link>
            <router-link to="/register" class="home-auth-register">
              {{ t('auth.signUp') }}
            </router-link>
          </div>
        </div>
      </nav>
    </header>

    <Teleport to="body">
      <Transition name="home-modal">
        <div
          v-if="announcementModalOpen"
          class="home-announcement-backdrop"
          :class="{ 'is-dark': isDark }"
          @click="closeAnnouncementModal"
        >
          <div class="home-announcement-modal" @click.stop>
            <div class="home-announcement-modal-head">
              <div class="flex min-w-0 items-center gap-3">
                <div class="home-announcement-modal-icon">
                  <Icon name="bell" size="md" />
                </div>
                <div class="min-w-0">
                  <h2 class="text-xl font-black tracking-tight text-gray-950 dark:text-white">
                    {{ t('home.announcements.modalTitle') }}
                  </h2>
                  <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
                    {{ t('home.announcements.modalDescription') }}
                  </p>
                </div>
              </div>

              <div class="home-announcement-head-actions">
                <div class="home-announcement-tabs" role="tablist">
                  <button
                    type="button"
                    role="tab"
                    class="home-announcement-tab"
                    :class="{ 'is-active': announcementTab === 'notifications' }"
                    :aria-selected="announcementTab === 'notifications'"
                    @click="announcementTab = 'notifications'"
                  >
                    {{ t('home.announcements.notifications') }}
                  </button>
                  <button
                    type="button"
                    role="tab"
                    class="home-announcement-tab"
                    :class="{ 'is-active': announcementTab === 'system' }"
                    :aria-selected="announcementTab === 'system'"
                    @click="announcementTab = 'system'"
                  >
                    {{ t('home.announcements.system') }}
                  </button>
                </div>

                <button
                  type="button"
                  class="home-announcement-close"
                  :aria-label="t('common.close')"
                  @click="closeAnnouncementModal"
                >
                  <Icon name="x" size="sm" />
                </button>
              </div>
            </div>

            <div class="home-announcement-modal-body">
              <div v-if="announcementStore.loading" class="home-announcement-loading">
                <div class="home-announcement-spinner"></div>
              </div>

              <div
                v-else-if="activeAnnouncements.length > 0"
                class="home-announcement-timeline"
              >
                <article
                  v-for="item in activeAnnouncements"
                  :key="item.id"
                  class="home-announcement-timeline-item"
                  :class="{ 'is-unread': !item.read_at }"
                >
                  <div class="home-announcement-timeline-dot"></div>
                  <div class="home-announcement-timeline-card">
                    <div class="flex items-start justify-between gap-4">
                      <h3 class="text-base font-black text-gray-950 dark:text-white">
                        {{ item.title }}
                      </h3>
                      <span v-if="!item.read_at" class="home-announcement-unread-pill">
                        {{ t('announcements.unread') }}
                      </span>
                    </div>
                    <p class="mt-2 line-clamp-3 text-sm leading-6 text-gray-600 dark:text-dark-300">
                      {{ plainAnnouncementContent(item.content) }}
                    </p>
                    <time class="mt-3 block text-xs font-semibold text-gray-400 dark:text-dark-500">
                      {{ formatAnnouncementTime(item.created_at) }}
                    </time>
                  </div>
                </article>
              </div>

              <div v-else class="home-announcement-empty-state">
                <div class="home-announcement-empty-illustration">
                  <Icon name="inbox" size="xl" />
                  <span></span>
                </div>
                <p class="mt-5 text-base font-black text-gray-900 dark:text-white">
                  {{ t('home.announcements.empty') }}
                </p>
                <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
                  {{ t('home.announcements.emptyDescription') }}
                </p>
              </div>
            </div>

            <div class="home-announcement-modal-foot">
              <button type="button" class="home-announcement-foot-button" @click="closeAnnouncementToday">
                {{ t('home.announcements.todayClose') }}
              </button>
              <button type="button" class="home-announcement-foot-button is-primary" @click="closeAnnouncementModal">
                {{ t('home.announcements.closeAnnouncement') }}
              </button>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>

    <!-- Main Content -->
    <main ref="snapContainerRef" class="home-snap-container relative z-10 flex-1">
      <section class="home-section home-snap-section home-hero-section px-4 sm:px-6">
        <div class="home-hero-shell mx-auto grid max-w-[72rem] items-center gap-8 lg:grid-cols-[minmax(0,38rem)_minmax(24rem,28rem)] lg:gap-10">
          <div class="home-hero-copy">
            <div class="inline-flex rounded-full border border-primary-200 bg-primary-50/80 px-3.5 py-1.5 text-[0.72rem] font-bold tracking-[0.14em] text-primary-700 shadow-sm dark:border-primary-800/70 dark:bg-primary-950/40 dark:text-primary-300">
              {{ t('home.hero.eyebrow') }}
            </div>
            <h1 class="mt-5 text-[2.34rem] font-black leading-[1.02] tracking-[-0.05em] text-gray-950 dark:text-white sm:text-[2.82rem] lg:text-[3.08rem]">
              {{ t('home.hero.titleLine1') }}
              <br />
              <span class="home-title-shimmer">
                {{ t('home.hero.titleLine2') }}
                <span class="home-title-gradient">{{ t('home.hero.titleHighlight') }}</span>
              </span>
            </h1>
            <p class="mt-4 max-w-[37rem] text-base leading-7 text-gray-600 dark:text-dark-300 sm:text-[1.04rem]">
              {{ t('home.hero.description') }}
            </p>

            <div class="home-url-card">
              <div class="mb-4 pl-1 text-sm font-medium tracking-tight text-gray-500 dark:text-dark-400 sm:text-base">
                {{ t('home.hero.baseUrlLabel') }}
              </div>
              <div class="home-url-field">
                <div class="home-url-content">
                  <span class="home-api-base">{{ apiBaseUrl }}</span>
                  <span class="home-endpoint-rotator" aria-hidden="true">
                    <span
                      v-for="(path, index) in apiEndpointPaths"
                      :key="path"
                      class="home-endpoint-path"
                      :style="{ animationDelay: `${index * 2.6}s` }"
                    >
                      {{ path }}
                    </span>
                  </span>
                </div>
                <button
                  type="button"
                  class="home-url-copy"
                  :title="t('common.copy')"
                  @click="copyBaseUrl"
                >
                  <Icon name="copy" size="sm" />
                </button>
              </div>
            </div>

            <div class="mt-6 flex flex-col items-start gap-3 sm:flex-row">
              <router-link
                :to="isAuthenticated ? dashboardPath : '/login'"
                class="btn btn-primary min-w-[7.5rem] px-5 py-2.5 text-sm shadow-lg shadow-primary-500/30"
              >
                {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
                <Icon name="arrowRight" size="md" class="ml-1.5" :stroke-width="2" />
              </router-link>
              <router-link
                to="/docs"
                class="btn btn-secondary min-w-[6rem] px-5 py-2.5 text-sm"
              >
                <Icon name="book" size="md" />
                {{ t('home.guide') }}
              </router-link>
            </div>

            <div class="home-stats-grid mt-6 grid max-w-[24rem] grid-cols-3 gap-3">
              <div
                v-for="stat in heroStats"
                :key="stat.label"
                class="home-stat-card"
              >
                <div class="text-lg font-black tracking-tight text-gray-950 dark:text-white sm:text-xl">
                  {{ stat.value }}
                </div>
                <div class="mt-1.5 text-xs font-medium text-gray-500 dark:text-dark-400">
                  {{ stat.label }}
                </div>
              </div>
            </div>
          </div>

          <div class="home-hero-visual">
            <div class="home-hero-panel">
              <div
                v-for="feature in heroFeatures"
                :key="feature.title"
                class="home-hero-card"
              >
                <div class="home-icon-soft">
                  <Icon :name="feature.icon" size="md" />
                </div>
                <div>
                  <h3 class="text-[0.95rem] font-bold text-gray-950 dark:text-white">{{ feature.title }}</h3>
                  <p class="mt-1.5 text-[0.82rem] leading-5 text-gray-500 dark:text-dark-300">{{ feature.description }}</p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section class="home-section home-snap-section bg-white/70 px-4 dark:bg-dark-950/30 sm:px-6">
        <div class="mx-auto max-w-6xl">
          <div class="max-w-3xl">
            <div class="home-section-label">{{ t('home.value.eyebrow') }}</div>
            <h2 class="mt-4 text-[2.08rem] font-black leading-tight tracking-[-0.045em] text-gray-950 dark:text-white sm:text-[2.62rem]">
              {{ t('home.value.title') }}
            </h2>
            <p class="mt-4 text-[1.04rem] leading-7 text-gray-600 dark:text-dark-300">
              {{ t('home.value.description') }}
            </p>
          </div>

          <div class="mt-9 grid gap-5 lg:grid-cols-2">
            <article
              v-for="card in valueCards"
              :key="card.title"
              class="home-value-card"
            >
              <div class="home-card-wash"></div>
              <div class="home-icon-soft">
                <Icon :name="card.icon" size="md" />
              </div>
              <h3 class="mt-6 text-[1.32rem] font-bold tracking-tight text-gray-950 dark:text-white">
                {{ card.title }}
              </h3>
              <p class="mt-3 text-[0.94rem] leading-6 text-gray-600 dark:text-dark-300">
                {{ card.description }}
              </p>
            </article>
          </div>
        </div>
      </section>

      <section class="home-section home-snap-section px-4 sm:px-6">
        <div class="mx-auto max-w-6xl">
          <div class="max-w-3xl">
            <div class="home-section-label">{{ t('home.workflow.eyebrow') }}</div>
            <h2 class="mt-4 text-[2.08rem] font-black leading-tight tracking-[-0.045em] text-gray-950 dark:text-white sm:text-[2.62rem]">
              {{ t('home.workflow.title') }}
            </h2>
          </div>

          <div class="mt-9 grid gap-5 lg:grid-cols-3">
            <article
              v-for="step in workflowSteps"
              :key="step.step"
              class="home-workflow-card"
            >
              <div class="home-icon-soft rounded-full">
                <Icon :name="step.icon" size="md" />
              </div>
              <div class="mt-4 text-xs font-black tracking-[0.2em] text-gray-400 dark:text-dark-500">
                {{ step.step }}
              </div>
              <h3 class="mt-3 text-[1.32rem] font-bold tracking-tight text-gray-950 dark:text-white">
                {{ step.title }}
              </h3>
              <p class="mt-3 text-[0.94rem] leading-6 text-gray-600 dark:text-dark-300">
                {{ step.description }}
              </p>
            </article>
          </div>
        </div>
      </section>

      <section
        ref="channelsSectionRef"
        class="home-section home-snap-section home-channels-section bg-white/70 px-4 dark:bg-dark-950/30 sm:px-6"
        :class="{ 'is-visible': channelsVisible }"
      >
        <div class="mx-auto max-w-6xl text-center">
          <div class="home-section-label justify-center">{{ t('home.channels.eyebrow') }}</div>
          <h2 class="mt-4 text-[2.08rem] font-black leading-tight tracking-[-0.045em] text-gray-950 dark:text-white sm:text-[2.62rem]">
            {{ t('home.channels.title') }}
          </h2>
          <p class="mx-auto mt-4 max-w-2xl text-[1.04rem] leading-7 text-gray-600 dark:text-dark-300">
            {{ t('home.channels.description') }}
          </p>

          <div class="mt-9 grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
            <article
              v-for="(channel, index) in supportedChannels"
              :key="channel.name"
              class="home-channel-card"
              :class="{ 'home-channel-card-muted': channel.isCustom }"
              :style="{ transitionDelay: channelsVisible ? `${index * 90}ms` : '0ms' }"
            >
              <div class="home-provider-mark" :class="channel.markClass">{{ channel.shortName }}</div>
              <h3 class="mt-4 text-xl font-bold text-gray-950 dark:text-white">{{ channel.name }}</h3>
              <p class="mt-2 min-h-10 text-[0.82rem] leading-5 text-gray-500 dark:text-dark-300">
                {{ channel.description }}
              </p>
              <span class="mt-4 inline-flex rounded-full bg-primary-50 px-2.5 py-1 text-[0.7rem] font-semibold text-primary-700 dark:bg-primary-950/50 dark:text-primary-300">
                {{ channel.status }}
              </span>
            </article>
          </div>
        </div>
      </section>

      <section class="home-section home-snap-section home-status-section bg-white/70 px-4 dark:bg-dark-950/30 sm:px-6">
        <div class="mx-auto grid w-full max-w-6xl items-center gap-7 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
          <div>
            <div class="home-section-label">{{ t('home.statusPreview.eyebrow') }}</div>
            <h2 class="mt-4 text-[2.08rem] font-black leading-tight tracking-[-0.045em] text-gray-950 dark:text-white sm:text-[2.62rem]">
              {{ t('home.statusPreview.title') }}
            </h2>
            <p class="mt-4 text-[1.04rem] leading-7 text-gray-600 dark:text-dark-300">
              {{ t('home.statusPreview.description') }}
            </p>
            <div class="mt-6 flex flex-wrap items-center gap-3">
              <router-link to="/monitor" class="btn btn-secondary px-5 py-2.5 text-sm">
                <Icon name="chart" size="md" />
                {{ t('home.statusPreview.button') }}
              </router-link>
              <router-link to="/docs" class="home-inline-link">
                {{ t('home.statusPreview.guideLink') }}
                <Icon name="arrowRight" size="sm" />
              </router-link>
            </div>
          </div>

          <div class="home-status-showcase">
            <HomeChannelStatusPreview :items="publicMonitorItems" :loading="publicMonitorLoading" />
          </div>
        </div>
      </section>

      <section class="home-section home-snap-section home-final-section px-4 sm:px-6">
        <div class="home-final-content mx-auto flex w-full max-w-5xl flex-col gap-8">
          <div class="home-final-card overflow-hidden rounded-[1.85rem] border border-primary-100 bg-gradient-to-br from-primary-100 via-white to-cyan-50 p-7 text-center shadow-card-hover dark:border-primary-900/50 dark:from-primary-950/50 dark:via-dark-900 dark:to-dark-950 sm:p-10">
            <div class="home-final-orbit mx-auto mb-6">
              <Icon name="sparkles" size="lg" />
            </div>
            <h2 class="text-[1.7rem] font-black leading-tight tracking-[-0.035em] text-gray-950 dark:text-white sm:text-[2.08rem]">
              {{ t('home.cta.title') }}
            </h2>
            <p class="mx-auto mt-4 max-w-2xl text-[0.96rem] leading-6 text-gray-600 dark:text-dark-300 sm:text-[1.04rem]">
              {{ t('home.cta.description') }}
            </p>
            <div class="mt-6 flex flex-col items-center justify-center gap-3 sm:flex-row">
              <router-link
                :to="isAuthenticated ? dashboardPath : '/login'"
                class="btn btn-primary px-6 py-2.5 text-sm shadow-lg shadow-primary-500/30"
              >
                {{ isAuthenticated ? t('home.goToDashboard') : t('home.cta.button') }}
                <Icon name="arrowRight" size="md" />
              </router-link>
              <router-link
                to="/docs"
                class="btn btn-secondary px-6 py-2.5 text-sm"
              >
                <Icon name="book" size="md" />
                {{ t('home.cta.guideButton') }}
              </router-link>
            </div>
          </div>

          <footer class="border-t border-gray-200/60 pt-6 dark:border-dark-800/70">
            <div class="flex flex-col items-center justify-between gap-4 text-center sm:flex-row sm:text-left">
              <p class="text-sm text-gray-500 dark:text-dark-400">
                &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
              </p>
              <div class="flex flex-wrap items-center justify-center gap-x-4 gap-y-2">
                <router-link
                  to="/docs"
                  class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
                >
                  {{ t('home.guide') }}
                </router-link>
                <a
                  v-if="docUrl"
                  :href="docUrl"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
                >
                  {{ t('home.externalDocs') }}
                </a>
                <router-link
                  v-for="link in legalLinks"
                  :key="link.to"
                  :to="link.to"
                  class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
                >
                  {{ link.label }}
                </router-link>
              </div>
            </div>
          </footer>
          </div>
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore, useAnnouncementStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import HomeChannelStatusPreview from '@/components/status/HomeChannelStatusPreview.vue'
import { listPublicChannelMonitors, type PublicMonitorView } from '@/api/publicChannelMonitor'
import { useClipboard } from '@/composables/useClipboard'
import { normalizeSiteName } from '@/utils/siteBrand'
import { formatRelativeWithDateTime } from '@/utils/format'
import type { UserAnnouncement } from '@/types'

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

type HomeIconName = InstanceType<typeof Icon>['$props']['name']

interface HomeIconCard {
  icon: HomeIconName
  title: string
  description: string
}

interface HomeWorkflowStep extends HomeIconCard {
  step: string
}

interface HomeChannel {
  name: string
  shortName: string
  description: string
  status: string
  markClass: string
  isCustom?: boolean
}

const authStore = useAuthStore()
const appStore = useAppStore()
const announcementStore = useAnnouncementStore()
const announcementModalOpen = ref(false)
const announcementTab = ref<'notifications' | 'system'>('system')
const publicMonitorItems = ref<PublicMonitorView[]>([])
const publicMonitorLoading = ref(false)

// Site settings - directly from appStore (already initialized from injected config)
const siteName = computed(() => normalizeSiteName(appStore.cachedPublicSettings?.site_name || appStore.siteName))
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const apiBaseUrl = computed(() => {
  const configured = appStore.cachedPublicSettings?.api_base_url || appStore.apiBaseUrl
  return normalizeBaseUrl(configured || 'https://subapis.com')
})
const apiEndpointPaths = [
  '/v1/chat/completions',
  '/v1/messages',
  '/v1/responses',
  '/v1beta/models/{model}:generateContent',
  '/antigravity/v1/messages'
] as const

const heroStats = computed(() => [
  { value: '4', label: t('home.hero.stats.channels') },
  { value: '99.9%', label: t('home.hero.stats.sla') },
  { value: t('home.hero.stats.realtimeValue'), label: t('home.hero.stats.billing') }
])

const heroFeatures = computed<HomeIconCard[]>(() => [
  {
    icon: 'clock',
    title: t('home.hero.features.routing.title'),
    description: t('home.hero.features.routing.description')
  },
  {
    icon: 'chart',
    title: t('home.hero.features.observability.title'),
    description: t('home.hero.features.observability.description')
  },
  {
    icon: 'cloud',
    title: t('home.hero.features.governance.title'),
    description: t('home.hero.features.governance.description')
  }
])

const valueCards = computed<HomeIconCard[]>(() => [
  {
    icon: 'link',
    title: t('home.value.cards.gateway.title'),
    description: t('home.value.cards.gateway.description')
  },
  {
    icon: 'chartBar',
    title: t('home.value.cards.observability.title'),
    description: t('home.value.cards.observability.description')
  },
  {
    icon: 'dollar',
    title: t('home.value.cards.billing.title'),
    description: t('home.value.cards.billing.description')
  },
  {
    icon: 'lock',
    title: t('home.value.cards.security.title'),
    description: t('home.value.cards.security.description')
  }
])

const workflowSteps = computed<HomeWorkflowStep[]>(() => [
  {
    step: '01',
    icon: 'server',
    title: t('home.workflow.steps.access.title'),
    description: t('home.workflow.steps.access.description')
  },
  {
    step: '02',
    icon: 'cog',
    title: t('home.workflow.steps.policy.title'),
    description: t('home.workflow.steps.policy.description')
  },
  {
    step: '03',
    icon: 'trendingUp',
    title: t('home.workflow.steps.operate.title'),
    description: t('home.workflow.steps.operate.description')
  }
])

const supportedChannels = computed<HomeChannel[]>(() => [
  {
    name: t('home.channels.items.claude.name'),
    shortName: 'C',
    description: t('home.channels.items.claude.description'),
    status: t('home.channels.supported'),
    markClass: 'home-provider-claude'
  },
  {
    name: t('home.channels.items.gpt.name'),
    shortName: 'G',
    description: t('home.channels.items.gpt.description'),
    status: t('home.channels.supported'),
    markClass: 'home-provider-gpt'
  },
  {
    name: t('home.channels.items.gemini.name'),
    shortName: 'G',
    description: t('home.channels.items.gemini.description'),
    status: t('home.channels.supported'),
    markClass: 'home-provider-gemini'
  },
  {
    name: t('home.channels.items.antigravity.name'),
    shortName: 'A',
    description: t('home.channels.items.antigravity.description'),
    status: t('home.channels.supported'),
    markClass: 'home-provider-antigravity'
  },
  {
    name: t('home.channels.items.custom.name'),
    shortName: '+',
    description: t('home.channels.items.custom.description'),
    status: t('home.channels.custom'),
    markClass: 'home-provider-custom',
    isCustom: true
  }
])

const homeAnnouncements = computed(() => announcementStore.announcements.slice(0, 20))
const notificationAnnouncements = computed(() =>
  homeAnnouncements.value.filter((announcement) => announcement.notify_mode === 'popup')
)
const systemAnnouncements = computed(() =>
  homeAnnouncements.value.filter((announcement) => announcement.notify_mode !== 'popup')
)
const unreadHomeAnnouncements = computed(() =>
  homeAnnouncements.value.filter((announcement) => !announcement.read_at)
)
const activeAnnouncements = computed<UserAnnouncement[]>(() =>
  announcementTab.value === 'notifications' ? notificationAnnouncements.value : systemAnnouncements.value
)

const legalLinks = computed(() => [
  { to: '/legal/terms', label: t('home.footer.terms') },
  { to: '/legal/usage-policy', label: t('home.footer.usagePolicy') },
  { to: '/legal/supported-regions', label: t('home.footer.supportedRegions') },
  { to: '/legal/service-specific-terms', label: t('home.footer.serviceSpecificTerms') }
])

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))
const snapContainerRef = ref<HTMLElement | null>(null)
const channelsSectionRef = ref<HTMLElement | null>(null)
const channelsVisible = ref(false)
let channelsObserver: IntersectionObserver | null = null
let snapLockTimer: number | undefined

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

// Toggle theme
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function normalizeBaseUrl(url: string): string {
  return url.trim().replace(/\/+$/, '')
}

async function copyBaseUrl() {
  await copyToClipboard(apiBaseUrl.value)
}

async function loadPublicMonitorPreview() {
  publicMonitorLoading.value = true
  try {
    const res = await listPublicChannelMonitors()
    publicMonitorItems.value = res.items || []
  } catch {
    publicMonitorItems.value = []
  } finally {
    publicMonitorLoading.value = false
  }
}

function plainAnnouncementContent(content: string): string {
  return content
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/!\[[^\]]*]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]+)]\([^)]*\)/g, '$1')
    .replace(/[#>*_~\-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

function formatAnnouncementTime(date: string): string {
  return formatRelativeWithDateTime(date)
}

async function openAnnouncementModal() {
  announcementModalOpen.value = true
  if (isAuthenticated.value) {
    await announcementStore.fetchAnnouncements(true)
    if (notificationAnnouncements.value.some((announcement) => !announcement.read_at)) {
      announcementTab.value = 'notifications'
    }
  }
}

function closeAnnouncementModal() {
  announcementModalOpen.value = false
}

function closeAnnouncementToday() {
  localStorage.setItem('home-announcement-closed-date', new Date().toISOString().slice(0, 10))
  closeAnnouncementModal()
}

function getSnapSections(): HTMLElement[] {
  return Array.from(snapContainerRef.value?.querySelectorAll<HTMLElement>('.home-snap-section') ?? [])
}

function syncSnapIndex(): number {
  const container = snapContainerRef.value
  const sections = getSnapSections()
  if (!container || sections.length === 0) return 0

  let closestIndex = 0
  let closestDistance = Number.POSITIVE_INFINITY
  sections.forEach((section, index) => {
    const distance = Math.abs(section.offsetTop - container.scrollTop)
    if (distance < closestDistance) {
      closestDistance = distance
      closestIndex = index
    }
  })
  return closestIndex
}

function scrollToSnapSection(index: number) {
  const container = snapContainerRef.value
  const sections = getSnapSections()
  if (!container || sections.length === 0) return

  const nextIndex = Math.min(Math.max(index, 0), sections.length - 1)
  container.scrollTo({
    top: sections[nextIndex].offsetTop,
    left: 0,
    behavior: 'smooth'
  })

  window.clearTimeout(snapLockTimer)
  snapLockTimer = window.setTimeout(() => {
    snapLockTimer = undefined
  }, 780)
}

function handleSnapWheel(event: WheelEvent) {
  if (Math.abs(event.deltaY) < 8) return

  event.preventDefault()
  if (snapLockTimer !== undefined) return

  const currentIndex = syncSnapIndex()
  scrollToSnapSection(currentIndex + (event.deltaY > 0 ? 1 : -1))
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return Boolean(target.closest('input, textarea, select, [contenteditable="true"]'))
}

function handleSnapKeydown(event: KeyboardEvent) {
  if (announcementModalOpen.value && event.key === 'Escape') {
    closeAnnouncementModal()
    return
  }

  if (isEditableTarget(event.target)) return

  const forwardKeys = ['ArrowDown', 'PageDown', 'Space']
  const backwardKeys = ['ArrowUp', 'PageUp']
  const isForward = forwardKeys.includes(event.code)
  const isBackward = backwardKeys.includes(event.code)
  if (!isForward && !isBackward) return

  event.preventDefault()
  if (snapLockTimer !== undefined) return

  const currentIndex = syncSnapIndex()
  scrollToSnapSection(currentIndex + (isForward ? 1 : -1))
}

// Initialize theme
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()

  // Check auth state
  authStore.checkAuth()

  // Ensure public settings are loaded (will use cache if already loaded from injected config)
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }

  if (authStore.isAuthenticated) {
    announcementStore.fetchAnnouncements()
  }

  void loadPublicMonitorPreview()

  snapContainerRef.value?.scrollTo({ top: 0, left: 0 })
  snapContainerRef.value?.addEventListener('wheel', handleSnapWheel, { passive: false })
  window.addEventListener('keydown', handleSnapKeydown)

  if (channelsSectionRef.value) {
    channelsObserver = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          channelsVisible.value = true
          channelsObserver?.disconnect()
          channelsObserver = null
        }
      },
      {
        root: snapContainerRef.value,
        threshold: 0.35
      }
    )
    channelsObserver.observe(channelsSectionRef.value)
  }
})

onUnmounted(() => {
  snapContainerRef.value?.removeEventListener('wheel', handleSnapWheel)
  window.removeEventListener('keydown', handleSnapKeydown)
  window.clearTimeout(snapLockTimer)
  channelsObserver?.disconnect()
  channelsObserver = null
})
</script>

<style scoped>
.home-page {
  isolation: isolate;
}

.home-nav {
  max-width: min(100% - 2rem, 104rem);
  padding: 0.35rem 0;
}

.home-brand-name {
  font-style: italic;
  letter-spacing: -0.045em;
  transform: skewX(-6deg);
}

.home-nav-action {
  align-items: center;
  border-radius: 9999px;
  color: #475569;
  display: inline-flex;
  height: 2.25rem;
  justify-content: center;
  position: relative;
  transition:
    background-color 180ms ease,
    box-shadow 180ms ease,
    color 180ms ease,
    transform 180ms ease;
  width: 2.25rem;
}

.home-nav-action:hover,
.home-nav-action.is-active {
  background: rgba(255, 255, 255, 0.72);
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.1);
  color: #0f766e;
  transform: translateY(-1px);
}

.home-nav-text-link {
  align-items: center;
  border-radius: 9999px;
  color: #475569;
  gap: 0.35rem;
  font-size: 0.82rem;
  font-weight: 800;
  min-height: 2.25rem;
  padding: 0 0.8rem;
  transition:
    background-color 180ms ease,
    box-shadow 180ms ease,
    color 180ms ease,
    transform 180ms ease;
}

.home-nav-text-link:hover {
  background: rgba(255, 255, 255, 0.72);
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.1);
  color: #0f766e;
  transform: translateY(-1px);
}

.home-announcement-badge {
  align-items: center;
  background: linear-gradient(135deg, #ef4444, #f97316);
  border: 2px solid #fff;
  border-radius: 9999px;
  box-shadow: 0 8px 14px rgba(239, 68, 68, 0.28);
  color: #fff;
  display: inline-flex;
  font-size: 0.625rem;
  font-weight: 900;
  height: 1rem;
  justify-content: center;
  min-width: 1rem;
  padding: 0 0.22rem;
  position: absolute;
  right: -0.1rem;
  top: -0.08rem;
}

.home-auth-actions {
  align-items: center;
  background: rgba(241, 245, 249, 0.9);
  border-radius: 9999px;
  display: inline-flex;
  gap: 0.15rem;
  padding: 0.25rem;
}

.home-auth-login,
.home-auth-register {
  align-items: center;
  border-radius: 9999px;
  display: inline-flex;
  font-size: 0.8125rem;
  font-weight: 700;
  justify-content: center;
  min-height: 2rem;
  padding: 0 0.9rem;
  transition:
    background-color 180ms ease,
    color 180ms ease,
    transform 180ms ease;
}

.home-auth-login {
  color: #334155;
}

.home-auth-login:hover {
  background: rgba(255, 255, 255, 0.88);
  color: #0f172a;
}

.home-auth-register {
  background: linear-gradient(135deg, #14b8a6, #0891b2);
  color: #fff;
  box-shadow: 0 10px 22px rgba(13, 148, 136, 0.25);
}

.home-auth-register:hover {
  transform: translateY(-1px);
}

.home-snap-container {
  height: 100vh;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior-y: contain;
  scroll-behavior: smooth;
  scroll-snap-type: y mandatory;
  scrollbar-width: none;
}

.home-snap-container::-webkit-scrollbar {
  display: none;
}

.home-bg {
  pointer-events: none;
  position: fixed;
  inset: 0;
  overflow: hidden;
}

.home-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(20, 184, 166, 0.035) 1px, transparent 1px),
    linear-gradient(90deg, rgba(20, 184, 166, 0.035) 1px, transparent 1px);
  background-size: 64px 64px;
  mask-image: linear-gradient(180deg, rgba(0, 0, 0, 0.68), transparent 62%);
}

.home-blob {
  position: absolute;
  border-radius: 9999px;
  filter: blur(52px);
}

.home-blob-a {
  left: -8rem;
  top: 4rem;
  height: 24rem;
  width: 24rem;
  background: rgba(20, 184, 166, 0.2);
}

.home-blob-b {
  bottom: 16rem;
  right: -10rem;
  height: 27rem;
  width: 27rem;
  background: rgba(6, 182, 212, 0.16);
}

.home-blob-c {
  left: 48%;
  top: 13rem;
  height: 20rem;
  width: 20rem;
  background: rgba(45, 212, 191, 0.12);
}

.home-section {
  position: relative;
}

.home-snap-section {
  align-items: center;
  display: flex;
  min-height: 100vh;
  padding-bottom: 2.5rem;
  padding-top: 6.25rem;
  scroll-snap-align: start;
  scroll-snap-stop: always;
}

.home-hero-section {
  min-height: 100vh;
}

.home-hero-shell {
  min-height: calc(100vh - 8.75rem);
}

.home-hero-copy {
  max-width: 38rem;
}

.home-hero-visual {
  align-items: center;
  display: flex;
  justify-content: center;
  min-height: 22rem;
  padding-top: 0.5rem;
}

.home-title-gradient {
  background: linear-gradient(90deg, #0f172a 0%, #0d9488 48%, rgba(20, 184, 166, 0.2) 100%);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.home-title-shimmer {
  display: inline-block;
  overflow: hidden;
  position: relative;
}

.home-title-shimmer::after {
  animation: home-title-sweep 3.6s ease-in-out infinite;
  background: linear-gradient(
    105deg,
    transparent 0%,
    rgba(255, 255, 255, 0) 34%,
    rgba(255, 255, 255, 0.88) 48%,
    rgba(255, 255, 255, 0) 62%,
    transparent 100%
  );
  content: '';
  inset: -12% -40%;
  mix-blend-mode: screen;
  position: absolute;
  transform: translateX(-65%);
}

.home-hero-panel {
  border: 1px solid rgba(226, 232, 240, 0.82);
  border-radius: 1.45rem;
  background: rgba(255, 255, 255, 0.86);
  box-shadow: 0 20px 48px rgba(15, 23, 42, 0.1);
  display: grid;
  gap: 0.75rem;
  max-width: 28rem;
  padding: 1rem;
  width: 100%;
  backdrop-filter: blur(18px);
}

.home-hero-card {
  animation: home-float 5.8s ease-in-out infinite;
  align-items: flex-start;
  border: 1px solid rgba(226, 232, 240, 0.9);
  border-radius: 1rem;
  background: rgba(255, 255, 255, 0.92);
  box-shadow: 0 6px 16px rgba(15, 23, 42, 0.04);
  display: flex;
  gap: 0.75rem;
  padding: 0.85rem;
  transition:
    border-color 220ms ease,
    box-shadow 220ms ease,
    transform 220ms ease;
}

.home-hero-card:nth-child(2) {
  animation-delay: -1.4s;
  margin-left: 0.8rem;
}

.home-hero-card:nth-child(3) {
  animation-delay: -2.8s;
  margin-left: 0.25rem;
}

.home-hero-card:hover {
  border-color: rgba(20, 184, 166, 0.38);
  box-shadow: 0 18px 34px rgba(13, 148, 136, 0.12);
  transform: translateY(-5px);
}

.home-icon-soft {
  align-items: center;
  background: linear-gradient(135deg, #ccfbf1 0%, #ecfeff 100%);
  border-radius: 0.9rem;
  color: #0d9488;
  display: inline-flex;
  flex: 0 0 auto;
  height: 2.25rem;
  justify-content: center;
  width: 2.25rem;
}

.home-section-label {
  align-items: center;
  color: #0f766e;
  display: inline-flex;
  font-size: 0.76rem;
  font-weight: 800;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}

.home-value-card,
.home-workflow-card,
.home-channel-card {
  border: 1px solid #e2e8f0;
  background: rgba(255, 255, 255, 0.88);
  box-shadow: 0 8px 22px rgba(15, 23, 42, 0.045);
  transition:
    border-color 220ms ease,
    box-shadow 220ms ease,
    transform 220ms ease;
}

.home-value-card:hover,
.home-workflow-card:hover,
.home-channel-card:hover {
  border-color: rgba(20, 184, 166, 0.36);
  box-shadow: 0 22px 54px rgba(15, 23, 42, 0.1);
  transform: translateY(-6px);
}

.home-value-card {
  border-radius: 1.25rem;
  overflow: hidden;
  padding: 1.5rem;
  position: relative;
}

.home-card-wash {
  background: linear-gradient(100deg, rgba(20, 184, 166, 0.16), rgba(6, 182, 212, 0.08), transparent 76%);
  border-bottom-left-radius: 70%;
  border-bottom-right-radius: 45%;
  height: 4.8rem;
  left: 2.5rem;
  position: absolute;
  top: 0;
  transform: skewX(-8deg);
  width: 75%;
}

.home-workflow-card {
  border-radius: 1.25rem;
  overflow: hidden;
  padding: 1.5rem;
  position: relative;
}

.home-workflow-card::before {
  background: linear-gradient(180deg, #14b8a6, #06b6d4);
  border-radius: 9999px;
  content: '';
  height: calc(100% - 1.5rem);
  left: 0;
  position: absolute;
  top: 0.75rem;
  width: 4px;
}

.home-channel-card {
  align-items: center;
  border-radius: 1.15rem;
  display: flex;
  flex-direction: column;
  min-height: 12rem;
  opacity: 0;
  padding: 1.15rem;
  transform: translateY(1.35rem) scale(0.98);
  transition:
    opacity 560ms ease,
    transform 560ms cubic-bezier(0.22, 1, 0.36, 1),
    border-color 220ms ease,
    box-shadow 220ms ease;
}

.home-channels-section.is-visible .home-channel-card {
  opacity: 1;
  transform: translateY(0) scale(1);
}

.home-channels-section.is-visible .home-channel-card:hover {
  transform: translateY(-6px) scale(1);
}

.home-channel-card-muted {
  background: rgba(248, 250, 252, 0.88);
}

.home-provider-mark {
  align-items: center;
  border-radius: 0.9rem;
  color: #fff;
  display: inline-flex;
  font-size: 1rem;
  font-weight: 900;
  height: 2.8rem;
  justify-content: center;
  width: 2.8rem;
}

.home-provider-claude {
  background: linear-gradient(135deg, #fb923c, #ea580c);
}

.home-provider-gpt {
  background: linear-gradient(135deg, #10b981, #059669);
}

.home-provider-gemini {
  background: linear-gradient(135deg, #38bdf8, #2563eb);
}

.home-provider-antigravity {
  background: linear-gradient(135deg, #14b8a6, #0d9488);
}

.home-provider-custom {
  background: linear-gradient(135deg, #64748b, #334155);
}

.home-stat-card {
  border: 1px dashed rgba(20, 184, 166, 0.34);
  border-radius: 0.85rem;
  background: rgba(255, 255, 255, 0.58);
  box-shadow: 0 8px 18px rgba(15, 23, 42, 0.035);
  padding: 0.7rem 0.8rem;
  text-align: left;
  backdrop-filter: blur(10px);
  transition:
    border-color 180ms ease,
    box-shadow 180ms ease,
    transform 180ms ease;
}

.home-stat-card:hover {
  border-color: rgba(20, 184, 166, 0.58);
  box-shadow: 0 16px 30px rgba(15, 23, 42, 0.08);
  transform: translateY(-3px);
}

.home-url-card {
  border: 1px solid rgba(226, 232, 240, 0.86);
  border-radius: 1.1rem;
  background: rgba(255, 255, 255, 0.88);
  box-shadow: 0 14px 30px rgba(15, 23, 42, 0.085);
  margin-top: 1.65rem;
  max-width: 30rem;
  padding: 0.9rem 1rem 1rem;
  backdrop-filter: blur(18px);
  transition:
    box-shadow 220ms ease,
    transform 220ms ease;
}

.home-url-card:hover {
  box-shadow: 0 20px 40px rgba(15, 23, 42, 0.12);
  transform: translateY(-3px);
}

.home-api-base {
  color: #111827;
  flex: 0 1 auto;
  white-space: nowrap;
  overflow-wrap: anywhere;
}

.home-url-content {
  align-items: center;
  display: flex;
  flex: 1 1 auto;
  gap: 0.5rem;
  justify-content: space-between;
  min-width: 0;
}

.home-endpoint-rotator {
  color: #2563eb;
  display: inline-grid;
  flex: 0 0 auto;
  font-weight: 700;
  max-width: 48%;
  min-height: 1.5em;
  overflow: hidden;
  padding-left: 0.18em;
  text-align: right;
  vertical-align: bottom;
}

.home-endpoint-path {
  animation: home-endpoint-cycle 13s infinite;
  grid-area: 1 / 1;
  opacity: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  transform: translateY(0.6rem);
  white-space: nowrap;
}

.home-url-field {
  align-items: center;
  background: #f2f3f5;
  border-radius: 0.9rem;
  color: #111827;
  display: flex;
  font-family:
    ui-sans-serif,
    system-ui,
    -apple-system,
    BlinkMacSystemFont,
    "Segoe UI",
    sans-serif;
  font-size: clamp(0.88rem, 1.05vw, 0.98rem);
  font-weight: 500;
  gap: 0.75rem;
  justify-content: space-between;
  line-height: 1.1;
  min-height: 2.5rem;
  padding: 0.3rem 0.3rem 0.3rem 0.7rem;
}

.home-url-copy {
  align-items: center;
  background: rgba(229, 231, 235, 0.96);
  border-radius: 9999px;
  color: #6b7280;
  display: inline-flex;
  flex: 0 0 auto;
  height: 2rem;
  justify-content: center;
  transition:
    background-color 160ms ease,
    color 160ms ease,
    transform 160ms ease;
  width: 2rem;
}

.home-url-copy:hover {
  background: #d1d5db;
  color: #374151;
  transform: translateY(-1px);
}

.home-inline-link {
  align-items: center;
  color: #0f766e;
  display: inline-flex;
  font-size: 0.875rem;
  font-weight: 800;
  gap: 0.35rem;
  transition:
    color 180ms ease,
    transform 180ms ease;
}

.home-inline-link:hover {
  color: #0891b2;
  transform: translateX(2px);
}

.home-status-section {
  overflow: hidden;
}

.home-status-showcase {
  border: 1px solid rgba(226, 232, 240, 0.86);
  border-radius: 1.35rem;
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 0.92), rgba(236, 254, 255, 0.78)),
    #fff;
  box-shadow: 0 20px 50px rgba(15, 23, 42, 0.09);
  max-height: min(62vh, 35rem);
  overflow: auto;
  padding: 0.6rem;
  scrollbar-width: thin;
}

.home-final-section {
  align-items: stretch;
}

.home-final-content {
  justify-content: center;
  min-height: calc(100vh - 8.75rem);
}

.home-final-card {
  position: relative;
}

.home-final-card::before,
.home-final-card::after {
  border-radius: 9999px;
  content: '';
  pointer-events: none;
  position: absolute;
}

.home-final-card::before {
  background: rgba(20, 184, 166, 0.12);
  height: 12rem;
  left: -4rem;
  top: -5rem;
  width: 12rem;
}

.home-final-card::after {
  background: rgba(6, 182, 212, 0.1);
  bottom: -5rem;
  height: 14rem;
  right: -4rem;
  width: 14rem;
}

.home-final-orbit {
  align-items: center;
  background: linear-gradient(135deg, #14b8a6, #0891b2);
  border-radius: 1.25rem;
  box-shadow: 0 18px 34px rgba(13, 148, 136, 0.25);
  color: #fff;
  display: flex;
  height: 3.6rem;
  justify-content: center;
  position: relative;
  width: 3.6rem;
}

.home-final-orbit::after {
  animation: home-spin 12s linear infinite;
  border: 1px dashed rgba(13, 148, 136, 0.32);
  border-radius: 9999px;
  content: '';
  inset: -0.72rem;
  position: absolute;
}

.home-announcement-backdrop {
  align-items: center;
  background:
    radial-gradient(circle at 50% 22%, rgba(20, 184, 166, 0.18), transparent 36%),
    rgba(2, 6, 23, 0.58);
  backdrop-filter: blur(10px);
  display: flex;
  inset: 0;
  justify-content: center;
  padding: 1.5rem;
  position: fixed;
  z-index: 120;
}

.home-announcement-modal {
  background: #fff;
  border: 1px solid rgba(226, 232, 240, 0.9);
  border-radius: 1rem;
  box-shadow: 0 34px 90px rgba(15, 23, 42, 0.28);
  display: flex;
  flex-direction: column;
  max-height: min(78vh, 42rem);
  overflow: hidden;
  width: min(100%, 52rem);
}

.home-announcement-modal-head {
  align-items: flex-start;
  border-bottom: 1px solid #edf2f7;
  display: flex;
  gap: 1rem;
  justify-content: space-between;
  padding: 1.25rem 1.5rem;
}

.home-announcement-modal-icon {
  align-items: center;
  background: linear-gradient(135deg, #14b8a6, #0891b2);
  border-radius: 9999px;
  box-shadow: 0 14px 26px rgba(13, 148, 136, 0.22);
  color: #fff;
  display: inline-flex;
  height: 2.5rem;
  justify-content: center;
  width: 2.5rem;
}

.home-announcement-head-actions {
  align-items: center;
  display: flex;
  flex: 0 0 auto;
  gap: 0.65rem;
}

.home-announcement-tabs {
  background: #f1f5f9;
  border-radius: 9999px;
  display: inline-flex;
  gap: 0.25rem;
  padding: 0.25rem;
}

.home-announcement-tab {
  border-radius: 9999px;
  color: #64748b;
  font-size: 0.8125rem;
  font-weight: 800;
  line-height: 1;
  padding: 0.55rem 0.95rem;
  transition:
    background-color 180ms ease,
    color 180ms ease,
    box-shadow 180ms ease;
}

.home-announcement-tab.is-active {
  background: #1677ff;
  box-shadow: 0 10px 20px rgba(22, 119, 255, 0.22);
  color: #fff;
}

.home-announcement-close {
  align-items: center;
  border-radius: 9999px;
  color: #94a3b8;
  display: inline-flex;
  height: 2rem;
  justify-content: center;
  transition:
    background-color 180ms ease,
    color 180ms ease,
    transform 180ms ease;
  width: 2rem;
}

.home-announcement-close:hover {
  background: #f1f5f9;
  color: #334155;
  transform: rotate(90deg);
}

.home-announcement-modal-body {
  min-height: 22rem;
  overflow-y: auto;
  padding: 1.25rem 1.5rem;
}

.home-announcement-loading {
  align-items: center;
  display: flex;
  min-height: 22rem;
  justify-content: center;
}

.home-announcement-spinner {
  animation: home-spin 0.8s linear infinite;
  border: 3px solid rgba(20, 184, 166, 0.16);
  border-top-color: #14b8a6;
  border-radius: 9999px;
  height: 2.5rem;
  width: 2.5rem;
}

.home-announcement-timeline {
  padding: 0.25rem 0 0.25rem 0.85rem;
}

.home-announcement-timeline-item {
  display: grid;
  gap: 0.9rem;
  grid-template-columns: 1.05rem minmax(0, 1fr);
  position: relative;
}

.home-announcement-timeline-item:not(:last-child) {
  padding-bottom: 1.1rem;
}

.home-announcement-timeline-item::before {
  background: #e5e7eb;
  bottom: 0;
  content: '';
  left: 0.36rem;
  position: absolute;
  top: 1.2rem;
  width: 1px;
}

.home-announcement-timeline-item:last-child::before {
  display: none;
}

.home-announcement-timeline-dot {
  background: #cbd5e1;
  border: 3px solid #fff;
  border-radius: 9999px;
  box-shadow: 0 0 0 1px #e5e7eb;
  height: 0.82rem;
  margin-top: 0.45rem;
  position: relative;
  width: 0.82rem;
  z-index: 1;
}

.home-announcement-timeline-item.is-unread .home-announcement-timeline-dot {
  background: #14b8a6;
  box-shadow: 0 0 0 4px rgba(20, 184, 166, 0.14);
}

.home-announcement-timeline-card {
  border-bottom: 1px solid #f1f5f9;
  padding: 0.25rem 0 1.05rem;
}

.home-announcement-timeline-item:last-child .home-announcement-timeline-card {
  border-bottom: 0;
  padding-bottom: 0.25rem;
}

.home-announcement-unread-pill {
  background: rgba(20, 184, 166, 0.1);
  border-radius: 9999px;
  color: #0f766e;
  flex: 0 0 auto;
  font-size: 0.6875rem;
  font-weight: 900;
  padding: 0.25rem 0.55rem;
}

.home-announcement-empty-state {
  align-items: center;
  display: flex;
  flex-direction: column;
  min-height: 22rem;
  justify-content: center;
  text-align: center;
}

.home-announcement-empty-illustration {
  align-items: center;
  background: linear-gradient(135deg, #f8fafc, #ecfeff);
  border: 1px solid #e2e8f0;
  border-radius: 1.5rem;
  color: #94a3b8;
  display: inline-flex;
  height: 5.75rem;
  justify-content: center;
  position: relative;
  width: 5.75rem;
}

.home-announcement-empty-illustration span {
  align-items: center;
  background: #22c55e;
  border: 3px solid #fff;
  border-radius: 9999px;
  bottom: 0.6rem;
  color: #fff;
  display: flex;
  height: 1.35rem;
  justify-content: center;
  position: absolute;
  right: 0.55rem;
  width: 1.35rem;
}

.home-announcement-empty-illustration span::before {
  content: '';
  border-bottom: 2px solid currentColor;
  border-right: 2px solid currentColor;
  height: 0.48rem;
  transform: rotate(45deg) translate(-1px, -1px);
  width: 0.26rem;
}

.home-announcement-modal-foot {
  align-items: center;
  background: #f8fafc;
  border-top: 1px solid #edf2f7;
  display: flex;
  gap: 0.75rem;
  justify-content: flex-end;
  padding: 0.95rem 1.5rem;
}

.home-announcement-foot-button {
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 0.5rem;
  color: #475569;
  font-size: 0.875rem;
  font-weight: 800;
  min-width: 5.75rem;
  padding: 0.58rem 1rem;
  transition:
    background-color 180ms ease,
    border-color 180ms ease,
    color 180ms ease,
    transform 180ms ease;
}

.home-announcement-foot-button:hover {
  border-color: rgba(20, 184, 166, 0.45);
  color: #0f766e;
  transform: translateY(-1px);
}

.home-announcement-foot-button.is-primary {
  background: #1677ff;
  border-color: #1677ff;
  box-shadow: 0 12px 22px rgba(22, 119, 255, 0.2);
  color: #fff;
}

.home-modal-enter-active,
.home-modal-leave-active {
  transition: opacity 180ms ease;
}

.home-modal-enter-active .home-announcement-modal,
.home-modal-leave-active .home-announcement-modal {
  transition:
    opacity 220ms ease,
    transform 220ms cubic-bezier(0.22, 1, 0.36, 1);
}

.home-modal-enter-from,
.home-modal-leave-to {
  opacity: 0;
}

.home-modal-enter-from .home-announcement-modal,
.home-modal-leave-to .home-announcement-modal {
  opacity: 0;
  transform: translateY(1rem) scale(0.97);
}

@keyframes home-endpoint-cycle {
  0%,
  16% {
    opacity: 1;
    transform: translateY(0);
  }

  20%,
  96% {
    opacity: 0;
    transform: translateY(-0.6rem);
  }

  100% {
    opacity: 0;
    transform: translateY(0.6rem);
  }
}

@keyframes home-spin {
  to {
    transform: rotate(360deg);
  }
}

@keyframes home-float {
  0%,
  100% {
    translate: 0 0;
  }

  50% {
    translate: 0 -0.7rem;
  }
}

@keyframes home-title-sweep {
  0%,
  38% {
    transform: translateX(-68%);
  }

  68%,
  100% {
    transform: translateX(68%);
  }
}

.home-page.home-dark {
  background:
    radial-gradient(circle at 17% 17%, rgba(20, 184, 166, 0.13), transparent 29%),
    radial-gradient(circle at 84% 24%, rgba(8, 145, 178, 0.14), transparent 28%),
    radial-gradient(circle at 62% 78%, rgba(15, 118, 110, 0.12), transparent 32%),
    linear-gradient(135deg, #07111f 0%, #0a1724 48%, #081820 100%);
  color: #e2e8f0;
}

.home-page.home-dark .home-grid {
  background-image:
    linear-gradient(rgba(148, 163, 184, 0.045) 1px, transparent 1px),
    linear-gradient(90deg, rgba(148, 163, 184, 0.045) 1px, transparent 1px);
  opacity: 0.32;
}

.home-page.home-dark .home-blob {
  filter: blur(72px);
}

.home-page.home-dark .home-blob-a {
  background: rgba(20, 184, 166, 0.12);
}

.home-page.home-dark .home-blob-b {
  background: rgba(8, 145, 178, 0.13);
}

.home-page.home-dark .home-blob-c {
  background: rgba(45, 212, 191, 0.08);
}

.home-page.home-dark .home-brand-name {
  color: #f8fafc;
  text-shadow: 0 10px 24px rgba(0, 0, 0, 0.28);
}

.home-page.home-dark .home-auth-actions {
  background: rgba(226, 232, 240, 0.92);
  border: 1px solid rgba(255, 255, 255, 0.5);
  box-shadow: 0 18px 38px rgba(0, 0, 0, 0.22);
}

.home-page.home-dark .home-auth-login {
  color: #0f172a;
}

.home-page.home-dark .home-auth-login:hover {
  background: rgba(255, 255, 255, 0.95);
  color: #020617;
}

.home-page.home-dark .home-auth-register {
  background: linear-gradient(135deg, #2dd4bf, #0891b2);
  box-shadow: 0 12px 28px rgba(45, 212, 191, 0.24);
}

.home-page.home-dark :where(.text-gray-950, .dark\:text-white) {
  color: #f8fafc;
}

.home-page.home-dark :where(.text-gray-900) {
  color: #e2e8f0;
}

.home-page.home-dark :where(.text-gray-700, .text-gray-600, .text-gray-500, .dark\:text-dark-300) {
  color: #cbd5e1;
}

.home-page.home-dark :where(.text-gray-400, .dark\:text-dark-400, .dark\:text-dark-500) {
  color: #94a3b8;
}

.home-page.home-dark .home-nav-action {
  color: #cbd5e1;
}

.home-page.home-dark .home-nav-action:hover,
.home-page.home-dark .home-nav-action.is-active {
  background: rgba(15, 23, 42, 0.66);
  border: 1px solid rgba(148, 163, 184, 0.18);
  box-shadow: 0 12px 28px rgba(0, 0, 0, 0.24);
  color: #5eead4;
}

.home-page.home-dark .home-nav-text-link {
  color: #cbd5e1;
}

.home-page.home-dark .home-nav-text-link:hover {
  background: rgba(15, 23, 42, 0.66);
  box-shadow: 0 12px 28px rgba(0, 0, 0, 0.24);
  color: #5eead4;
}

.home-page.home-dark .home-announcement-badge {
  border-color: #020617;
}

.home-page.home-dark .home-title-gradient {
  background: linear-gradient(90deg, #99f6e4 0%, #67e8f9 50%, #f8fafc 100%);
  -webkit-background-clip: text;
  background-clip: text;
  text-shadow: none;
}

.home-page.home-dark .home-title-shimmer::after {
  background: linear-gradient(
    105deg,
    transparent 0%,
    rgba(94, 234, 212, 0) 34%,
    rgba(255, 255, 255, 0.2) 48%,
    rgba(94, 234, 212, 0) 62%,
    transparent 100%
  );
  mix-blend-mode: normal;
}

.home-page.home-dark .home-icon-soft {
  background: linear-gradient(135deg, rgba(45, 212, 191, 0.16), rgba(8, 145, 178, 0.12));
  border: 1px solid rgba(94, 234, 212, 0.16);
  color: #5eead4;
}

.home-page.home-dark .home-url-field {
  background: rgba(2, 6, 23, 0.48);
  border: 1px solid rgba(148, 163, 184, 0.2);
  color: #f8fafc;
}

.home-page.home-dark .home-url-copy {
  background: rgba(30, 41, 59, 0.92);
  color: #cbd5e1;
}

.home-page.home-dark .home-url-copy:hover {
  background: rgba(51, 65, 85, 0.94);
  color: #fff;
}

.home-page.home-dark .home-api-base {
  color: #f8fafc;
}

.home-page.home-dark .home-stat-card {
  border-color: rgba(94, 234, 212, 0.22);
  background: rgba(15, 23, 42, 0.42);
}

.home-page.home-dark .home-url-card {
  border-color: rgba(148, 163, 184, 0.22);
  background: linear-gradient(180deg, rgba(15, 23, 42, 0.58), rgba(15, 23, 42, 0.4));
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.06),
    0 24px 58px rgba(0, 0, 0, 0.26);
}

.home-page.home-dark .home-hero-panel,
.home-page.home-dark .home-hero-card,
.home-page.home-dark .home-value-card,
.home-page.home-dark .home-workflow-card,
.home-page.home-dark .home-channel-card {
  border-color: rgba(148, 163, 184, 0.18);
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.82), rgba(8, 22, 32, 0.62)),
    rgba(15, 23, 42, 0.66);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.05),
    0 26px 70px rgba(0, 0, 0, 0.28);
}

.home-page.home-dark .home-hero-card {
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.86), rgba(8, 22, 32, 0.72)),
    rgba(15, 23, 42, 0.74);
}

.home-page.home-dark .home-value-card:hover,
.home-page.home-dark .home-workflow-card:hover,
.home-page.home-dark .home-channel-card:hover,
.home-page.home-dark .home-hero-card:hover {
  border-color: rgba(94, 234, 212, 0.3);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.06),
    0 24px 62px rgba(0, 0, 0, 0.34);
}

.home-page.home-dark .home-card-wash {
  background: linear-gradient(100deg, rgba(20, 184, 166, 0.12), rgba(6, 182, 212, 0.06), transparent 76%);
}

.home-page.home-dark .home-endpoint-rotator {
  color: #67e8f9;
}

.home-page.home-dark .home-section-label {
  color: #7dd3fc;
}

.home-page.home-dark .home-inline-link {
  color: #5eead4;
}

.home-page.home-dark .home-inline-link:hover {
  color: #67e8f9;
}

.home-page.home-dark .home-status-showcase {
  border-color: rgba(148, 163, 184, 0.2);
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.78), rgba(8, 22, 32, 0.62)),
    rgba(15, 23, 42, 0.66);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.05),
    0 26px 70px rgba(0, 0, 0, 0.28);
}

.home-page.home-dark .home-provider-custom {
  background: linear-gradient(135deg, #475569, #0f172a);
}

.home-page.home-dark .home-section.bg-white\/70 {
  background:
    linear-gradient(180deg, rgba(8, 13, 24, 0.2), rgba(8, 13, 24, 0.08));
}

.home-page.home-dark :where(.border-gray-200\/60, .dark\:border-dark-800\/70) {
  border-color: rgba(51, 65, 85, 0.66);
}

.home-page.home-dark .home-final-card {
  border-color: rgba(94, 234, 212, 0.18);
  background:
    linear-gradient(135deg, rgba(20, 184, 166, 0.18), rgba(15, 23, 42, 0.74) 44%, rgba(8, 145, 178, 0.12)),
    rgba(15, 23, 42, 0.72);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.06),
    0 24px 70px rgba(0, 0, 0, 0.28);
}

:global(.dark) .home-announcement-backdrop {
  background:
    radial-gradient(circle at 50% 20%, rgba(20, 184, 166, 0.16), transparent 34%),
    rgba(2, 6, 23, 0.74);
}

:global(.dark) .home-announcement-modal {
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.98), rgba(8, 18, 30, 0.98)),
    #0f172a;
  border-color: rgba(148, 163, 184, 0.22);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.06),
    0 34px 90px rgba(0, 0, 0, 0.5);
}

:global(.dark) .home-announcement-modal-head,
:global(.dark) .home-announcement-modal-foot {
  border-color: rgba(51, 65, 85, 0.68);
}

:global(.dark) .home-announcement-modal-foot,
:global(.dark) .home-announcement-tabs {
  background: rgba(2, 6, 23, 0.42);
}

:global(.dark) .home-announcement-tab {
  color: #94a3b8;
}

:global(.dark) .home-announcement-tab.is-active {
  background: linear-gradient(135deg, #14b8a6, #0891b2);
  box-shadow: 0 12px 22px rgba(20, 184, 166, 0.22);
  color: #fff;
}

:global(.dark) .home-announcement-close:hover {
  background: rgba(30, 41, 59, 0.88);
  color: #e2e8f0;
}

:global(.dark) .home-announcement-timeline-item::before {
  background: #334155;
}

:global(.dark) .home-announcement-timeline-dot {
  background: #475569;
  border-color: #0f172a;
  box-shadow: 0 0 0 1px #334155;
}

:global(.dark) .home-announcement-timeline-item.is-unread .home-announcement-timeline-dot {
  background: #5eead4;
  box-shadow: 0 0 0 4px rgba(94, 234, 212, 0.12);
}

:global(.dark) .home-announcement-timeline-card {
  border-color: rgba(51, 65, 85, 0.7);
}

:global(.dark) .home-announcement-unread-pill {
  background: rgba(20, 184, 166, 0.18);
  color: #5eead4;
}

:global(.dark) .home-announcement-empty-illustration {
  background: linear-gradient(135deg, rgba(15, 23, 42, 0.88), rgba(8, 47, 73, 0.42));
  border-color: rgba(51, 65, 85, 0.86);
  color: #64748b;
}

:global(.dark) .home-announcement-empty-illustration span {
  border-color: #0f172a;
}

:global(.dark) .home-announcement-foot-button {
  background: rgba(15, 23, 42, 0.9);
  border-color: rgba(51, 65, 85, 0.9);
  color: #cbd5e1;
}

:global(.dark) .home-announcement-foot-button:hover {
  border-color: rgba(94, 234, 212, 0.42);
  color: #5eead4;
}

:global(.dark) .home-announcement-foot-button.is-primary {
  background: linear-gradient(135deg, #0d9488, #0891b2);
  border-color: transparent;
  color: #fff;
}

.home-announcement-backdrop.is-dark {
  background:
    radial-gradient(circle at 50% 20%, rgba(20, 184, 166, 0.16), transparent 34%),
    rgba(2, 6, 23, 0.76);
}

.home-announcement-backdrop.is-dark .home-announcement-modal {
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.98), rgba(8, 18, 30, 0.98)),
    #0f172a;
  border-color: rgba(148, 163, 184, 0.22);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.06),
    0 34px 90px rgba(0, 0, 0, 0.52);
}

.home-announcement-backdrop.is-dark .home-announcement-modal-head,
.home-announcement-backdrop.is-dark .home-announcement-modal-foot {
  border-color: rgba(51, 65, 85, 0.68);
}

.home-announcement-backdrop.is-dark .home-announcement-modal-foot,
.home-announcement-backdrop.is-dark .home-announcement-tabs {
  background: rgba(2, 6, 23, 0.42);
}

.home-announcement-backdrop.is-dark :where(.text-gray-950, .text-gray-900, .dark\:text-white) {
  color: #f8fafc;
}

.home-announcement-backdrop.is-dark :where(.text-gray-600, .text-gray-500, .dark\:text-dark-300, .dark\:text-dark-400) {
  color: #cbd5e1;
}

.home-announcement-backdrop.is-dark :where(.text-gray-400, .dark\:text-dark-500) {
  color: #94a3b8;
}

.home-announcement-backdrop.is-dark .home-announcement-tab {
  color: #94a3b8;
}

.home-announcement-backdrop.is-dark .home-announcement-tab.is-active {
  background: linear-gradient(135deg, #14b8a6, #0891b2);
  box-shadow: 0 12px 22px rgba(20, 184, 166, 0.22);
  color: #fff;
}

.home-announcement-backdrop.is-dark .home-announcement-close {
  color: #94a3b8;
}

.home-announcement-backdrop.is-dark .home-announcement-close:hover {
  background: rgba(30, 41, 59, 0.88);
  color: #e2e8f0;
}

.home-announcement-backdrop.is-dark .home-announcement-empty-illustration {
  background: linear-gradient(135deg, rgba(15, 23, 42, 0.88), rgba(8, 47, 73, 0.42));
  border-color: rgba(51, 65, 85, 0.86);
  color: #64748b;
}

.home-announcement-backdrop.is-dark .home-announcement-empty-illustration span {
  border-color: #0f172a;
}

.home-announcement-backdrop.is-dark .home-announcement-foot-button {
  background: rgba(15, 23, 42, 0.9);
  border-color: rgba(51, 65, 85, 0.9);
  color: #cbd5e1;
}

.home-announcement-backdrop.is-dark .home-announcement-foot-button:hover {
  border-color: rgba(94, 234, 212, 0.42);
  color: #5eead4;
}

.home-announcement-backdrop.is-dark .home-announcement-foot-button.is-primary {
  background: linear-gradient(135deg, #14b8a6, #0891b2);
  border-color: transparent;
  color: #fff;
}

@media (prefers-reduced-motion: reduce) {
  .home-snap-container {
    scroll-behavior: auto;
  }

  .home-endpoint-path,
  .home-hero-card,
  .home-title-shimmer::after {
    animation: none;
  }

  .home-channel-card {
    opacity: 1;
    transform: none;
    transition: none;
  }
}

@media (max-height: 760px) and (min-width: 1024px) {
  .home-snap-section {
    padding-bottom: 2rem;
    padding-top: 5.75rem;
  }

  .home-hero-shell {
    min-height: calc(100vh - 7.75rem);
  }

  .home-hero-visual {
    min-height: 20rem;
  }
}

@media (max-width: 1279px) and (min-width: 1024px) {
  .home-nav {
    max-width: min(100% - 2rem, 70rem);
  }

  .home-hero-shell {
    max-width: 67rem;
    grid-template-columns: minmax(0, 35rem) minmax(21rem, 26rem);
    gap: 2rem;
  }

  .home-hero-copy {
    max-width: 35rem;
  }

  .home-hero-copy h1 {
    font-size: 2.72rem;
  }

  .home-hero-copy p {
    max-width: 34rem;
  }

  .home-hero-panel {
    max-width: 26rem;
  }

  .home-hero-visual {
    min-height: 21rem;
  }

  .home-url-card {
    max-width: 28rem;
  }
}

@media (max-width: 1023px) {
  .home-snap-container {
    scroll-snap-type: y proximity;
  }

  .home-snap-section {
    min-height: 100vh;
    padding-bottom: 2.75rem;
    padding-top: 5.75rem;
  }

  .home-hero-copy h1 {
    font-size: clamp(2.18rem, 7vw, 2.78rem);
  }

  .home-hero-copy p {
    margin-top: 1.1rem;
    font-size: 0.95rem;
    line-height: 1.7;
  }

  .home-hero-shell {
    min-height: auto;
  }

  .home-hero-visual {
    min-height: 0;
    padding-top: 0;
  }

  .home-hero-panel {
    gap: 0.75rem;
    margin-top: 1rem;
    padding: 0.75rem;
  }

  .home-hero-card {
    padding: 0.75rem;
  }

  .home-hero-card p {
    display: none;
  }

  .home-stats-grid {
    display: none;
  }

  .home-hero-card:nth-child(2),
  .home-hero-card:nth-child(3) {
    margin-left: 0;
  }
}

@media (max-width: 420px) {
  .home-nav {
    max-width: min(100% - 1.5rem, 104rem);
  }

  .home-announcement-backdrop {
    align-items: flex-end;
    padding: 0.75rem;
  }

  .home-announcement-modal {
    border-radius: 1rem;
    max-height: 86vh;
  }

  .home-announcement-modal-head {
    flex-direction: column;
  }

  .home-announcement-head-actions {
    justify-content: space-between;
    width: 100%;
  }

  .home-announcement-tabs {
    flex: 1 1 auto;
  }

  .home-announcement-tab {
    flex: 1 1 0;
  }

  .home-announcement-modal-body {
    min-height: 18rem;
    padding: 1rem;
  }

  .home-announcement-modal-foot {
    padding: 0.85rem 1rem;
  }

  .home-snap-section {
    padding-top: 6.25rem;
  }

  .home-url-card {
    padding: 1rem;
  }

  .home-url-field {
    align-items: flex-start;
    border-radius: 1.15rem;
    flex-direction: column;
    font-size: 1.08rem;
    gap: 0.75rem;
    min-height: 0;
    padding: 1rem;
  }

  .home-url-copy {
    align-self: flex-end;
    height: 2.5rem;
    width: 2.5rem;
  }

  .home-url-content {
    align-items: flex-start;
    flex-direction: column;
    gap: 0.35rem;
  }

  .home-endpoint-rotator {
    padding-left: 0;
    text-align: left;
  }

  .home-hero-card {
    flex-direction: column;
  }

  .home-value-card,
  .home-workflow-card {
    padding: 1.5rem;
  }
}
</style>
