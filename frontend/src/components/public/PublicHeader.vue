<template>
  <header class="public-header">
    <nav class="public-header__bar" :aria-label="t('nav.mainNavigation')">
      <PublicBrand
        :site-logo="siteLogo"
        :site-name="siteName"
        collapse-on-mobile
        @click="closeMenu"
      />

      <div class="public-header__desktop-nav">
        <RouterLink
          v-for="item in visibleNavigation"
          :key="item.to"
          :to="item.to"
          class="public-header__nav-link"
          :class="{ 'public-header__nav-link--wide': item.wideOnly }"
        >
          {{ item.label }}
        </RouterLink>
      </div>

      <div class="public-header__actions">
        <slot name="actions" />
        <button
          type="button"
          class="public-header__icon-button public-header__announcement"
          :class="{ 'is-active': announcementBadgeCount > 0 }"
          :title="t('home.announcements.modalTitle')"
          :aria-label="t('home.announcements.modalTitle')"
          @click="openAnnouncementDialog"
        >
          <Icon name="bell" size="md" />
          <span v-if="announcementBadgeCount > 0" class="public-header__badge">
            {{ announcementBadgeCount > 9 ? '9+' : announcementBadgeCount }}
          </span>
        </button>
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
          class="public-header__dashboard public-header__desktop-action"
          :to="dashboardPath"
        >
          <span>{{ userInitial }}</span>
          {{ t('home.dashboard') }}
          <Icon name="externalLink" size="xs" />
        </RouterLink>
        <div v-else class="public-header__auth-actions public-header__desktop-action">
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
        </div>

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
          v-for="item in mobileNavigation"
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

    <PublicAnnouncementDialog
      :open="announcementDialogOpen"
      :loading="announcementsLoading"
      :items="activeAnnouncements"
      :tab="announcementTab"
      :is-authenticated="authStore.isAuthenticated"
      @close="closeAnnouncementDialog"
      @close-today="closeAnnouncementToday"
      @update:tab="announcementTab = $event"
    />
  </header>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { announcementsAPI } from '@/api'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import PublicAnnouncementDialog from '@/components/public/PublicAnnouncementDialog.vue'
import PublicBrand from '@/components/public/PublicBrand.vue'
import { useAnnouncementStore, useAppStore, useAuthStore } from '@/stores'
import type { UserAnnouncement } from '@/types'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { normalizeSiteName } from '@/utils/siteBrand'
import { sanitizeUrl } from '@/utils/url'

type IconName = InstanceType<typeof Icon>['$props']['name']

interface PublicNavigationItem {
  to: string
  label: string
  icon: IconName
  enabled?: boolean
  wideOnly?: boolean
}

const { t } = useI18n()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const announcementStore = useAnnouncementStore()
const menuOpen = ref(false)
const isDark = ref(document.documentElement.classList.contains('dark'))
const announcementDialogOpen = ref(false)
const announcementTab = ref<'notifications' | 'system'>('system')
const publicAnnouncements = ref<UserAnnouncement[]>([])
const publicAnnouncementsLoading = ref(false)
const publicAnnouncementsLoaded = ref(false)

const siteName = computed(() => normalizeSiteName(appStore.siteName))
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '/logo.png', {
  allowRelative: true,
  allowDataUrl: true,
}) || '/logo.png')
const registrationEnabled = computed(() => appStore.cachedPublicSettings?.registration_enabled !== false)
const dashboardPath = computed(() => authStore.isAdmin ? '/admin/dashboard' : '/dashboard')
const userInitial = computed(() => authStore.user?.email?.charAt(0).toUpperCase() || 'A')
const visibleNavigation = computed<PublicNavigationItem[]>(() => {
  const items: PublicNavigationItem[] = [
    {
      to: '/models',
      label: t('modelMarket.navLabel'),
      icon: 'cube',
      enabled: isFeatureFlagEnabled(FeatureFlags.publicModelMarket),
    },
    { to: '/monitor', label: t('nav.modelStatus'), icon: 'chart', wideOnly: true },
    { to: '/docs', label: t('nav.docs'), icon: 'book', wideOnly: true },
  ]
  return items.filter((item) => item.enabled !== false)
})
const secondaryNavigation = computed<PublicNavigationItem[]>(() => [
  { to: '/key-usage', label: t('keyUsage.title'), icon: 'key' },
])
const mobileNavigation = computed(() => [...visibleNavigation.value, ...secondaryNavigation.value])
const headerAnnouncements = computed(() => (
  authStore.isAuthenticated ? announcementStore.announcements : publicAnnouncements.value
).slice(0, 20))
const notificationAnnouncements = computed(() => (
  headerAnnouncements.value.filter((item) => item.notify_mode === 'popup')
))
const systemAnnouncements = computed(() => (
  headerAnnouncements.value.filter((item) => item.notify_mode !== 'popup')
))
const announcementBadgeCount = computed(() => (
  authStore.isAuthenticated
    ? headerAnnouncements.value.filter((item) => !item.read_at).length
    : publicAnnouncements.value.length
))
const announcementsLoading = computed(() => (
  authStore.isAuthenticated ? announcementStore.loading : publicAnnouncementsLoading.value
))
const activeAnnouncements = computed(() => (
  announcementTab.value === 'notifications'
    ? notificationAnnouncements.value
    : systemAnnouncements.value
))

function closeMenu() {
  menuOpen.value = false
}

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

async function openAnnouncementDialog() {
  announcementDialogOpen.value = true
  if (authStore.isAuthenticated) {
    await announcementStore.fetchAnnouncements(true)
    if (notificationAnnouncements.value.some((item) => !item.read_at)) {
      announcementTab.value = 'notifications'
    }
    return
  }

  if (!publicAnnouncementsLoaded.value && !publicAnnouncementsLoading.value) {
    publicAnnouncementsLoading.value = true
    try {
      publicAnnouncements.value = (await announcementsAPI.listPublic()).slice(0, 20)
      if (notificationAnnouncements.value.length > 0) announcementTab.value = 'notifications'
    } catch {
      publicAnnouncements.value = []
    } finally {
      publicAnnouncementsLoaded.value = true
      publicAnnouncementsLoading.value = false
    }
  }
}

function closeAnnouncementDialog() {
  announcementDialogOpen.value = false
}

function closeAnnouncementToday() {
  localStorage.setItem('home-announcement-closed-date', new Date().toISOString().slice(0, 10))
  closeAnnouncementDialog()
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
  border-bottom: 1px solid rgba(40, 74, 76, 0.1);
  background: rgba(244, 249, 249, 0.84);
  backdrop-filter: blur(18px) saturate(145%);
}

.public-header__bar {
  display: flex;
  min-height: 60px;
  max-width: 1180px;
  margin: 0 auto;
  padding: 0 24px;
  align-items: center;
  gap: 16px;
}

.public-header__actions,
.public-header__desktop-nav,
.public-header__primary-action,
.public-header__dashboard,
.public-header__auth-actions,
.public-header__mobile-link,
.public-header__mobile-actions {
  display: flex;
  align-items: center;
}

.public-header__desktop-nav { margin-left: auto; gap: 12px; }
.public-header__nav-link,
.public-header__login {
  min-height: 36px;
  border-radius: 999px;
  color: #475569;
  font-size: 13px;
  font-weight: 800;
  line-height: 36px;
  padding: 0 13px;
  text-decoration: none;
  transition: background-color 180ms ease-out, box-shadow 180ms ease-out, color 180ms ease-out, transform 100ms ease-out;
}

.public-header__nav-link:hover,
.public-header__nav-link.router-link-active,
.public-header__login:hover {
  background: rgba(255, 255, 255, 0.72);
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.1);
  color: #0f766e;
  transform: translateY(-1px);
}

.public-header__actions { gap: 4px; }
.public-header__icon-button {
  display: inline-grid;
  width: 2.25rem;
  height: 2.25rem;
  place-items: center;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: #475569;
  cursor: pointer;
  transition: background-color 160ms ease, color 160ms ease, transform 100ms ease-out;
}
.public-header__icon-button:hover,
.public-header__icon-button.is-active {
  background: rgba(255, 255, 255, 0.72);
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.1);
  color: #0f766e;
}
.public-header__icon-button:active,
.public-header__primary-action:active,
.public-header__login:active { transform: scale(0.97); }
.public-header__icon-button:focus-visible,
.public-header__nav-link:focus-visible,
.public-header__primary-action:focus-visible,
.public-header__dashboard:focus-visible,
.public-header__login:focus-visible,
.public-header__mobile-link:focus-visible { outline: 2px solid var(--ui2-accent, #2563eb); outline-offset: 2px; }

.public-header__announcement {
  position: relative;
}

.public-header__badge {
  position: absolute;
  top: -1px;
  right: -2px;
  display: grid;
  min-width: 16px;
  height: 16px;
  place-items: center;
  padding: 0 3px;
  background: #e5484d;
  border: 2px solid rgba(244, 249, 249, 0.96);
  border-radius: 999px;
  color: #fff;
  font-size: 9px;
  font-weight: 800;
  line-height: 1;
}

.public-header__auth-actions {
  gap: 2px;
  padding: 4px;
  background: rgba(241, 245, 249, 0.9);
  border-radius: 999px;
}

.public-header__auth-actions .public-header__login {
  min-height: 32px;
  line-height: 32px;
  padding-inline: 14px;
  color: #334155;
  font-weight: 700;
}

.public-header__primary-action {
  min-height: 32px;
  justify-content: center;
  gap: 0.375rem;
  border-radius: 999px;
  background: linear-gradient(135deg, #14b8a6, #0891b2);
  box-shadow: 0 10px 22px rgba(13, 148, 136, 0.22);
  color: #fff;
  font-size: 0.8125rem;
  font-weight: 700;
  padding: 0 14px;
  text-decoration: none;
  transition: background-color 160ms ease, transform 100ms ease-out;
}
.public-header__primary-action:hover { background: linear-gradient(135deg, #0d9f90, #087f9b); }

.public-header__dashboard {
  min-height: 32px;
  gap: 6px;
  padding: 4px 10px 4px 4px;
  background: #111827;
  border-radius: 999px;
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  text-decoration: none;
  transition: background-color 160ms ease-out, transform 100ms ease-out;
}

.public-header__dashboard > span {
  display: grid;
  width: 22px;
  height: 22px;
  place-items: center;
  background: linear-gradient(135deg, #2dd4bf, #0891b2);
  border-radius: 50%;
  font-size: 10px;
  font-weight: 700;
}

.public-header__dashboard:hover { background: #1f2937; }
.public-header__dashboard:active { transform: scale(0.97); }
.public-header__menu-button { display: none; }
.public-header__mobile-menu { display: none; }

@media (max-width: 1023px) {
  .public-header__nav-link--wide { display: none; }
  .public-header__menu-button { display: inline-grid; }
  .public-header__mobile-menu {
    display: grid;
    max-width: 1180px;
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

@media (max-width: 760px) {
  .public-header__desktop-nav { display: none; }
  .public-header__actions { margin-left: auto; }
}

@media (max-width: 640px) {
  .public-header__bar { min-height: 56px; padding-inline: 10px; gap: 8px; }
  .public-header__desktop-action { display: none; }
  .public-header__mobile-menu { padding-inline: 1rem; }
  .public-header__mobile-actions > * { flex: 1; }
}

:global(.dark) .public-header {
  background: rgba(17, 17, 19, 0.82);
  border-color: rgba(255, 255, 255, 0.09);
}

:global(.dark) .public-header__nav-link,
:global(.dark) .public-header__icon-button,
:global(.dark) .public-header__login {
  color: #b1b1b7;
}

:global(.dark) .public-header__nav-link:hover,
:global(.dark) .public-header__nav-link.router-link-active,
:global(.dark) .public-header__icon-button:hover,
:global(.dark) .public-header__icon-button.is-active,
:global(.dark) .public-header__login:hover {
  background: #242427;
  box-shadow: none;
  color: #f5f5f7;
}

:global(.dark) .public-header__auth-actions {
  background: #1d1d20;
}

:global(.dark) .public-header__badge {
  border-color: #111113;
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
