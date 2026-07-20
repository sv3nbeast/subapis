<template>
  <nav class="ui-v2-mobile-dock" :aria-label="dockAriaLabel">
    <router-link
      v-for="item in items"
      :key="item.path"
      :to="item.path"
      class="ui-v2-mobile-dock-item"
      :class="{ 'ui-v2-mobile-dock-item-active': isActive(item.path) }"
      :aria-current="isActive(item.path) ? 'page' : undefined"
      @pointerdown="handlePointerDown"
      @pointerup="handlePointerRelease"
      @pointercancel="handlePointerRelease"
      @lostpointercapture="handlePointerRelease"
    >
      <Icon :name="item.icon" size="md" :stroke-width="1.8" />
      <span>{{ item.label }}</span>
    </router-link>
  </nav>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import Icon from '@/components/icons/Icon.vue'

type DockIcon = 'grid' | 'key' | 'chart' | 'globe'

interface DockItem {
  path: string
  label: string
  icon: DockIcon
}

const route = useRoute()
const { t } = useI18n()
const authStore = useAuthStore()

const dockAriaLabel = computed(() => t('nav.mainNavigation'))

const items = computed<DockItem[]>(() => authStore.isAdmin
  ? [
      { path: '/admin/dashboard', label: t('nav.dashboard'), icon: 'grid' },
      { path: '/admin/accounts', label: t('nav.accounts'), icon: 'globe' },
      { path: '/admin/usage', label: t('nav.usage'), icon: 'chart' },
    ]
  : [
      { path: '/dashboard', label: t('nav.dashboard'), icon: 'grid' },
      { path: '/keys', label: t('nav.apiKeys'), icon: 'key' },
      { path: '/usage', label: t('nav.usage'), icon: 'chart' },
    ])

function isActive(path: string): boolean {
  return route.path === path || route.path.startsWith(`${path}/`)
}

function handlePointerDown(event: PointerEvent): void {
  const target = event.currentTarget as HTMLElement | null
  if (!target || (event.pointerType === 'mouse' && event.button !== 0)) return
  target.setPointerCapture?.(event.pointerId)
  target?.style.setProperty('--dock-press', '0.96')
}

function handlePointerRelease(event: PointerEvent): void {
  const target = event.currentTarget as HTMLElement | null
  target?.style.removeProperty('--dock-press')
  if (target?.hasPointerCapture?.(event.pointerId)) {
    target.releasePointerCapture(event.pointerId)
  }
}
</script>
