<template>
  <PublicLayout v-if="isPublicUiV2 && !authStore.isAuthenticated" content-class="public-status-content">
    <div class="public-status-page">
      <header>
        <p>{{ t('nav.serviceStatus') }}</p>
        <h1>{{ t('status.title') }}</h1>
        <span>{{ t('status.description') }}</span>
      </header>
      <ServiceStatusOverview class="public-service-status" show-empty />
    </div>
  </PublicLayout>
  <AppLayout v-else>
    <ServiceStatusOverview />
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import PublicLayout from '@/components/public/PublicLayout.vue'
import ServiceStatusOverview from '@/components/status/ServiceStatusOverview.vue'
import { usePublicUiVersion } from '@/composables/usePublicUiVersion'
import { useAuthStore } from '@/stores/auth'
import { useI18n } from 'vue-i18n'

const authStore = useAuthStore()
const { isPublicUiV2 } = usePublicUiVersion()
const { t } = useI18n()
</script>
