<template>
  <header class="public-header">
    <nav class="public-header__bar" :aria-label="t('nav.mainNavigation')">
      <RouterLink class="public-header__brand" to="/home" @click="closeMenu">
        <span class="public-header__logo">
          <img :src="siteLogo" :alt="t('common.logoAlt')" />
        </span>
        <span class="public-header__site-name">{{ siteName }}</span>
      </RouterLink>

      <div class="public-header__desktop-nav">
        <RouterLink
          v-for="item in visibleNavigation"
          :key="item.to"
          :to="item.to"
          class="public-header__nav-link"
        >
          {{ item.label }}
        </RouterLink>
      </div>

      <div class="public-header__actions">
        <slot name="actions" />
        <LocaleSwitcher />
        <button
          type="button"
          class="public-header__icon-button"
          :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          @click="toggleTheme"
        >
          <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
        </button>

        <RouterLink
          v-if="authStore.isAuthenticated"
          class="public-header__primary-action public-header__desktop-action"
          :to="dashboardPath"
        >
          {{ t('home.dashboard') }}
          <Icon name="arrowRight" size="sm" />
        </RouterLink>
        <template v-else>
          <RouterLink class="public-header__login public-header__desktop-action" to="/login">
            {{ t('home.login') }}
          </RouterLink>
          <RouterLink
            v-if="registrationEnabled"
            class="public-header__primary-action public-header__desktop-action"
            to="/register"
          >
            {{ t('auth.signUp') }}
          </RouterLink>
        </template>

        <button
          type="button"
          class="public-header__icon-button public-header__menu-button"
          :aria-expanded="menuOpen"
          :aria-label="t('nav.mainNavigation')"
          aria-controls="public-mobile-menu"
          @click="menuOpen = !menuOpen"
        >
          <Icon :name="menuOpen ? 'x' : 'menu'" size="md" />
        </button>
      </div>
    </nav>

    <Transition name="public-menu">
      <div v-if="menuOpen" id="public-mobile-menu" class="public-header__mobile-menu">
        <RouterLink
          v-for="item in visibleNavigation"
          :key="item.to"
          :to="item.to"
          class="public-header__mobile-link"
          @click="closeMenu"
        >
          <Icon :name="item.icon" size="sm" />
          <span>{{ item.label }}</span>
          <Icon name="chevronRight" size="sm" />
        </RouterLink>

        <div class="public-header__mobile-actions">
          <RouterLink
            v-if="authStore.isAuthenticated"
            class="public-header__primary-action"
            :to="dashboardPath"
            @click="closeMenu"
          >
            {{ t('home.dashboard') }}
            <Icon name="arrowRight" size="sm" />
          </RouterLink>
          <template v-else>
            <RouterLink class="public-header__login" to="/login" @click="closeMenu">
              {{ t('home.login') }}
            </RouterLink>
            <RouterLink
              v-if="registrationEnabled"
              class="public-header__primary-action"
              to="/register"
              @click="closeMenu"
            >
              {{ t('auth.signUp') }}
            </RouterLink>
          </template>
        </div>
      </div>
    </Transition>
  </header>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore, useAuthStore } from '@/stores'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { normalizeSiteName } from '@/utils/siteBrand'
import { sanitizeUrl } from '@/utils/url'

type IconName = InstanceType<typeof Icon>['$props']['name']

interface PublicNavigationItem {
  to: string
  label: string
  icon: IconName
  enabled?: boolean
}

const { t } = useI18n()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const menuOpen = ref(false)
const isDark = ref(document.documentElement.classList.contains('dark'))

const siteName = computed(() => normalizeSiteName(appStore.siteName))
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '/logo.png', {
  allowRelative: true,
  allowDataUrl: true,
}) || '/logo.png')
const registrationEnabled = computed(() => appStore.cachedPublicSettings?.registration_enabled !== false)
const dashboardPath = computed(() => authStore.isAdmin ? '/admin/dashboard' : '/dashboard')
const visibleNavigation = computed<PublicNavigationItem[]>(() => {
  const items: PublicNavigationItem[] = [
    {
      to: '/models',
      label: t('modelMarket.navLabel'),
      icon: 'cube',
      enabled: isFeatureFlagEnabled(FeatureFlags.publicModelMarket),
    },
    { to: '/monitor', label: t('nav.modelStatus'), icon: 'chart' },
    { to: '/status', label: t('nav.serviceStatus'), icon: 'server' },
    { to: '/key-usage', label: t('keyUsage.title'), icon: 'key' },
    { to: '/docs', label: t('nav.docs'), icon: 'book' },
  ]
  return items.filter((item) => item.enabled !== false)
})

function closeMenu() {
  menuOpen.value = false
}

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === 'Escape') closeMenu()
}

watch(() => route.fullPath, closeMenu)
onMounted(() => document.addEventListener('keydown', handleEscape))
onBeforeUnmount(() => document.removeEventListener('keydown', handleEscape))
</script>

<style scoped>
.public-header {
  position: sticky;
  top: 0;
  z-index: 40;
  width: 100%;
  border-bottom: 1px solid var(--ui2-line, rgba(15, 23, 42, 0.09));
  background: color-mix(in srgb, var(--ui2-page, #f5f6f8), transparent 12%);
  backdrop-filter: blur(18px) saturate(145%);
}

.public-header__bar {
  display: flex;
  min-height: 3.75rem;
  max-width: 80rem;
  margin: 0 auto;
  padding: 0.5rem 1.25rem;
  align-items: center;
  gap: 1rem;
}

.public-header__brand,
.public-header__actions,
.public-header__desktop-nav,
.public-header__primary-action,
.public-header__mobile-link,
.public-header__mobile-actions {
  display: flex;
  align-items: center;
}

.public-header__brand {
  min-width: 0;
  color: var(--ui2-text, #111827);
  gap: 0.625rem;
  text-decoration: none;
}

.public-header__logo {
  width: 2rem;
  height: 2rem;
  flex: 0 0 2rem;
  overflow: hidden;
  border: 1px solid var(--ui2-line, rgba(15, 23, 42, 0.09));
  border-radius: 8px;
  background: var(--ui2-surface, #fff);
}

.public-header__logo img { width: 100%; height: 100%; object-fit: contain; }
.public-header__site-name { overflow: hidden; font-size: 0.9375rem; font-weight: 700; text-overflow: ellipsis; white-space: nowrap; }

.public-header__desktop-nav { margin-left: auto; gap: 0.125rem; }
.public-header__nav-link,
.public-header__login {
  border-radius: 7px;
  color: var(--ui2-text-secondary, #4b5563);
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 2.25rem;
  padding: 0 0.75rem;
  text-decoration: none;
}

.public-header__nav-link:hover,
.public-header__nav-link.router-link-active,
.public-header__login:hover { background: var(--ui2-surface-hover, rgba(15, 23, 42, 0.055)); color: var(--ui2-text, #111827); }

.public-header__actions { gap: 0.25rem; }
.public-header__icon-button {
  display: inline-grid;
  width: 2.25rem;
  height: 2.25rem;
  place-items: center;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--ui2-text-secondary, #4b5563);
  cursor: pointer;
  transition: background-color 160ms ease, color 160ms ease, transform 100ms ease-out;
}
.public-header__icon-button:hover { background: var(--ui2-surface-hover, rgba(15, 23, 42, 0.055)); color: var(--ui2-text, #111827); }
.public-header__icon-button:active,
.public-header__primary-action:active,
.public-header__login:active { transform: scale(0.97); }
.public-header__icon-button:focus-visible,
.public-header__nav-link:focus-visible,
.public-header__primary-action:focus-visible,
.public-header__login:focus-visible,
.public-header__mobile-link:focus-visible { outline: 2px solid var(--ui2-accent, #2563eb); outline-offset: 2px; }

.public-header__primary-action {
  min-height: 2.25rem;
  justify-content: center;
  gap: 0.375rem;
  border-radius: 7px;
  background: var(--ui2-accent, #2563eb);
  color: #fff;
  font-size: 0.8125rem;
  font-weight: 650;
  padding: 0 0.875rem;
  text-decoration: none;
  transition: background-color 160ms ease, transform 100ms ease-out;
}
.public-header__primary-action:hover { background: color-mix(in srgb, var(--ui2-accent, #2563eb), #000 9%); }
.public-header__menu-button { display: none; }
.public-header__mobile-menu { display: none; }

@media (max-width: 71rem) {
  .public-header__desktop-nav { display: none; }
  .public-header__actions { margin-left: auto; }
  .public-header__menu-button { display: inline-grid; }
  .public-header__mobile-menu {
    display: grid;
    max-width: 80rem;
    margin: 0 auto;
    padding: 0.375rem 1.25rem 1rem;
    gap: 0.125rem;
  }
  .public-header__mobile-link {
    min-height: 2.75rem;
    gap: 0.75rem;
    border-radius: 7px;
    color: var(--ui2-text-secondary, #4b5563);
    font-size: 0.875rem;
    font-weight: 600;
    padding: 0 0.75rem;
    text-decoration: none;
  }
  .public-header__mobile-link > :last-child { margin-left: auto; color: var(--ui2-text-tertiary, #9ca3af); }
  .public-header__mobile-link:hover,
  .public-header__mobile-link.router-link-active { background: var(--ui2-surface-hover, rgba(15, 23, 42, 0.055)); color: var(--ui2-text, #111827); }
  .public-header__mobile-actions { gap: 0.5rem; padding: 0.75rem 0.25rem 0; }
}

@media (max-width: 40rem) {
  .public-header__bar { padding-inline: 1rem; }
  .public-header__site-name { max-width: 8rem; }
  .public-header__desktop-action { display: none; }
  .public-header__mobile-menu { padding-inline: 1rem; }
  .public-header__mobile-actions > * { flex: 1; }
}

.public-menu-enter-active,
.public-menu-leave-active { transition: opacity 180ms ease, transform 180ms ease; transform-origin: top center; }
.public-menu-enter-from,
.public-menu-leave-to { opacity: 0; transform: translateY(-0.375rem) scale(0.985); }

@media (prefers-reduced-motion: reduce) {
  .public-menu-enter-active,
  .public-menu-leave-active { transition: opacity 120ms ease; }
  .public-menu-enter-from,
  .public-menu-leave-to { transform: none; }
}

@media (prefers-reduced-transparency: reduce) {
  .public-header { background: var(--ui2-page, #f5f6f8); backdrop-filter: none; }
}

@media (prefers-contrast: more) {
  .public-header { border-bottom-color: var(--ui2-text, #111827); background: var(--ui2-page, #f5f6f8); }
}
</style>
