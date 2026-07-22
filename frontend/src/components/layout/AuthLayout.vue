<template>
  <PublicLayout
    :show-chrome="false"
    :show-footer="false"
    content-class="public-ui-v2__content--flush public-auth-content"
  >
    <div v-if="isPublicUiV2" class="auth-v2">
      <div class="auth-v2-shell">
        <section class="auth-v2-form-pane">
          <div class="auth-v2-workspace">
            <header class="auth-v2-topbar">
              <RouterLink to="/home" class="auth-v2-brand">
                <span><img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" /></span>
                <strong>{{ siteName }}</strong>
              </RouterLink>
              <div class="auth-v2-controls">
                <LocaleSwitcher />
                <button
                  type="button"
                  :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
                  :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
                  @click="toggleTheme"
                >
                  <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
                </button>
              </div>
            </header>

            <div class="auth-v2-form-wrap">
              <div class="auth-layout-card">
                <slot />
              </div>
              <div class="auth-layout-footer">
                <slot name="footer" />
              </div>
            </div>
          </div>

          <p class="auth-v2-copyright">
            &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
          </p>
        </section>

        <aside class="auth-v2-context">
          <div class="auth-v2-context-inner">
            <span class="auth-v2-badge"><i aria-hidden="true"></i> API Gateway</span>
            <h2>{{ t('home.hero.titleLine1') }}</h2>
            <p>{{ t('home.hero.description') }}</p>

            <ul class="auth-v2-benefits">
              <li v-for="feature in contextFeatures" :key="feature.title">
                <span aria-hidden="true"><Icon name="check" size="sm" /></span>
                <div>
                  <strong>{{ feature.title }}</strong>
                  <p>{{ feature.description }}</p>
                </div>
              </li>
            </ul>

            <div class="auth-v2-api">
              <span>{{ t('home.hero.baseUrlLabel') }}</span>
              <code>{{ apiBaseUrl }}</code>
            </div>
          </div>
          <p>&copy; {{ currentYear }} {{ siteName }}</p>
        </aside>
      </div>
    </div>

    <div v-else class="relative flex min-h-screen items-center justify-center overflow-hidden p-4">
      <div
        class="absolute inset-0 bg-gradient-to-br from-gray-50 via-primary-50/30 to-gray-100 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950"
      ></div>
      <div class="pointer-events-none absolute inset-0 overflow-hidden">
        <div class="absolute -right-40 -top-40 h-80 w-80 rounded-full bg-primary-400/20 blur-3xl"></div>
        <div class="absolute -bottom-40 -left-40 h-80 w-80 rounded-full bg-primary-500/15 blur-3xl"></div>
        <div class="absolute left-1/2 top-1/2 h-96 w-96 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary-300/10 blur-3xl"></div>
        <div
          class="absolute inset-0 bg-[linear-gradient(rgba(20,184,166,0.03)_1px,transparent_1px),linear-gradient(90deg,rgba(20,184,166,0.03)_1px,transparent_1px)] bg-[size:64px_64px]"
        ></div>
      </div>

      <div class="relative z-10 w-full max-w-md">
        <div class="mb-8 text-center">
          <div class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl shadow-lg shadow-primary-500/30">
            <img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" class="h-full w-full object-contain" />
          </div>
          <h1 class="text-gradient mb-2 text-3xl font-bold">{{ siteName }}</h1>
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ siteSubtitle }}</p>
        </div>
        <div class="card-glass rounded-2xl p-8 shadow-glass"><slot /></div>
        <div class="mt-6 text-center text-sm"><slot name="footer" /></div>
        <div class="mt-8 text-center text-xs text-gray-400 dark:text-dark-500">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </div>
      </div>
    </div>
  </PublicLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { normalizeSiteName } from '@/utils/siteBrand'
import PublicLayout from '@/components/public/PublicLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import { usePublicUiVersion } from '@/composables/usePublicUiVersion'

const appStore = useAppStore()
const { t } = useI18n()
const { isPublicUiV2 } = usePublicUiVersion()
const isDark = ref(document.documentElement.classList.contains('dark'))

const siteName = computed(() => normalizeSiteName(appStore.siteName))
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || t('home.defaultSubtitle'))
const currentYear = computed(() => new Date().getFullYear())
const apiBaseUrl = computed(() => {
  const configured = appStore.cachedPublicSettings?.api_base_url || appStore.apiBaseUrl
  return String(configured || window.location.origin).trim().replace(/\/+$/, '')
})
const contextFeatures = computed(() => [
  {
    title: t('home.hero.features.routing.title'),
    description: t('home.hero.features.routing.description')
  },
  {
    title: t('home.hero.features.observability.title'),
    description: t('home.hero.features.observability.description')
  },
  {
    title: t('home.hero.features.governance.title'),
    description: t('home.hero.features.governance.description')
  }
])

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.text-gradient {
  @apply bg-gradient-to-r from-primary-600 to-primary-500 bg-clip-text text-transparent;
}

.auth-v2,
.auth-v2-shell {
  min-height: 100dvh;
}

.auth-v2-shell {
  display: grid;
  grid-template-columns: minmax(0, 56%) minmax(480px, 44%);
  animation: auth-v2-enter 220ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.auth-v2-form-pane {
  position: relative;
  display: grid;
  min-width: 0;
  grid-template-rows: 1fr auto;
  padding: 0 48px 24px;
  background: var(--ui2-surface);
}

.auth-v2-workspace {
  width: min(100%, 440px);
  margin: 0 auto;
  padding: clamp(72px, 12vh, 112px) 0 48px;
}

.auth-v2-topbar,
.auth-v2-brand,
.auth-v2-controls {
  display: flex;
  align-items: center;
}

.auth-v2-topbar {
  justify-content: space-between;
  gap: 20px;
}

.auth-v2-brand {
  min-width: 0;
  gap: 10px;
  color: var(--ui2-text);
}

.auth-v2-brand > span {
  width: 34px;
  height: 34px;
  flex: 0 0 34px;
  overflow: hidden;
  background: var(--ui2-surface-muted);
  border: 1px solid var(--ui2-line);
  border-radius: 8px;
}

.auth-v2-brand img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.auth-v2-brand strong {
  overflow: hidden;
  font-size: 15px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.auth-v2-controls {
  gap: 4px;
}

.auth-v2-controls > button {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 8px;
  color: var(--ui2-text-secondary);
  transition: background-color 140ms ease-out, color 140ms ease-out, transform 90ms ease-out;
}

.auth-v2-controls > button:hover {
  background: var(--ui2-surface-muted);
  color: var(--ui2-text);
}

.auth-v2-controls > button:active {
  transform: scale(0.94);
}

.auth-v2-form-wrap {
  width: 100%;
  padding-top: 48px;
}

:global(.auth-v2 .auth-layout-card > .space-y-6 > .text-center) {
  text-align: left;
}

:global(.auth-v2 .auth-layout-card h2) {
  color: var(--ui2-text) !important;
  font-size: 30px;
  font-weight: 700;
  line-height: 1.18;
}

:global(.auth-v2 .auth-layout-card .input-label) {
  margin-bottom: 7px;
  color: var(--ui2-text);
  font-size: 13px;
  font-weight: 650;
}

:global(.auth-v2 .auth-layout-card .pointer-events-none.absolute) {
  display: none;
}

:global(.auth-v2 .auth-layout-card .input) {
  min-height: 46px;
  padding-left: 13px !important;
  background: var(--ui2-surface) !important;
  border: 1px solid var(--ui2-line-strong) !important;
  border-radius: 8px !important;
  box-shadow: none !important;
  color: var(--ui2-text) !important;
  transition: background-color 140ms ease-out, border-color 140ms ease-out, box-shadow 140ms ease-out;
}

:global(.auth-v2 .auth-layout-card .input:focus) {
  border-color: var(--ui2-accent) !important;
  box-shadow: 0 0 0 3px var(--ui2-accent-soft) !important;
  outline: none;
}

:global(.auth-v2 .auth-layout-card .input::placeholder) {
  color: var(--ui2-text-tertiary) !important;
}

:global(.auth-v2 .auth-layout-card :where(button, select)) {
  border-radius: 8px !important;
}

:global(.auth-v2 .auth-layout-card .btn) {
  min-height: 46px;
}

:global(.auth-v2 .auth-layout-card .btn-primary) {
  background: var(--ui2-text) !important;
  border-color: var(--ui2-text) !important;
  box-shadow: none !important;
  color: var(--ui2-surface) !important;
}

:global(.auth-v2 .auth-layout-card .btn-primary:hover) {
  background: color-mix(in srgb, var(--ui2-text), var(--ui2-surface) 12%) !important;
}

:global(.auth-v2 .auth-layout-card .btn:active) {
  transform: scale(0.98);
  transition: transform 90ms ease-out;
}

.auth-layout-footer {
  margin-top: 26px;
  color: var(--ui2-text-secondary);
  font-size: 13px;
  text-align: center;
}

.auth-v2-copyright {
  width: min(100%, 440px);
  margin: 0 auto;
  color: var(--ui2-text-tertiary);
  font-size: 11px;
}

.auth-v2-context {
  display: grid;
  min-width: 0;
  grid-column: 1;
  grid-row: 1;
  grid-template-rows: 1fr auto;
  padding: 64px clamp(48px, 6vw, 104px) 28px;
  background: #0c0d0f;
  color: #f5f5f7;
}

.auth-v2-context-inner {
  width: min(100%, 600px);
  margin: auto;
}

.auth-v2-form-pane {
  grid-column: 2;
  grid-row: 1;
}

.auth-v2-badge {
  display: inline-flex;
  min-height: 28px;
  align-items: center;
  gap: 8px;
  padding: 0 11px;
  background: rgba(72, 201, 130, 0.1);
  border: 1px solid rgba(72, 201, 130, 0.16);
  border-radius: 999px;
  color: #74d99e;
  font-size: 11px;
  font-weight: 650;
}

.auth-v2-badge i {
  width: 6px;
  height: 6px;
  background: #48c982;
  border-radius: 50%;
  box-shadow: 0 0 0 3px rgba(72, 201, 130, 0.12);
}

.auth-v2-context h2 {
  max-width: 560px;
  margin: 30px 0 0;
  font-size: clamp(38px, 3.5vw, 56px);
  font-weight: 680;
  line-height: 1.02;
}

.auth-v2-context-inner > p {
  max-width: 580px;
  margin: 18px 0 0;
  color: #a5a5aa;
  font-size: 14px;
  line-height: 1.75;
}

.auth-v2-benefits {
  display: grid;
  gap: 20px;
  margin: 34px 0 0;
  padding: 0;
  list-style: none;
}

.auth-v2-benefits li {
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr);
  align-items: start;
  gap: 12px;
}

.auth-v2-benefits li > span {
  display: grid;
  width: 22px;
  height: 22px;
  place-items: center;
  margin-top: 1px;
  background: rgba(72, 201, 130, 0.12);
  border-radius: 50%;
  color: #74d99e;
}

.auth-v2-benefits strong {
  color: #f5f5f7;
  font-size: 13px;
  font-weight: 650;
}

.auth-v2-benefits p {
  margin: 0;
  color: #77777e;
  font-size: 12px;
  line-height: 1.55;
}

.auth-v2-api {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 20px;
  margin-top: 36px;
  padding: 15px 0;
  border-top: 1px solid rgba(255, 255, 255, 0.1);
  border-bottom: 1px solid rgba(255, 255, 255, 0.1);
}

.auth-v2-api span {
  color: #77777e;
  font-size: 11px;
}

.auth-v2-api code {
  display: block;
  max-width: 300px;
  overflow: hidden;
  color: #f5f5f7;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.auth-v2-context > p {
  margin: 28px 0 0;
  color: #55555b;
  font-size: 10px;
}

@keyframes auth-v2-enter {
  from { opacity: 0; transform: translateY(8px); }
  to { opacity: 1; transform: translateY(0); }
}

@media (max-width: 900px) {
  .auth-v2-shell {
    display: block;
  }

  .auth-v2-form-pane {
    min-height: 100dvh;
    padding: 20px 20px 24px;
  }

  .auth-v2-context {
    display: none;
  }

  .auth-v2-workspace {
    width: min(100%, 460px);
    padding: 0 0 40px;
  }

  .auth-v2-form-wrap {
    padding-top: 44px;
  }
}

@media (max-width: 480px) {
  .auth-v2-form-pane {
    padding-inline: 16px;
  }

  .auth-v2-form-wrap {
    padding-top: 36px;
  }

  :global(.auth-v2 .auth-layout-card h2) {
    font-size: 26px;
  }

  .auth-v2-copyright {
    text-align: center;
  }
}

@media (prefers-reduced-motion: reduce) {
  .auth-v2-shell {
    animation: none;
  }
}
</style>
