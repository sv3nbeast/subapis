<template>
  <div
    class="docs-guide min-h-screen overflow-hidden bg-gradient-to-br from-gray-50 via-primary-50/40 to-cyan-50/30 text-gray-950 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950 dark:text-white"
    :class="{ 'docs-guide-dark': isDark }"
  >
    <div class="docs-guide-bg" aria-hidden="true">
      <div class="docs-guide-blob docs-guide-blob-a"></div>
      <div class="docs-guide-blob docs-guide-blob-b"></div>
      <div class="docs-guide-grid"></div>
    </div>

    <header class="sticky top-0 z-40 px-4 py-3 sm:px-6">
      <nav class="docs-guide-nav mx-auto flex max-w-7xl items-center justify-between gap-4">
        <RouterLink to="/home" class="flex min-w-0 items-center gap-2.5">
          <span class="h-9 w-9 overflow-hidden rounded-xl bg-white shadow-md ring-1 ring-gray-200/70 dark:bg-dark-800 dark:ring-dark-700">
            <img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" class="h-full w-full object-contain" />
          </span>
          <span class="docs-guide-brand truncate text-base font-black tracking-tight text-gray-950 dark:text-white sm:text-lg">
            {{ siteName }}
          </span>
        </RouterLink>

        <div class="flex items-center gap-2 sm:gap-3">
          <RouterLink to="/home" class="docs-guide-nav-link hidden sm:inline-flex">
            {{ t('docsGuide.nav.home') }}
          </RouterLink>
          <RouterLink to="/monitor" class="docs-guide-nav-link hidden sm:inline-flex">
            {{ t('docsGuide.nav.status') }}
          </RouterLink>
          <LocaleSwitcher />
          <button
            type="button"
            class="docs-guide-icon-button"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <RouterLink
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="docs-guide-dashboard-link"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </RouterLink>
        </div>
      </nav>
    </header>

    <main class="relative z-10 mx-auto max-w-7xl px-4 pb-16 pt-8 sm:px-6 lg:pb-24 lg:pt-12">
      <section class="grid items-center gap-8 lg:grid-cols-[minmax(0,1.05fr)_minmax(21rem,0.95fr)]">
        <div>
          <div class="docs-guide-eyebrow">
            {{ t('docsGuide.hero.eyebrow') }}
          </div>
          <h1 class="mt-5 max-w-4xl text-[2rem] font-black leading-[1.08] tracking-[-0.035em] text-gray-950 dark:text-white sm:text-[2.4rem] lg:text-[2.85rem]">
            {{ t('docsGuide.hero.title') }}
          </h1>
          <p class="mt-5 max-w-2xl text-base leading-8 text-gray-600 dark:text-dark-300 sm:text-lg">
            {{ t('docsGuide.hero.description') }}
          </p>
          <div class="mt-7 flex flex-col gap-3 sm:flex-row">
            <RouterLink
              :to="isAuthenticated ? dashboardPath : '/register'"
              class="btn btn-primary px-6 py-3 text-sm shadow-lg shadow-primary-500/30"
            >
              {{ isAuthenticated ? t('home.goToDashboard') : t('docsGuide.hero.primaryCta') }}
              <Icon name="arrowRight" size="md" />
            </RouterLink>
            <a href="#quick-start" class="btn btn-secondary px-6 py-3 text-sm">
              <Icon name="book" size="md" />
              {{ t('docsGuide.hero.secondaryCta') }}
            </a>
          </div>
        </div>

        <div class="docs-guide-terminal">
          <div class="docs-guide-terminal-head">
            <span></span>
            <span></span>
            <span></span>
          </div>
          <div class="space-y-4 p-5">
            <div>
              <p class="docs-guide-terminal-label">{{ t('docsGuide.hero.baseUrlLabel') }}</p>
              <div class="docs-guide-code-line">
                {{ apiBaseUrl }}<span>/v1/chat/completions</span>
              </div>
            </div>
            <div>
              <p class="docs-guide-terminal-label">{{ t('docsGuide.hero.envLabel') }}</p>
              <pre><code>OPENAI_API_KEY=sk-...
OPENAI_BASE_URL={{ apiBaseUrl }}/v1</code></pre>
            </div>
          </div>
        </div>
      </section>

      <section id="docs-center" class="mt-14 grid gap-8 lg:grid-cols-[18rem_minmax(0,1fr)] lg:items-start">
        <aside class="docs-guide-sidebar">
          <p class="px-3 text-xs font-black uppercase tracking-[0.18em] text-primary-700 dark:text-primary-300">
            {{ t('docsGuide.sidebar.title') }}
          </p>
          <nav class="mt-4 space-y-4">
            <div
              v-for="group in docsNavigation"
              :key="group.title"
              class="docs-guide-nav-group"
            >
              <p class="docs-guide-sidebar-group">{{ group.title }}</p>
              <div class="mt-1.5 space-y-1">
                <a
                  v-for="link in group.links"
                  :key="link.id"
                  :href="`#${link.id}`"
                  class="docs-guide-side-link"
                >
                  <span>{{ link.badge }}</span>
                  {{ link.title }}
                </a>
              </div>
            </div>
          </nav>
        </aside>

        <div class="space-y-5">
          <section id="quick-start" class="docs-guide-content-section scroll-mt-28">
            <div class="docs-guide-section-head">
              <div class="docs-guide-eyebrow">{{ t('docsGuide.quickStart.eyebrow') }}</div>
              <h2>{{ t('docsGuide.quickStart.title') }}</h2>
              <p>{{ t('docsGuide.quickStart.description') }}</p>
            </div>

            <div class="mt-5 space-y-4">
              <article
                v-for="step in quickStartSteps"
                :id="step.id"
                :key="step.id"
                class="docs-guide-step-card scroll-mt-28"
              >
                <div class="flex flex-col gap-4 sm:flex-row sm:items-start">
                  <div class="docs-guide-step-icon">
                    <Icon :name="step.icon" size="lg" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <div class="flex flex-wrap items-center gap-3">
                      <span class="docs-guide-step-number">{{ step.step }}</span>
                      <h3 class="text-lg font-black tracking-[-0.02em] text-gray-950 dark:text-white sm:text-xl">
                        {{ step.title }}
                      </h3>
                    </div>
                    <p class="mt-2.5 text-[0.86rem] leading-6 text-gray-600 dark:text-dark-300 sm:text-sm">
                      {{ step.description }}
                    </p>
                    <ul class="mt-4 grid gap-2.5 sm:grid-cols-2">
                      <li
                        v-for="item in step.items"
                        :key="item"
                        class="docs-guide-check-item"
                      >
                        <Icon name="checkCircle" size="sm" />
                        <span>{{ item }}</span>
                      </li>
                    </ul>
                  </div>
                </div>
              </article>
            </div>
          </section>

          <section
            v-for="section in guideSections"
            :id="section.id"
            :key="section.id"
            class="docs-guide-content-section scroll-mt-28"
          >
            <div class="docs-guide-section-head">
              <div class="docs-guide-eyebrow">{{ section.eyebrow }}</div>
              <h2>{{ section.title }}</h2>
              <p>{{ section.description }}</p>
            </div>

            <div class="mt-6 grid gap-5 xl:grid-cols-2">
              <article
                v-for="article in section.articles"
                :id="article.id"
                :key="article.id"
                class="docs-guide-article-card scroll-mt-28"
              >
                <div class="flex items-start gap-4">
                  <div class="docs-guide-step-icon docs-guide-article-icon">
                    <Icon :name="article.icon" size="md" />
                  </div>
                  <div class="min-w-0">
                    <p class="text-xs font-black uppercase tracking-[0.16em] text-primary-700 dark:text-primary-300">
                      {{ article.badge }}
                    </p>
                    <h3 class="mt-2 text-base font-black tracking-[-0.02em] text-gray-950 dark:text-white sm:text-lg">
                      {{ article.title }}
                    </h3>
                  </div>
                </div>

                <p class="mt-4 text-sm leading-7 text-gray-600 dark:text-dark-300">
                  {{ article.description }}
                </p>
                <ul class="mt-5 space-y-2.5">
                  <li
                    v-for="item in article.items"
                    :key="item"
                    class="docs-guide-check-item docs-guide-check-item-compact"
                  >
                    <Icon name="checkCircle" size="sm" />
                    <span>{{ item }}</span>
                  </li>
                </ul>
                <pre v-if="article.code" class="docs-guide-article-code"><code>{{ article.code }}</code></pre>
                <p v-if="article.note" class="docs-guide-note">
                  {{ article.note }}
                </p>
                <div v-if="article.links?.length" class="mt-5 flex flex-wrap gap-2">
                  <RouterLink
                    v-for="link in article.links"
                    :key="link.to"
                    :to="link.to"
                    class="docs-guide-pill-link"
                  >
                    {{ link.label }}
                  </RouterLink>
                </div>
              </article>
            </div>
          </section>

          <section id="endpoint-map" class="docs-guide-example-card scroll-mt-28">
            <div class="grid gap-6 lg:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)]">
              <div>
                <div class="docs-guide-eyebrow">
                  {{ t('docsGuide.examples.eyebrow') }}
                </div>
                <h2 class="mt-4 text-2xl font-black tracking-[-0.035em] text-gray-950 dark:text-white sm:text-[1.85rem]">
                  {{ t('docsGuide.examples.title') }}
                </h2>
                <p class="mt-4 text-sm leading-7 text-gray-600 dark:text-dark-300 sm:text-base">
                  {{ t('docsGuide.examples.description') }}
                </p>
                <div class="mt-6 grid gap-3">
                  <div
                    v-for="endpoint in endpoints"
                    :key="endpoint.path"
                    class="docs-guide-endpoint-row"
                  >
                    <span>{{ endpoint.name }}</span>
                    <code>{{ endpoint.path }}</code>
                  </div>
                </div>
              </div>

              <div class="docs-guide-code-card">
                <div class="mb-3 flex items-center justify-between gap-3">
                  <span class="text-xs font-black uppercase tracking-[0.16em] text-primary-700 dark:text-primary-300">cURL</span>
                  <button type="button" class="docs-guide-copy-button" @click="copyCurlExample">
                    <Icon name="copy" size="sm" />
                    {{ t('common.copy') }}
                  </button>
                </div>
                <pre><code>{{ curlExample }}</code></pre>
              </div>
            </div>
          </section>
        </div>
      </section>

      <section class="docs-guide-bottom-cta">
        <div>
          <div class="docs-guide-eyebrow">{{ t('docsGuide.bottom.eyebrow') }}</div>
          <h2 class="mt-3 text-2xl font-black tracking-[-0.035em] text-gray-950 dark:text-white sm:text-[1.85rem]">
            {{ t('docsGuide.bottom.title') }}
          </h2>
          <p class="mt-3 max-w-2xl text-sm leading-7 text-gray-600 dark:text-dark-300 sm:text-base">
            {{ t('docsGuide.bottom.description') }}
          </p>
        </div>
        <RouterLink
          :to="isAuthenticated ? dashboardPath : '/register'"
          class="btn btn-primary shrink-0 px-6 py-3 text-sm shadow-lg shadow-primary-500/30"
        >
          {{ isAuthenticated ? t('home.goToDashboard') : t('docsGuide.bottom.button') }}
          <Icon name="arrowRight" size="md" />
        </RouterLink>
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { useAppStore, useAuthStore } from '@/stores'
import Icon from '@/components/icons/Icon.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import { useClipboard } from '@/composables/useClipboard'
import { normalizeSiteName } from '@/utils/siteBrand'

type GuideIconName = InstanceType<typeof Icon>['$props']['name']

interface QuickStartStep {
  id: string
  step: string
  icon: GuideIconName
  title: string
  description: string
  items: string[]
}

interface EndpointItem {
  name: string
  path: string
}

interface DocsNavLink {
  id: string
  badge: string
  title: string
}

interface DocsNavGroup {
  title: string
  links: DocsNavLink[]
}

interface GuideArticle {
  id: string
  badge: string
  icon: GuideIconName
  title: string
  description: string
  items: string[]
  code?: string
  note?: string
  links?: Array<{
    label: string
    to: string
  }>
}

interface GuideSection {
  id: string
  eyebrow: string
  title: string
  description: string
  articles: GuideArticle[]
}

const { t, tm } = useI18n()
const { copyToClipboard } = useClipboard()
const appStore = useAppStore()
const authStore = useAuthStore()
const isDark = ref(document.documentElement.classList.contains('dark'))

const siteName = computed(() => normalizeSiteName(appStore.cachedPublicSettings?.site_name || appStore.siteName))
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => (isAdmin.value ? '/admin/dashboard' : '/dashboard'))
const apiBaseUrl = computed(() => normalizeBaseUrl(appStore.cachedPublicSettings?.api_base_url || appStore.apiBaseUrl || 'https://subapis.com'))

const docsNavigation = computed<DocsNavGroup[]>(() => [
  {
    title: t('docsGuide.navGroups.quickStart'),
    links: [
      { id: 'quick-start', badge: '00', title: t('docsGuide.quickStart.title') },
      ...quickStartSteps.value.map((step) => ({ id: step.id, badge: step.step, title: step.title }))
    ]
  },
  {
    title: t('docsGuide.navGroups.models'),
    links: [
      { id: 'model-center', badge: 'M1', title: t('docsGuide.sections.models.articles.market.title') },
      { id: 'token-groups', badge: 'M2', title: t('docsGuide.sections.models.articles.groups.title') },
      { id: 'endpoint-map', badge: 'API', title: t('docsGuide.examples.title') }
    ]
  },
  {
    title: t('docsGuide.navGroups.cli'),
    links: [
      { id: 'cli-overview', badge: 'CLI', title: t('docsGuide.sections.cli.title') },
      { id: 'claude-code', badge: 'CC', title: t('docsGuide.sections.cli.articles.claude.title') },
      { id: 'codex-cli', badge: 'CX', title: t('docsGuide.sections.cli.articles.codex.title') },
      { id: 'gemini-cli', badge: 'GM', title: t('docsGuide.sections.cli.articles.gemini.title') },
      { id: 'cc-switch', badge: 'SW', title: t('docsGuide.sections.cli.articles.ccSwitch.title') },
      { id: 'cc-switch-cli', badge: 'SC', title: t('docsGuide.sections.cli.articles.ccSwitchCli.title') }
    ]
  },
  {
    title: t('docsGuide.navGroups.more'),
    links: [
      { id: 'advanced-usage', badge: 'AD', title: t('docsGuide.sections.advanced.title') },
      { id: 'image-models', badge: 'IMG', title: t('docsGuide.sections.advanced.articles.image.title') },
      { id: 'faq', badge: 'FAQ', title: t('docsGuide.sections.faq.title') },
      { id: 'policies', badge: 'TOS', title: t('docsGuide.sections.policies.title') }
    ]
  }
])

const quickStartSteps = computed<QuickStartStep[]>(() => [
  {
    id: 'register',
    step: '01',
    icon: 'userPlus',
    title: t('docsGuide.steps.register.title'),
    description: t('docsGuide.steps.register.description'),
    items: tm('docsGuide.steps.register.items') as string[]
  },
  {
    id: 'login',
    step: '02',
    icon: 'login',
    title: t('docsGuide.steps.login.title'),
    description: t('docsGuide.steps.login.description'),
    items: tm('docsGuide.steps.login.items') as string[]
  },
  {
    id: 'billing',
    step: '03',
    icon: 'creditCard',
    title: t('docsGuide.steps.billing.title'),
    description: t('docsGuide.steps.billing.description'),
    items: tm('docsGuide.steps.billing.items') as string[]
  },
  {
    id: 'token',
    step: '04',
    icon: 'key',
    title: t('docsGuide.steps.token.title'),
    description: t('docsGuide.steps.token.description'),
    items: tm('docsGuide.steps.token.items') as string[]
  },
  {
    id: 'environment',
    step: '05',
    icon: 'terminal',
    title: t('docsGuide.steps.environment.title'),
    description: t('docsGuide.steps.environment.description'),
    items: tm('docsGuide.steps.environment.items') as string[]
  },
  {
    id: 'first-call',
    step: '06',
    icon: 'play',
    title: t('docsGuide.steps.firstCall.title'),
    description: t('docsGuide.steps.firstCall.description'),
    items: tm('docsGuide.steps.firstCall.items') as string[]
  }
])

const endpoints = computed<EndpointItem[]>(() => [
  { name: 'GPT', path: `${apiBaseUrl.value}/v1/chat/completions` },
  { name: 'Claude', path: `${apiBaseUrl.value}/v1/messages` },
  { name: 'Responses', path: `${apiBaseUrl.value}/v1/responses` },
  { name: 'Gemini', path: `${apiBaseUrl.value}/v1beta/models/{model}:generateContent` },
  { name: 'Antigravity', path: `${apiBaseUrl.value}/antigravity/v1/messages` }
])

const curlExample = computed(() => `curl ${apiBaseUrl.value}/v1/chat/completions \\
  -H "Authorization: Bearer sk-..." \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-4.1-mini",
    "messages": [
      { "role": "user", "content": "Hello from SubAPIs" }
    ]
  }'`)

const cliEnvExample = computed(() => `export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="${apiBaseUrl.value}/v1"`)

const openAIEnvExample = computed(() => `OPENAI_API_KEY=sk-...
OPENAI_BASE_URL=${apiBaseUrl.value}/v1`)

const claudeEnvExample = computed(() => `ANTHROPIC_API_KEY=sk-...
ANTHROPIC_BASE_URL=${apiBaseUrl.value}`)

const geminiExample = computed(() => `GEMINI_API_KEY=sk-...
GEMINI_BASE_URL=${apiBaseUrl.value}/v1beta`)

const guideSections = computed<GuideSection[]>(() => [
  {
    id: 'models-and-groups',
    eyebrow: t('docsGuide.sections.models.eyebrow'),
    title: t('docsGuide.sections.models.title'),
    description: t('docsGuide.sections.models.description'),
    articles: [
      createArticle('docsGuide.sections.models.articles.market', {
        id: 'model-center',
        badge: 'M1',
        icon: 'grid'
      }),
      createArticle('docsGuide.sections.models.articles.groups', {
        id: 'token-groups',
        badge: 'M2',
        icon: 'users'
      }),
      createArticle('docsGuide.sections.models.articles.pricing', {
        id: 'pricing-and-quota',
        badge: 'M3',
        icon: 'calculator'
      }),
      createArticle('docsGuide.sections.models.articles.routing', {
        id: 'routing-policy',
        badge: 'M4',
        icon: 'swap'
      })
    ]
  },
  {
    id: 'cli-overview',
    eyebrow: t('docsGuide.sections.cli.eyebrow'),
    title: t('docsGuide.sections.cli.title'),
    description: t('docsGuide.sections.cli.description'),
    articles: [
      createArticle('docsGuide.sections.cli.articles.env', {
        id: 'cli-env',
        badge: 'CLI',
        icon: 'terminal',
        code: cliEnvExample.value
      }),
      createArticle('docsGuide.sections.cli.articles.claude', {
        id: 'claude-code',
        badge: 'CC',
        icon: 'chat',
        code: claudeEnvExample.value
      }),
      createArticle('docsGuide.sections.cli.articles.codex', {
        id: 'codex-cli',
        badge: 'CX',
        icon: 'cpu',
        code: openAIEnvExample.value
      }),
      createArticle('docsGuide.sections.cli.articles.gemini', {
        id: 'gemini-cli',
        badge: 'GM',
        icon: 'sparkles',
        code: geminiExample.value
      }),
      createArticle('docsGuide.sections.cli.articles.ccSwitch', {
        id: 'cc-switch',
        badge: 'SW',
        icon: 'sync'
      }),
      createArticle('docsGuide.sections.cli.articles.ccSwitchCli', {
        id: 'cc-switch-cli',
        badge: 'SC',
        icon: 'terminal'
      }),
      createArticle('docsGuide.sections.cli.articles.cache', {
        id: 'cache-tips',
        badge: 'CA',
        icon: 'database'
      })
    ]
  },
  {
    id: 'advanced-usage',
    eyebrow: t('docsGuide.sections.advanced.eyebrow'),
    title: t('docsGuide.sections.advanced.title'),
    description: t('docsGuide.sections.advanced.description'),
    articles: [
      createArticle('docsGuide.sections.advanced.articles.desktop', {
        id: 'claude-desktop',
        badge: 'A1',
        icon: 'cloud'
      }),
      createArticle('docsGuide.sections.advanced.articles.gateway', {
        id: 'gateway-migration',
        badge: 'A2',
        icon: 'link'
      }),
      createArticle('docsGuide.sections.advanced.articles.compatibleClaude', {
        id: 'compatible-claude-code',
        badge: 'A3',
        icon: 'swap'
      }),
      createArticle('docsGuide.sections.advanced.articles.image', {
        id: 'image-models',
        badge: 'IMG',
        icon: 'sparkles'
      }),
      createArticle('docsGuide.sections.advanced.articles.risk', {
        id: 'risk-control',
        badge: 'A4',
        icon: 'shield'
      }),
      createArticle('docsGuide.sections.advanced.articles.monitoring', {
        id: 'monitoring',
        badge: 'A5',
        icon: 'chartBar'
      })
    ]
  },
  {
    id: 'faq',
    eyebrow: t('docsGuide.sections.faq.eyebrow'),
    title: t('docsGuide.sections.faq.title'),
    description: t('docsGuide.sections.faq.description'),
    articles: [
      createArticle('docsGuide.sections.faq.articles.noModel', {
        id: 'faq-no-model',
        badge: 'Q1',
        icon: 'questionCircle'
      }),
      createArticle('docsGuide.sections.faq.articles.auth', {
        id: 'faq-auth',
        badge: 'Q2',
        icon: 'key'
      }),
      createArticle('docsGuide.sections.faq.articles.billing', {
        id: 'faq-billing',
        badge: 'Q3',
        icon: 'dollar'
      }),
      createArticle('docsGuide.sections.faq.articles.latency', {
        id: 'faq-latency',
        badge: 'Q4',
        icon: 'clock'
      })
    ]
  },
  {
    id: 'policies',
    eyebrow: t('docsGuide.sections.policies.eyebrow'),
    title: t('docsGuide.sections.policies.title'),
    description: t('docsGuide.sections.policies.description'),
    articles: [
      createArticle('docsGuide.sections.policies.articles.terms', {
        id: 'policy-terms',
        badge: 'P1',
        icon: 'document',
        links: [{ label: t('home.footer.terms'), to: '/legal/terms' }]
      }),
      createArticle('docsGuide.sections.policies.articles.aup', {
        id: 'policy-aup',
        badge: 'P2',
        icon: 'shield',
        links: [{ label: t('home.footer.usagePolicy'), to: '/legal/usage-policy' }]
      }),
      createArticle('docsGuide.sections.policies.articles.regions', {
        id: 'policy-regions',
        badge: 'P3',
        icon: 'globe',
        links: [{ label: t('home.footer.supportedRegions'), to: '/legal/supported-regions' }]
      }),
      createArticle('docsGuide.sections.policies.articles.serviceTerms', {
        id: 'policy-service-terms',
        badge: 'P4',
        icon: 'book',
        links: [{ label: t('home.footer.serviceSpecificTerms'), to: '/legal/service-specific-terms' }]
      })
    ]
  }
])

function createArticle(key: string, options: Omit<GuideArticle, 'title' | 'description' | 'items' | 'note'> & { note?: string }): GuideArticle {
  return {
    ...options,
    title: t(`${key}.title`),
    description: t(`${key}.description`),
    items: tm(`${key}.items`) as string[],
    note: options.note ?? t(`${key}.note`)
  }
}

function normalizeBaseUrl(url: string): string {
  return url.trim().replace(/\/+$/, '')
}

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

async function copyCurlExample() {
  await copyToClipboard(curlExample.value)
}

function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (savedTheme === 'dark' || (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>

<style scoped>
.docs-guide {
  isolation: isolate;
}

.docs-guide-bg {
  inset: 0;
  overflow: hidden;
  pointer-events: none;
  position: fixed;
}

.docs-guide-grid {
  background-image:
    linear-gradient(rgba(20, 184, 166, 0.045) 1px, transparent 1px),
    linear-gradient(90deg, rgba(20, 184, 166, 0.045) 1px, transparent 1px);
  background-size: 64px 64px;
  inset: 0;
  mask-image: linear-gradient(180deg, rgba(0, 0, 0, 0.7), transparent 70%);
  position: absolute;
}

.docs-guide-blob {
  border-radius: 9999px;
  filter: blur(58px);
  position: absolute;
}

.docs-guide-blob-a {
  background: rgba(20, 184, 166, 0.2);
  height: 24rem;
  left: -8rem;
  top: 8rem;
  width: 24rem;
}

.docs-guide-blob-b {
  background: rgba(6, 182, 212, 0.16);
  height: 28rem;
  right: -10rem;
  top: 18rem;
  width: 28rem;
}

.docs-guide-nav {
  padding: 0.35rem 0;
}

.docs-guide-brand {
  font-style: italic;
  letter-spacing: -0.045em;
  transform: skewX(-6deg);
}

.docs-guide-nav-link,
.docs-guide-icon-button,
.docs-guide-dashboard-link {
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

.docs-guide-nav-link {
  padding: 0 0.8rem;
}

.docs-guide-icon-button {
  width: 2.25rem;
}

.docs-guide-dashboard-link {
  background: #0f172a;
  color: #fff;
  padding: 0 1rem;
}

.docs-guide-nav-link:hover,
.docs-guide-icon-button:hover {
  background: rgba(255, 255, 255, 0.72);
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.1);
  color: #0f766e;
  transform: translateY(-1px);
}

.docs-guide-dashboard-link:hover {
  background: #1f2937;
  transform: translateY(-1px);
}

.docs-guide-eyebrow {
  align-items: center;
  color: #0f766e;
  display: inline-flex;
  font-size: 0.76rem;
  font-weight: 900;
  letter-spacing: 0.16em;
  text-transform: uppercase;
}

.docs-guide-terminal,
.docs-guide-step-card,
.docs-guide-example-card,
.docs-guide-bottom-cta,
.docs-guide-sidebar {
  border: 1px solid rgba(226, 232, 240, 0.86);
  background: rgba(255, 255, 255, 0.88);
  box-shadow: 0 18px 44px rgba(15, 23, 42, 0.08);
  backdrop-filter: blur(18px);
}

.docs-guide-terminal {
  border-radius: 1.45rem;
  overflow: hidden;
}

.docs-guide-terminal-head {
  align-items: center;
  background: rgba(15, 23, 42, 0.92);
  display: flex;
  gap: 0.45rem;
  padding: 0.9rem 1rem;
}

.docs-guide-terminal-head span {
  border-radius: 9999px;
  height: 0.7rem;
  width: 0.7rem;
}

.docs-guide-terminal-head span:nth-child(1) {
  background: #fb7185;
}

.docs-guide-terminal-head span:nth-child(2) {
  background: #fbbf24;
}

.docs-guide-terminal-head span:nth-child(3) {
  background: #34d399;
}

.docs-guide-terminal-label {
  color: #64748b;
  font-size: 0.75rem;
  font-weight: 900;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}

.docs-guide-code-line {
  background: #f1f5f9;
  border-radius: 0.9rem;
  color: #111827;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.92rem;
  margin-top: 0.6rem;
  overflow-x: auto;
  padding: 0.95rem 1rem;
  white-space: nowrap;
}

.docs-guide-code-line span {
  color: #0891b2;
  font-weight: 800;
}

.docs-guide-terminal pre,
.docs-guide-code-card pre {
  background: #07111f;
  border-radius: 1rem;
  color: #d1fae5;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.83rem;
  line-height: 1.7;
  margin-top: 0.6rem;
  overflow-x: auto;
  padding: 1rem;
}

.docs-guide-sidebar {
  border-radius: 1.25rem;
  max-height: calc(100vh - 7rem);
  overflow: auto;
  padding: 1rem 0.7rem;
  position: sticky;
  scrollbar-width: thin;
  top: 5.5rem;
}

.docs-guide-sidebar-group {
  color: #64748b;
  font-size: 0.7rem;
  font-weight: 900;
  letter-spacing: 0.14em;
  padding: 0 0.8rem;
  text-transform: uppercase;
}

.docs-guide-side-link {
  align-items: center;
  border-radius: 0.9rem;
  color: #475569;
  display: flex;
  gap: 0.65rem;
  font-size: 0.9rem;
  font-weight: 800;
  padding: 0.75rem 0.8rem;
  transition:
    background-color 180ms ease,
    color 180ms ease,
    transform 180ms ease;
}

.docs-guide-side-link span {
  color: #0f766e;
  font-size: 0.72rem;
  font-weight: 900;
}

.docs-guide-side-link:hover {
  background: rgba(20, 184, 166, 0.1);
  color: #0f766e;
  transform: translateX(2px);
}

.docs-guide-step-card {
  border-radius: 1.25rem;
  padding: 1.05rem;
}

.docs-guide-content-section {
  border: 1px solid rgba(226, 232, 240, 0.86);
  border-radius: 1.45rem;
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 0.9), rgba(240, 253, 250, 0.72)),
    rgba(255, 255, 255, 0.88);
  box-shadow: 0 18px 44px rgba(15, 23, 42, 0.07);
  padding: 1.15rem;
  backdrop-filter: blur(18px);
}

.docs-guide-section-head h2 {
  color: #0f172a;
  font-size: clamp(1.42rem, 1.9vw, 2rem);
  font-weight: 900;
  letter-spacing: -0.032em;
  line-height: 1.12;
  margin-top: 0.7rem;
}

.docs-guide-section-head p {
  color: #64748b;
  font-size: 0.92rem;
  line-height: 1.75;
  margin-top: 0.8rem;
  max-width: 46rem;
}

.docs-guide-article-card {
  border: 1px solid rgba(226, 232, 240, 0.86);
  border-radius: 1.35rem;
  background: rgba(255, 255, 255, 0.82);
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.045);
  padding: 1.2rem;
  transition:
    border-color 180ms ease,
    box-shadow 180ms ease,
    transform 180ms ease;
}

.docs-guide-article-card:hover {
  border-color: rgba(20, 184, 166, 0.36);
  box-shadow: 0 20px 48px rgba(15, 23, 42, 0.09);
  transform: translateY(-3px);
}

.docs-guide-article-icon {
  height: 2.75rem;
  width: 2.75rem;
}

.docs-guide-check-item-compact {
  background: rgba(248, 250, 252, 0.72);
  padding: 0.65rem 0.75rem;
}

.docs-guide-article-code {
  background: #07111f;
  border-radius: 1rem;
  color: #d1fae5;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.8rem;
  line-height: 1.7;
  margin-top: 1rem;
  overflow-x: auto;
  padding: 1rem;
}

.docs-guide-note {
  background: rgba(20, 184, 166, 0.08);
  border: 1px solid rgba(20, 184, 166, 0.18);
  border-radius: 0.95rem;
  color: #0f766e;
  font-size: 0.85rem;
  font-weight: 700;
  line-height: 1.7;
  margin-top: 1rem;
  padding: 0.8rem 0.9rem;
}

.docs-guide-pill-link {
  border: 1px solid rgba(20, 184, 166, 0.26);
  border-radius: 9999px;
  color: #0f766e;
  font-size: 0.78rem;
  font-weight: 900;
  padding: 0.48rem 0.75rem;
  transition:
    background-color 180ms ease,
    color 180ms ease,
    transform 180ms ease;
}

.docs-guide-pill-link:hover {
  background: rgba(20, 184, 166, 0.1);
  transform: translateY(-1px);
}

.docs-guide-step-icon {
  align-items: center;
  background: linear-gradient(135deg, #ccfbf1 0%, #ecfeff 100%);
  border-radius: 1rem;
  color: #0d9488;
  display: inline-flex;
  flex: 0 0 auto;
  height: 2.85rem;
  justify-content: center;
  width: 2.85rem;
}

.docs-guide-step-number {
  background: rgba(20, 184, 166, 0.1);
  border-radius: 9999px;
  color: #0f766e;
  font-size: 0.68rem;
  font-weight: 900;
  letter-spacing: 0.18em;
  padding: 0.28rem 0.62rem;
}

.docs-guide-check-item {
  align-items: flex-start;
  background: rgba(248, 250, 252, 0.86);
  border: 1px solid rgba(226, 232, 240, 0.86);
  border-radius: 0.95rem;
  color: #475569;
  display: flex;
  font-size: 0.875rem;
  gap: 0.55rem;
  line-height: 1.5;
  padding: 0.62rem 0.72rem;
}

.docs-guide-check-item svg {
  color: #0d9488;
  flex: 0 0 auto;
  margin-top: 0.1rem;
}

.docs-guide-example-card,
.docs-guide-bottom-cta {
  border-radius: 1.6rem;
  padding: 1.5rem;
}

.docs-guide-endpoint-row {
  align-items: center;
  background: rgba(248, 250, 252, 0.86);
  border: 1px solid rgba(226, 232, 240, 0.86);
  border-radius: 0.95rem;
  display: flex;
  gap: 0.75rem;
  justify-content: space-between;
  padding: 0.75rem 0.85rem;
}

.docs-guide-endpoint-row span {
  color: #0f172a;
  font-weight: 900;
}

.docs-guide-endpoint-row code {
  color: #0891b2;
  font-size: 0.78rem;
  overflow-wrap: anywhere;
  text-align: right;
}

.docs-guide-code-card {
  background: #fff;
  border: 1px solid rgba(226, 232, 240, 0.86);
  border-radius: 1.25rem;
  padding: 1rem;
}

.docs-guide-copy-button {
  align-items: center;
  border: 1px solid rgba(226, 232, 240, 0.9);
  border-radius: 9999px;
  color: #475569;
  display: inline-flex;
  font-size: 0.78rem;
  font-weight: 800;
  gap: 0.35rem;
  padding: 0.45rem 0.7rem;
  transition:
    border-color 180ms ease,
    color 180ms ease,
    transform 180ms ease;
}

.docs-guide-copy-button:hover {
  border-color: rgba(20, 184, 166, 0.45);
  color: #0f766e;
  transform: translateY(-1px);
}

.docs-guide-bottom-cta {
  align-items: center;
  display: flex;
  gap: 1.5rem;
  justify-content: space-between;
  margin-top: 5rem;
}

.docs-guide-dark {
  background:
    radial-gradient(circle at 18% 18%, rgba(20, 184, 166, 0.13), transparent 30%),
    radial-gradient(circle at 84% 24%, rgba(8, 145, 178, 0.14), transparent 28%),
    linear-gradient(135deg, #07111f 0%, #0a1724 48%, #081820 100%);
}

.docs-guide-dark .docs-guide-grid {
  background-image:
    linear-gradient(rgba(148, 163, 184, 0.045) 1px, transparent 1px),
    linear-gradient(90deg, rgba(148, 163, 184, 0.045) 1px, transparent 1px);
  opacity: 0.32;
}

.docs-guide-dark .docs-guide-nav-link,
.docs-guide-dark .docs-guide-icon-button {
  color: #cbd5e1;
}

.docs-guide-dark .docs-guide-nav-link:hover,
.docs-guide-dark .docs-guide-icon-button:hover {
  background: rgba(15, 23, 42, 0.66);
  box-shadow: 0 12px 28px rgba(0, 0, 0, 0.24);
  color: #5eead4;
}

.docs-guide-dark .docs-guide-dashboard-link {
  background: #e2e8f0;
  color: #0f172a;
}

.docs-guide-dark .docs-guide-eyebrow,
.docs-guide-dark .docs-guide-side-link span,
.docs-guide-dark .docs-guide-section-head h2 {
  color: #5eead4;
}

.docs-guide-dark .docs-guide-terminal,
.docs-guide-dark .docs-guide-step-card,
.docs-guide-dark .docs-guide-content-section,
.docs-guide-dark .docs-guide-article-card,
.docs-guide-dark .docs-guide-example-card,
.docs-guide-dark .docs-guide-bottom-cta,
.docs-guide-dark .docs-guide-sidebar,
.docs-guide-dark .docs-guide-code-card {
  border-color: rgba(148, 163, 184, 0.18);
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.82), rgba(8, 22, 32, 0.62)),
    rgba(15, 23, 42, 0.66);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.05),
    0 26px 70px rgba(0, 0, 0, 0.28);
}

.docs-guide-dark .docs-guide-code-line,
.docs-guide-dark .docs-guide-check-item,
.docs-guide-dark .docs-guide-endpoint-row {
  background: rgba(2, 6, 23, 0.42);
  border-color: rgba(148, 163, 184, 0.18);
  color: #e2e8f0;
}

.docs-guide-dark .docs-guide-terminal-label,
.docs-guide-dark .docs-guide-sidebar-group,
.docs-guide-dark .docs-guide-section-head p,
.docs-guide-dark .docs-guide-side-link,
.docs-guide-dark .docs-guide-check-item,
.docs-guide-dark .docs-guide-copy-button {
  color: #cbd5e1;
}

.docs-guide-dark .docs-guide-note {
  background: rgba(20, 184, 166, 0.14);
  border-color: rgba(94, 234, 212, 0.18);
  color: #99f6e4;
}

.docs-guide-dark .docs-guide-pill-link {
  border-color: rgba(94, 234, 212, 0.24);
  color: #5eead4;
}

.docs-guide-dark .docs-guide-step-icon {
  background: linear-gradient(135deg, rgba(45, 212, 191, 0.16), rgba(8, 145, 178, 0.12));
  border: 1px solid rgba(94, 234, 212, 0.16);
  color: #5eead4;
}

.docs-guide-dark .docs-guide-step-number {
  background: rgba(20, 184, 166, 0.16);
  color: #5eead4;
}

.docs-guide-dark .docs-guide-endpoint-row span {
  color: #f8fafc;
}

.docs-guide-dark .docs-guide-endpoint-row code,
.docs-guide-dark .docs-guide-code-line span {
  color: #67e8f9;
}

@media (max-width: 1023px) {
  .docs-guide-sidebar {
    position: relative;
    top: auto;
  }

  .docs-guide-bottom-cta {
    align-items: flex-start;
    flex-direction: column;
  }
}

@media (max-width: 640px) {
  .docs-guide-endpoint-row {
    align-items: flex-start;
    flex-direction: column;
  }

  .docs-guide-endpoint-row code {
    text-align: left;
  }
}
</style>
