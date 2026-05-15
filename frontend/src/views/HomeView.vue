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
    class="home-page relative flex min-h-screen flex-col overflow-hidden bg-gradient-to-br from-gray-50 via-primary-50/40 to-cyan-50/30 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950"
  >
    <div class="home-bg" aria-hidden="true">
      <div class="home-blob home-blob-a"></div>
      <div class="home-blob home-blob-b"></div>
      <div class="home-blob home-blob-c"></div>
      <div class="home-grid"></div>
    </div>

    <!-- Header -->
    <header class="relative z-20 px-4 py-4 sm:px-6">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-4">
        <router-link to="/home" class="flex min-w-0 items-center gap-3">
          <div class="h-10 w-10 overflow-hidden rounded-xl bg-white shadow-md ring-1 ring-gray-200/70 dark:bg-dark-800 dark:ring-dark-700">
            <img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" class="h-full w-full object-contain" />
          </div>
          <span class="truncate text-lg font-black tracking-tight text-gray-950 dark:text-white sm:text-xl">
            {{ siteName }}
          </span>
        </router-link>

        <div class="flex items-center gap-3">
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
          <router-link
            v-else
            to="/login"
            class="inline-flex items-center rounded-full bg-gray-900 px-3 py-1 text-xs font-medium text-white transition-colors hover:bg-gray-800 dark:bg-gray-800 dark:hover:bg-gray-700"
          >
            {{ t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <!-- Main Content -->
    <main class="relative z-10 flex-1">
      <section class="home-section home-hero-section px-4 pb-20 pt-16 sm:px-6 lg:pb-28 lg:pt-24">
        <div class="mx-auto grid max-w-7xl items-center gap-12 lg:grid-cols-[1.05fr_0.95fr] lg:gap-16">
          <div class="mx-auto max-w-2xl text-center lg:mx-0 lg:text-left">
            <div class="inline-flex rounded-full border border-primary-200 bg-primary-50/80 px-4 py-2 text-xs font-bold uppercase tracking-[0.18em] text-primary-700 shadow-sm dark:border-primary-800/70 dark:bg-primary-950/40 dark:text-primary-300">
              {{ t('home.hero.eyebrow') }}
            </div>
            <h1 class="mt-8 text-5xl font-black leading-[1.03] tracking-[-0.06em] text-gray-950 dark:text-white sm:text-6xl lg:text-7xl">
              {{ t('home.hero.titleLine1') }}
              <br />
              {{ t('home.hero.titleLine2') }}
              <span class="home-title-gradient">{{ t('home.hero.titleHighlight') }}</span>
            </h1>
            <p class="mt-8 text-lg leading-8 text-gray-600 dark:text-dark-300 sm:text-xl">
              {{ t('home.hero.description') }}
            </p>

            <div class="mt-10 rounded-[1.75rem] border border-gray-200/80 bg-white/80 p-5 text-left shadow-card-hover backdrop-blur-xl dark:border-dark-700/70 dark:bg-dark-900/70">
              <div class="mb-3 text-sm font-medium text-gray-500 dark:text-dark-400">
                {{ t('home.hero.baseUrlLabel') }}
              </div>
              <div class="relative overflow-hidden rounded-2xl bg-gray-100/80 py-3 pl-4 pr-11 font-mono text-sm text-gray-900 dark:bg-dark-800 dark:text-dark-100 sm:text-base">
                <div class="flex min-h-6 min-w-0 flex-wrap items-center">
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
                <Icon name="copy" size="sm" class="absolute right-4 top-1/2 -translate-y-1/2 text-gray-400" />
              </div>
            </div>

            <div class="mt-9 flex flex-col items-center gap-3 sm:flex-row lg:justify-start">
              <router-link
                :to="isAuthenticated ? dashboardPath : '/login'"
                class="btn btn-primary min-w-36 px-7 py-3 text-base shadow-lg shadow-primary-500/30"
              >
                {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
                <Icon name="arrowRight" size="md" class="ml-2" :stroke-width="2" />
              </router-link>
              <a
                v-if="docUrl"
                :href="docUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="btn btn-secondary min-w-32 px-7 py-3 text-base"
              >
                <Icon name="book" size="md" />
                {{ t('home.docs') }}
              </a>
            </div>

            <div class="mt-10 grid grid-cols-3 gap-3 sm:max-w-lg sm:gap-5">
              <div
                v-for="stat in heroStats"
                :key="stat.label"
                class="rounded-2xl border border-dashed border-primary-200 bg-white/60 p-4 text-left shadow-sm backdrop-blur-sm dark:border-primary-900/70 dark:bg-dark-900/50"
              >
                <div class="text-2xl font-black tracking-tight text-gray-950 dark:text-white sm:text-3xl">
                  {{ stat.value }}
                </div>
                <div class="mt-2 text-xs font-medium text-gray-500 dark:text-dark-400 sm:text-sm">
                  {{ stat.label }}
                </div>
              </div>
            </div>
          </div>

          <div class="home-hero-panel">
            <div
              v-for="feature in heroFeatures"
              :key="feature.title"
              class="home-hero-card"
            >
              <div class="home-icon-soft">
                <Icon :name="feature.icon" size="lg" />
              </div>
              <div>
                <h3 class="text-lg font-bold text-gray-950 dark:text-white">{{ feature.title }}</h3>
                <p class="mt-3 text-sm leading-6 text-gray-500 dark:text-dark-300">{{ feature.description }}</p>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section class="home-section bg-white/70 px-4 py-20 dark:bg-dark-950/30 sm:px-6 lg:py-28">
        <div class="mx-auto max-w-7xl">
          <div class="max-w-4xl">
            <div class="home-section-label">{{ t('home.value.eyebrow') }}</div>
            <h2 class="mt-5 text-4xl font-black tracking-[-0.05em] text-gray-950 dark:text-white sm:text-5xl">
              {{ t('home.value.title') }}
            </h2>
            <p class="mt-5 text-lg leading-8 text-gray-600 dark:text-dark-300">
              {{ t('home.value.description') }}
            </p>
          </div>

          <div class="mt-12 grid gap-6 lg:grid-cols-2">
            <article
              v-for="card in valueCards"
              :key="card.title"
              class="home-value-card"
            >
              <div class="home-card-wash"></div>
              <div class="home-icon-soft">
                <Icon :name="card.icon" size="lg" />
              </div>
              <h3 class="mt-8 text-2xl font-bold tracking-tight text-gray-950 dark:text-white">
                {{ card.title }}
              </h3>
              <p class="mt-4 text-base leading-7 text-gray-600 dark:text-dark-300">
                {{ card.description }}
              </p>
            </article>
          </div>
        </div>
      </section>

      <section class="home-section px-4 py-20 sm:px-6 lg:py-28">
        <div class="mx-auto max-w-7xl">
          <div class="max-w-4xl">
            <div class="home-section-label">{{ t('home.workflow.eyebrow') }}</div>
            <h2 class="mt-5 text-4xl font-black tracking-[-0.05em] text-gray-950 dark:text-white sm:text-5xl">
              {{ t('home.workflow.title') }}
            </h2>
          </div>

          <div class="mt-12 grid gap-6 lg:grid-cols-3">
            <article
              v-for="step in workflowSteps"
              :key="step.step"
              class="home-workflow-card"
            >
              <div class="home-icon-soft rounded-full">
                <Icon :name="step.icon" size="lg" />
              </div>
              <div class="mt-5 text-sm font-black tracking-[0.2em] text-gray-400 dark:text-dark-500">
                {{ step.step }}
              </div>
              <h3 class="mt-4 text-2xl font-bold tracking-tight text-gray-950 dark:text-white">
                {{ step.title }}
              </h3>
              <p class="mt-4 text-base leading-7 text-gray-600 dark:text-dark-300">
                {{ step.description }}
              </p>
            </article>
          </div>
        </div>
      </section>

      <section class="home-section bg-white/70 px-4 py-20 dark:bg-dark-950/30 sm:px-6 lg:py-28">
        <div class="mx-auto max-w-7xl text-center">
          <div class="home-section-label justify-center">{{ t('home.channels.eyebrow') }}</div>
          <h2 class="mt-5 text-4xl font-black tracking-[-0.05em] text-gray-950 dark:text-white sm:text-5xl">
            {{ t('home.channels.title') }}
          </h2>
          <p class="mx-auto mt-5 max-w-3xl text-lg leading-8 text-gray-600 dark:text-dark-300">
            {{ t('home.channels.description') }}
          </p>

          <div class="mt-12 grid gap-5 sm:grid-cols-2 lg:grid-cols-5">
            <article
              v-for="channel in supportedChannels"
              :key="channel.name"
              class="home-channel-card"
              :class="{ 'home-channel-card-muted': channel.isCustom }"
            >
              <div class="home-provider-mark" :class="channel.markClass">{{ channel.shortName }}</div>
              <h3 class="mt-5 text-xl font-bold text-gray-950 dark:text-white">{{ channel.name }}</h3>
              <p class="mt-3 min-h-12 text-sm leading-6 text-gray-500 dark:text-dark-300">
                {{ channel.description }}
              </p>
              <span class="mt-5 inline-flex rounded-full bg-primary-50 px-3 py-1 text-xs font-semibold text-primary-700 dark:bg-primary-950/50 dark:text-primary-300">
                {{ channel.status }}
              </span>
            </article>
          </div>
        </div>
      </section>

      <!-- Service Status -->
      <section class="home-section px-4 py-20 sm:px-6 lg:py-24">
        <div class="mx-auto max-w-7xl">
          <ServiceStatusOverview />
        </div>
      </section>

      <section class="home-section px-4 pb-20 pt-4 sm:px-6 lg:pb-28">
        <div class="mx-auto max-w-6xl overflow-hidden rounded-[2rem] border border-primary-100 bg-gradient-to-br from-primary-100 via-white to-cyan-50 p-8 text-center shadow-card-hover dark:border-primary-900/50 dark:from-primary-950/50 dark:via-dark-900 dark:to-dark-950 sm:p-12">
          <h2 class="text-3xl font-black tracking-[-0.04em] text-gray-950 dark:text-white sm:text-4xl">
            {{ t('home.cta.title') }}
          </h2>
          <p class="mx-auto mt-5 max-w-3xl text-base leading-7 text-gray-600 dark:text-dark-300 sm:text-lg">
            {{ t('home.cta.description') }}
          </p>
          <div class="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="btn btn-primary px-7 py-3 text-base shadow-lg shadow-primary-500/30"
            >
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.cta.button') }}
              <Icon name="arrowRight" size="md" />
            </router-link>
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="btn btn-secondary px-7 py-3 text-base"
            >
              <Icon name="book" size="md" />
              {{ t('home.docs') }}
            </a>
          </div>
        </div>
      </section>
    </main>

    <!-- Footer -->
    <footer class="relative z-10 border-t border-gray-200/50 px-6 py-8 dark:border-dark-800/50">
      <div
        class="mx-auto flex max-w-7xl flex-col items-center justify-between gap-4 text-center sm:flex-row sm:text-left"
      >
        <p class="text-sm text-gray-500 dark:text-dark-400">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <div class="flex flex-wrap items-center justify-center gap-x-4 gap-y-2">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
          >
            {{ t('home.docs') }}
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
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import ServiceStatusOverview from '@/components/status/ServiceStatusOverview.vue'
import { normalizeSiteName } from '@/utils/siteBrand'

const { t } = useI18n()

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

})
</script>

<style scoped>
.home-page {
  isolation: isolate;
}

.home-bg {
  pointer-events: none;
  position: absolute;
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

.home-hero-section {
  min-height: calc(100vh - 72px);
}

.home-title-gradient {
  background: linear-gradient(90deg, #0f172a 0%, #0d9488 48%, rgba(20, 184, 166, 0.2) 100%);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.home-hero-panel {
  border: 1px solid rgba(226, 232, 240, 0.82);
  border-radius: 2rem;
  background: rgba(255, 255, 255, 0.82);
  box-shadow: 0 24px 60px rgba(15, 23, 42, 0.12);
  display: grid;
  gap: 1.25rem;
  padding: 1.5rem;
  backdrop-filter: blur(18px);
}

.home-hero-card {
  align-items: flex-start;
  border: 1px solid rgba(226, 232, 240, 0.9);
  border-radius: 1.25rem;
  background: rgba(255, 255, 255, 0.92);
  box-shadow: 0 6px 16px rgba(15, 23, 42, 0.04);
  display: flex;
  gap: 1rem;
  padding: 1.25rem;
}

.home-icon-soft {
  align-items: center;
  background: linear-gradient(135deg, #ccfbf1 0%, #ecfeff 100%);
  border-radius: 1rem;
  color: #0d9488;
  display: inline-flex;
  flex: 0 0 auto;
  height: 3.25rem;
  justify-content: center;
  width: 3.25rem;
}

.home-section-label {
  align-items: center;
  color: #0f766e;
  display: inline-flex;
  font-size: 0.875rem;
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
}

.home-value-card {
  border-radius: 1.5rem;
  overflow: hidden;
  padding: 2rem;
  position: relative;
}

.home-card-wash {
  background: linear-gradient(100deg, rgba(20, 184, 166, 0.16), rgba(6, 182, 212, 0.08), transparent 76%);
  border-bottom-left-radius: 70%;
  border-bottom-right-radius: 45%;
  height: 6rem;
  left: 3rem;
  position: absolute;
  top: 0;
  transform: skewX(-8deg);
  width: 75%;
}

.home-workflow-card {
  border-radius: 1.5rem;
  overflow: hidden;
  padding: 2rem;
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
  border-radius: 1.375rem;
  display: flex;
  flex-direction: column;
  min-height: 14rem;
  padding: 1.5rem;
}

.home-channel-card-muted {
  background: rgba(248, 250, 252, 0.88);
}

.home-provider-mark {
  align-items: center;
  border-radius: 1rem;
  color: #fff;
  display: inline-flex;
  font-size: 1.125rem;
  font-weight: 900;
  height: 3.25rem;
  justify-content: center;
  width: 3.25rem;
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

.home-api-base {
  overflow-wrap: anywhere;
}

.home-endpoint-rotator {
  color: #0d9488;
  display: inline-grid;
  font-weight: 700;
  min-height: 1.5em;
  overflow: hidden;
  vertical-align: bottom;
}

.home-endpoint-path {
  animation: home-endpoint-cycle 13s infinite;
  grid-area: 1 / 1;
  opacity: 0;
  overflow-wrap: anywhere;
  transform: translateY(0.6rem);
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

:global(.dark) .home-grid {
  background-image:
    linear-gradient(rgba(20, 184, 166, 0.06) 1px, transparent 1px),
    linear-gradient(90deg, rgba(20, 184, 166, 0.06) 1px, transparent 1px);
}

:global(.dark) .home-title-gradient {
  background: linear-gradient(90deg, #fff 0%, #5eead4 50%, rgba(20, 184, 166, 0.4) 100%);
  -webkit-background-clip: text;
  background-clip: text;
}

:global(.dark) .home-hero-panel,
:global(.dark) .home-hero-card,
:global(.dark) .home-value-card,
:global(.dark) .home-workflow-card,
:global(.dark) .home-channel-card {
  border-color: rgba(51, 65, 85, 0.82);
  background: rgba(15, 23, 42, 0.72);
}

:global(.dark) .home-endpoint-rotator {
  color: #5eead4;
}

@media (max-width: 640px) {
  .home-hero-card {
    flex-direction: column;
  }

  .home-value-card,
  .home-workflow-card {
    padding: 1.5rem;
  }
}
</style>
