<template>
  <div>
    <!-- Header row: status dot + model name + uptime percentage (hidden in compact) -->
    <div class="flex items-center justify-between mb-1">
      <div class="flex items-center gap-1.5">
        <span :class="dotClass" class="w-1.5 h-1.5 rounded-full inline-block" />
        <span class="font-medium text-sm text-gray-900 dark:text-gray-100">{{ modelStatus.display_name }}</span>
      </div>
      <span v-if="!compact" class="text-xs text-gray-500 dark:text-gray-400">{{ formattedUptime }}</span>
    </div>

    <!-- Hover wrapper -->
    <div
      style="padding-top: 5rem; margin-top: -5rem;"
      @mouseleave="activeSlot = null"
    >
      <!-- Bar: 24 slots = 24 hours -->
      <div
        ref="barRef"
        class="flex gap-px h-3 relative"
        @mousemove="onBarMouseMove"
      >
        <div
          v-for="(slot, i) in slots"
          :key="i"
          class="flex-1 rounded-[1px] transition-opacity"
          :class="slotColorClass(slot)"
        />

        <!-- Tooltip -->
        <div
          v-show="activeSlot !== null"
          class="absolute z-50 bottom-full mb-2 px-3 py-2 bg-gray-900 dark:bg-gray-800 text-white text-xs rounded-lg shadow-lg whitespace-nowrap pointer-events-none"
          :style="tooltipStyle"
        >
          <div class="font-medium">{{ tooltipData?.timeRange }}</div>
          <div class="text-gray-300">{{ tooltipData?.probeInfo }}</div>
          <div v-if="tooltipData?.failCount" class="text-red-300">
            {{ t('status.outage') }} {{ tooltipData.failDuration }}
          </div>
          <div v-if="tooltipData?.avgLatency" class="text-gray-400">
            {{ t('status.avgLatency') }}: {{ tooltipData.avgLatency }}ms
          </div>
        </div>
      </div>
    </div>

    <!-- Time labels (hidden in compact) -->
    <div v-if="!compact" class="flex justify-between mt-1">
      <span class="text-[10px] text-gray-400 dark:text-gray-500">{{ t('status.hoursAgoLabel') }}</span>
      <span class="text-[10px] text-gray-400 dark:text-gray-500">{{ t('status.now') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelStatus, HourlyStat, ProbeFailure } from '@/api/status'

interface Props {
  modelStatus: ModelStatus
  intervalMinutes?: number
  compact?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  intervalMinutes: 5,
  compact: false
})

const { t } = useI18n()

const barRef = ref<HTMLElement | null>(null)
const activeSlot = ref<number | null>(null)

// 24 hours / interval = total slots
const totalSlots = computed(() => Math.floor(24 * 60 / props.intervalMinutes))

function onBarMouseMove(e: MouseEvent) {
  if (!barRef.value) return
  const rect = barRef.value.getBoundingClientRect()
  const x = e.clientX - rect.left
  const count = totalSlots.value
  const index = Math.floor((x / rect.width) * count)
  activeSlot.value = Math.max(0, Math.min(count - 1, index))
}

interface SlotData {
  hour: string
  success: number
  total: number
  avgLatency: number
  failures: ProbeFailure[]
  hasData: boolean
}

function buildStatsMap(): Map<string, HourlyStat> {
  const map = new Map<string, HourlyStat>()
  const intervalMs = props.intervalMinutes * 60_000
  for (const stat of props.modelStatus.hourly_stats) {
    // Truncate to interval window to match backend aggregation
    const d = new Date(stat.hour)
    d.setTime(Math.floor(d.getTime() / intervalMs) * intervalMs)
    map.set(d.toISOString(), stat)
  }
  return map
}

const slots = computed<SlotData[]>(() => {
  const statsMap = buildStatsMap()
  const now = new Date()
  const currentSlot = new Date(now)
  const intervalMs = props.intervalMinutes * 60_000
  // Truncate to current interval window
  currentSlot.setTime(Math.floor(currentSlot.getTime() / intervalMs) * intervalMs)

  const count = totalSlots.value
  const result: SlotData[] = []
  for (let i = 0; i < count; i++) {
    const slotTime = new Date(currentSlot.getTime() - (count - 1 - i) * intervalMs)
    const key = slotTime.toISOString()
    const stat = statsMap.get(key)

    if (stat) {
      result.push({
        hour: key,
        success: stat.success,
        total: stat.total,
        avgLatency: stat.avg_latency_ms,
        failures: stat.failures ?? [],
        hasData: true
      })
    } else {
      result.push({
        hour: key,
        success: 0,
        total: 0,
        avgLatency: 0,
        failures: [],
        hasData: false
      })
    }
  }
  return result
})

function slotColorClass(slot: SlotData): string {
  if (!slot.hasData || slot.total === 0) {
    return 'bg-gray-200 dark:bg-gray-700 opacity-60'
  }
  const failRate = (slot.total - slot.success) / slot.total
  if (failRate >= 0.5) {
    return 'bg-red-500 opacity-85'
  }
  if (failRate > 0) {
    return 'bg-amber-400 opacity-80'
  }
  return 'bg-emerald-500 opacity-80'
}

const dotClass = computed<string>(() => {
  switch (props.modelStatus.current_status) {
    case 'operational':
      return 'bg-emerald-500'
    case 'degraded':
      return 'bg-amber-400'
    case 'outage':
      return 'bg-red-500 shadow-[0_0_6px_rgba(239,68,68,0.6)]'
    default:
      return 'bg-gray-400'
  }
})

const formattedUptime = computed<string>(() => {
  return `${props.modelStatus.uptime_percentage.toFixed(2)}% ${t('status.uptime')}`
})

const tooltipStyle = computed(() => {
  if (activeSlot.value === null) return {}
  const pct = (activeSlot.value / totalSlots.value) * 100
  const clamped = Math.max(15, Math.min(85, pct))
  return {
    left: `${clamped}%`,
    transform: 'translateX(-50%)'
  }
})

interface TooltipInfo {
  timeRange: string
  probeInfo: string
  failCount: number
  failDuration: string
  avgLatency: number | null
}

const tooltipData = computed<TooltipInfo | null>(() => {
  if (activeSlot.value === null) return null
  const slot = slots.value[activeSlot.value]
  if (!slot) return null

  const start = new Date(slot.hour)
  const end = new Date(start.getTime() + props.intervalMinutes * 60_000)
  const timeRange = `${formatHour(start)} - ${formatHour(end)}`

  let probeInfo: string
  if (!slot.hasData || slot.total === 0) {
    probeInfo = t('status.noData')
  } else {
    probeInfo = `${slot.success}/${slot.total} ${t('status.successful')}`
  }

  const failCount = slot.total - slot.success
  let failDuration = ''
  if (failCount > 0 && slot.failures.length > 0) {
    if (slot.failures.length === 1) {
      failDuration = '< 5' + t('status.minutes')
    } else {
      const times = slot.failures.map(f => new Date(f.time).getTime())
      const minT = Math.min(...times)
      const maxT = Math.max(...times)
      const diffMin = Math.round((maxT - minT) / 60000)
      if (diffMin < 5) {
        failDuration = '< 5' + t('status.minutes')
      } else if (diffMin < 60) {
        failDuration = '~' + diffMin + t('status.minutes')
      } else {
        failDuration = '~' + Math.round(diffMin / 60) + t('status.hours')
      }
    }
  }

  return {
    timeRange,
    probeInfo,
    failCount,
    failDuration,
    avgLatency: slot.hasData && slot.total > 0 ? Math.round(slot.avgLatency) : null
  }
})

function formatHour(d: Date): string {
  return d.toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit'
  })
}
</script>
