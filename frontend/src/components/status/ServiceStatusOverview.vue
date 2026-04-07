<template>
  <div
    v-if="status && status.models.length > 0"
    :class="compact ? 'cursor-pointer' : ''"
    @click="compact ? router.push('/status') : null"
  >
    <!-- Overall status header (hidden in compact mode) -->
    <div v-if="!compact" class="flex items-center gap-2 mb-4">
      <span :class="overallDotClass" class="w-2.5 h-2.5 rounded-full" />
      <span class="text-base font-semibold text-gray-900 dark:text-gray-100">{{ overallText }}</span>
      <span class="ml-auto text-xs text-gray-400">{{ t('status.lastUpdated') }}: {{ relativeTime }}</span>
    </div>

    <!-- Model bars -->
    <div :class="compact ? 'space-y-2' : 'space-y-3'">
      <ServiceStatusBar
        v-for="model in status.models"
        :key="model.model"
        :model-status="model"
        :compact="compact"
      />
    </div>
  </div>

  <!-- Loading state -->
  <div v-else-if="loading" class="flex items-center justify-center py-4">
    <LoadingSpinner v-if="!compact" />
  </div>

  <!-- No data / disabled: render nothing -->
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { statusAPI } from '@/api/status'
import type { ServiceStatusResponse } from '@/api/status'
import ServiceStatusBar from '@/components/status/ServiceStatusBar.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'

interface Props {
  compact?: boolean
}

withDefaults(defineProps<Props>(), {
  compact: false
})

const router = useRouter()
const { t } = useI18n()

const status = ref<ServiceStatusResponse | null>(null)
const loading = ref(true)

onMounted(async () => {
  try {
    const data = await statusAPI.getStatus()
    if (data.overall_status === 'unknown') {
      // Treat unknown as disabled; render nothing
      status.value = null
    } else {
      status.value = data
    }
  } catch {
    // Fetch failed; render nothing
    status.value = null
  } finally {
    loading.value = false
  }
})

/**
 * Overall status dot color class.
 */
const overallDotClass = computed<string>(() => {
  if (!status.value) return 'bg-gray-400'
  switch (status.value.overall_status) {
    case 'operational':
      return 'bg-emerald-500'
    case 'degraded':
      return 'bg-amber-400'
    case 'major_outage':
      return 'bg-red-500 shadow-[0_0_6px_rgba(239,68,68,0.6)]'
    default:
      return 'bg-gray-400'
  }
})

/**
 * Overall status text.
 */
const overallText = computed<string>(() => {
  if (!status.value) return ''
  switch (status.value.overall_status) {
    case 'operational':
      return t('status.allOperational')
    case 'degraded':
      return t('status.degraded')
    case 'major_outage':
      return t('status.majorOutage')
    default:
      return ''
  }
})

/**
 * Relative time since last update.
 */
const relativeTime = computed<string>(() => {
  if (!status.value?.last_updated) return t('status.never')

  const updated = new Date(status.value.last_updated)
  const now = new Date()
  const diffMs = now.getTime() - updated.getTime()
  const diffSec = Math.floor(diffMs / 1000)
  const diffMin = Math.floor(diffSec / 60)
  const diffHour = Math.floor(diffMin / 60)
  const diffDay = Math.floor(diffHour / 24)

  if (diffSec < 60) return t('status.justNow')
  if (diffMin < 60) return `${diffMin} ${t('status.minutesAgo')}`
  if (diffHour < 24) return `${diffHour} ${t('status.hoursAgo')}`
  return `${diffDay} ${t('status.daysAgoRelative')}`
})
</script>
