<template>
  <div
    v-if="status && status.models.length > 0"
    :class="compact ? 'cursor-pointer' : ''"
    @click="compact ? router.push('/status') : null"
  >
    <!-- Overall status header (hidden in compact mode) -->
    <div v-if="!compact" class="flex items-center justify-between mb-4">
      <div class="flex items-center gap-2">
        <span :class="overallDotClass" class="w-2.5 h-2.5 rounded-full" />
        <span class="text-base font-semibold text-gray-900 dark:text-gray-100">{{ overallText }}</span>
      </div>
      <span class="text-sm font-mono tabular-nums text-gray-500 dark:text-gray-400">{{ currentTime }}</span>
    </div>

    <!-- Model bars -->
    <div :class="compact ? 'space-y-2' : 'space-y-3'">
      <ServiceStatusBar
        v-for="model in status.models"
        :key="model.model"
        :model-status="model"
        :interval-minutes="status.interval_minutes || 5"
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
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { statusAPI } from '@/api/status'
import type { ServiceStatusResponse } from '@/api/status'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
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
const appStore = useAppStore()
const authStore = useAuthStore()

const status = ref<ServiceStatusResponse | null>(null)
const loading = ref(true)
const currentTime = ref('')
let clockTimer: ReturnType<typeof setInterval> | null = null

function updateClock() {
  currentTime.value = new Date().toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  })
}

onMounted(async () => {
  updateClock()
  clockTimer = setInterval(updateClock, 1000)
  try {
    const data = await statusAPI.getStatus()
    const visible =
      data.overall_status !== 'unknown' &&
      data.models.length > 0 &&
      (data.public_visible || authStore.isAdmin)

    if (!visible) {
      // Treat unknown as disabled; render nothing
      status.value = null
      appStore.statusProbeEnabled = false
    } else {
      status.value = data
      appStore.statusProbeEnabled = true
    }
  } catch {
    // Fetch failed; render nothing
    status.value = null
    appStore.statusProbeEnabled = false
  } finally {
    loading.value = false
  }
})

onUnmounted(() => {
  if (clockTimer) clearInterval(clockTimer)
})
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

</script>
