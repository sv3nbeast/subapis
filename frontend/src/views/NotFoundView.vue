<template>
  <PublicLayout>
  <div
    class="public-not-found-view relative flex min-h-screen items-center justify-center overflow-hidden bg-gray-50 px-4 dark:bg-dark-950"
  >
    <!-- Background Decoration -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div
        class="absolute -right-40 -top-40 h-80 w-80 rounded-full bg-primary-400/10 blur-3xl"
      ></div>
      <div
        class="absolute -bottom-40 -left-40 h-80 w-80 rounded-full bg-primary-500/10 blur-3xl"
      ></div>
    </div>

    <div class="relative z-10 w-full max-w-md text-center">
      <!-- 404 Display -->
      <div class="mb-8">
        <div class="relative inline-block">
          <span class="text-[12rem] font-bold leading-none text-gray-100 dark:text-dark-800"
            >404</span
          >
          <div class="absolute inset-0 flex items-center justify-center">
            <div class="public-not-found-icon flex h-24 w-24 items-center justify-center rounded-2xl bg-primary-600 shadow-lg shadow-primary-500/30">
              <Icon name="exclamationTriangle" size="xl" class="text-white" />
            </div>
          </div>
        </div>
      </div>

      <!-- Text Content -->
      <div class="mb-8">
        <h1 class="mb-3 text-2xl font-bold text-gray-900 dark:text-white">
          {{ t('errors.pageNotFound') }}
        </h1>
        <p class="text-gray-500 dark:text-dark-400">
          {{ t('errors.pageNotFoundDescription') }}
        </p>
      </div>

      <!-- Action Buttons -->
      <div class="flex flex-col justify-center gap-3 sm:flex-row">
        <button @click="goBack" class="btn btn-secondary">
          <Icon name="arrowLeft" size="md" class="mr-2" />
          {{ t('common.back') }}
        </button>
        <router-link :to="dashboardPath" class="btn btn-primary">
          <Icon name="home" size="md" class="mr-2" />
          {{ authStore.isAuthenticated ? t('home.goToDashboard') : t('docsGuide.nav.home') }}
        </router-link>
      </div>

      <!-- Help Link -->
      <p class="mt-8 text-sm text-gray-400 dark:text-dark-500">
        {{ t('errors.needHelp') }}
        <a
          href="#"
          class="text-primary-600 transition-colors hover:text-primary-500 dark:text-primary-400 dark:hover:text-primary-300"
        >
          {{ t('common.contactSupport') }}
        </a>
      </p>
    </div>
  </div>
  </PublicLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'
import PublicLayout from '@/components/public/PublicLayout.vue'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const router = useRouter()
const authStore = useAuthStore()
const dashboardPath = authStore.isAuthenticated
  ? (authStore.isAdmin ? '/admin/dashboard' : '/dashboard')
  : '/home'

function goBack(): void {
  router.back()
}
</script>
