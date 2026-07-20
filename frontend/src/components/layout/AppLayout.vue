<template>
  <div
    class="app-shell min-h-screen bg-gray-50 dark:bg-dark-950"
    :class="{
      'ui-v2': isV2,
      'ui-v2-sidebar-collapsed': isV2 && sidebarCollapsed,
    }"
    :data-ui-version="uiVersion"
  >
    <!-- Background Decoration -->
    <div class="pointer-events-none fixed inset-0 bg-mesh-gradient"></div>

    <!-- Sidebar -->
    <AppSidebar />

    <!-- Main Content Area -->
    <div
      class="app-workspace relative min-h-screen transition-all duration-300"
      :class="[sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64']"
    >
      <!-- Header -->
      <AppHeader :ui-version="uiVersion" @use-legacy-ui="useLegacyUi" />

      <!-- Main Content -->
      <main class="app-content-shell p-3 md:p-4 lg:p-5">
        <slot :ui-version="uiVersion" />
      </main>
    </div>

    <AppMobileDock v-if="isV2 && !appStore.mobileOpen" />
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, onBeforeMount, onBeforeUnmount, onMounted, watch } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'
import AppMobileDock from './AppMobileDock.vue'
import { useUiVersion } from '@/composables/useUiVersion'

const appStore = useAppStore()
const authStore = useAuthStore()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')
const uiVersionSubject = computed(() => authStore.user?.id)
const { uiVersion, useLegacyUi } = useUiVersion(uiVersionSubject)
const isV2 = computed(() => uiVersion.value === 'v2')

const { replayTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()

function syncUiVersionClass(): void {
  document.documentElement.classList.toggle('ui-v2-active', isV2.value)
}

onBeforeMount(() => {
  document.documentElement.classList.add('app-density-compact')
  syncUiVersionClass()
})

watch(isV2, syncUiVersionClass)

onBeforeUnmount(() => {
  document.documentElement.classList.remove('app-density-compact')
  document.documentElement.classList.remove('ui-v2-active')
})

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
})

defineExpose({ replayTour })
</script>

<style scoped>
.app-content-shell {
  animation: app-content-enter 180ms cubic-bezier(0.2, 0.8, 0.2, 1);
  will-change: opacity, transform;
}

@keyframes app-content-enter {
  from {
    opacity: 0;
    transform: translateY(8px) scale(0.998);
  }

  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

@media (prefers-reduced-motion: reduce) {
  .app-content-shell {
    animation: none;
    will-change: auto;
  }
}
</style>
