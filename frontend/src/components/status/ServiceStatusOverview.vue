<template>
  <div
    v-if="status && status.models.length > 0"
    class="service-status-content"
    :class="compact ? 'cursor-pointer' : ''"
    @click="compact ? router.push('/status') : null"
  >
    <div v-if="compact" class="dashboard-route-health">
      <header class="dashboard-route-health-header">
        <div>
          <h2>{{ t('dashboard.routeHealth') }}</h2>
          <p>{{ t('status.hoursAgoLabel') }}</p>
        </div>
        <span class="dashboard-route-status" :class="dashboardStatusClass">
          <i aria-hidden="true"></i>{{ overallText }}
        </span>
      </header>

      <div class="dashboard-route-score">
        <div class="dashboard-route-ring" :style="{ '--route-score': overallUptime }">
          <strong>{{ overallUptime.toFixed(2) }}%</strong>
          <span>{{ t('dashboard.successRate') }}</span>
        </div>
      </div>

      <div class="dashboard-route-list">
        <div v-for="model in status.models" :key="model.model" class="dashboard-route-row">
          <i :class="modelStatusTone(model.current_status)" aria-hidden="true"></i>
          <strong :title="model.display_name">{{ model.display_name }}</strong>
          <span>{{ model.uptime_percentage.toFixed(2) }}%</span>
          <small>{{ formatModelLatency(model) }}</small>
        </div>
      </div>
    </div>

    <div v-if="!compact" class="service-status-bars">
      <div class="mb-4 flex items-center justify-between">
        <div class="flex items-center gap-2">
          <span :class="overallDotClass" class="h-2.5 w-2.5 rounded-full" />
          <span class="text-base font-semibold text-gray-900 dark:text-gray-100">{{ overallText }}</span>
        </div>
        <span class="font-mono text-sm tabular-nums text-gray-500 dark:text-gray-400">{{ currentTime }}</span>
      </div>

      <!-- Model bars -->
      <div class="space-y-3">
        <ServiceStatusBar
          v-for="model in status.models"
          :key="model.model"
          :model-status="model"
          :interval-minutes="status.interval_minutes || 5"
        />
      </div>
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
import type { ModelStatus } from '@/api/status'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import ServiceStatusBar from '@/components/status/ServiceStatusBar.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'

interface Props {
  compact?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  compact: false
})

const router = useRouter()
const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const status = computed(() => appStore.serviceStatus)
const loading = computed(() => appStore.serviceStatusLoading && !status.value)
const currentTime = ref('')
let clockTimer: ReturnType<typeof setInterval> | null = null

function updateClock() {
  currentTime.value = new Date().toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  })
}

onMounted(() => {
  if (!props.compact) {
    updateClock()
    clockTimer = setInterval(updateClock, 1000)
  }
  void appStore.fetchServiceStatus(authStore.isAdmin)
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

const overallUptime = computed(() => {
  if (!status.value?.models.length) return 0
  const total = status.value.models.reduce((sum, model) => sum + model.uptime_percentage, 0)
  return Math.min(100, Math.max(0, total / status.value.models.length))
})

const dashboardStatusClass = computed(() => {
  switch (status.value?.overall_status) {
    case 'operational':
      return 'is-operational'
    case 'degraded':
      return 'is-degraded'
    case 'major_outage':
      return 'is-outage'
    default:
      return 'is-unknown'
  }
})

function modelStatusTone(state: ModelStatus['current_status']): string {
  switch (state) {
    case 'operational':
      return 'is-operational'
    case 'degraded':
      return 'is-degraded'
    case 'outage':
      return 'is-outage'
    default:
      return 'is-unknown'
  }
}

function formatModelLatency(model: ModelStatus): string {
  for (let index = model.hourly_stats.length - 1; index >= 0; index -= 1) {
    const sample = model.hourly_stats[index]
    if (sample && sample.total > 0) {
      if (sample.avg_latency_ms >= 1000) return `${(sample.avg_latency_ms / 1000).toFixed(2)}s`
      return `${Math.round(sample.avg_latency_ms)}ms`
    }
  }
  return '-'
}

</script>
