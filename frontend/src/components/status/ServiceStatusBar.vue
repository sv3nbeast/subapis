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

    <!-- Availability bar: 720 slots (30 days * 24 hours) -->
    <div class="flex gap-px h-3 relative" @mouseleave="hoveredSlot = null">
      <div
        v-for="(slot, i) in slots"
        :key="i"
        class="flex-1 rounded-[1px] cursor-pointer transition-opacity"
        :class="slotColorClass(slot)"
        @mouseenter="hoveredSlot = i"
      />

      <!-- Tooltip on hover -->
      <div
        v-if="hoveredSlot !== null"
        class="absolute z-50 bottom-full mb-2 px-3 py-2 bg-gray-900 dark:bg-gray-800 text-white text-xs rounded-lg shadow-lg whitespace-nowrap pointer-events-none"
        :style="tooltipStyle"
      >
        <div class="font-medium">{{ tooltipData?.timeRange }}</div>
        <div class="text-gray-300">{{ tooltipData?.probeInfo }}</div>
        <div v-if="tooltipData?.failures?.length" class="mt-1 border-t border-gray-700 pt-1">
          <div v-for="f in tooltipData.failures" :key="f.time" class="text-red-300">
            {{ formatTime(f.time) }} {{ t('status.failed') }} ({{ f.error }})
          </div>
          <div class="text-gray-400 mt-0.5">{{ t('status.duration') }}: {{ tooltipData.duration }}</div>
        </div>
        <div v-if="tooltipData?.avgLatency" class="text-gray-400">{{ t('status.avgLatency') }}: {{ tooltipData.avgLatency }}ms</div>
      </div>
    </div>

    <!-- Date labels (hidden in compact) -->
    <div v-if="!compact" class="flex justify-between mt-1">
      <span class="text-[10px] text-gray-400 dark:text-gray-500">{{ t('status.daysAgo') }}</span>
      <span class="text-[10px] text-gray-400 dark:text-gray-500">{{ t('status.today') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelStatus, HourlyStat, ProbeFailure } from '@/api/status'

interface Props {
  modelStatus: ModelStatus
  compact?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  compact: false
})

const { t } = useI18n()

const hoveredSlot = ref<number | null>(null)

const TOTAL_SLOTS = 720 // 30 days * 24 hours

interface SlotData {
  hour: string
  success: number
  total: number
  avgLatency: number
  failures: ProbeFailure[]
  hasData: boolean
}

/**
 * Build a map of hourly stats keyed by the hour string (truncated to hour).
 */
function buildStatsMap(): Map<string, HourlyStat> {
  const map = new Map<string, HourlyStat>()
  for (const stat of props.modelStatus.hourly_stats) {
    // Normalize the hour key to the start of the hour
    const d = new Date(stat.hour)
    d.setMinutes(0, 0, 0)
    map.set(d.toISOString(), stat)
  }
  return map
}

/**
 * Generate 720 slot entries covering the past 30 days, one per hour.
 */
const slots = computed<SlotData[]>(() => {
  const statsMap = buildStatsMap()
  const now = new Date()
  // Start at the current hour
  const currentHour = new Date(now)
  currentHour.setMinutes(0, 0, 0)

  const result: SlotData[] = []
  for (let i = 0; i < TOTAL_SLOTS; i++) {
    const slotTime = new Date(currentHour.getTime() - (TOTAL_SLOTS - 1 - i) * 3600_000)
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

/**
 * Determine the color class for a given slot.
 * - No data or all success: green
 * - Has failures but < 50% fail rate: yellow
 * - >= 50% fail rate: red
 */
function slotColorClass(slot: SlotData): string {
  if (!slot.hasData || slot.total === 0) {
    return 'bg-emerald-500 opacity-60'
  }
  const failRate = (slot.total - slot.success) / slot.total
  if (failRate >= 0.5) {
    return 'bg-red-500 opacity-85'
  }
  if (failRate > 0) {
    return 'bg-amber-400 opacity-80'
  }
  return 'bg-emerald-500 opacity-60'
}

/**
 * Status dot color based on current_status.
 */
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

/**
 * Formatted uptime percentage string.
 */
const formattedUptime = computed<string>(() => {
  return `${props.modelStatus.uptime_percentage.toFixed(2)}% ${t('status.uptime')}`
})

/**
 * Tooltip position style: calculate left offset and clamp to stay in bounds.
 */
const tooltipStyle = computed(() => {
  if (hoveredSlot.value === null) return {}
  const pct = (hoveredSlot.value / TOTAL_SLOTS) * 100
  // Clamp so tooltip doesn't overflow edges
  const clamped = Math.max(10, Math.min(90, pct))
  return {
    left: `${clamped}%`,
    transform: 'translateX(-50%)'
  }
})

interface TooltipInfo {
  timeRange: string
  probeInfo: string
  failures: ProbeFailure[]
  duration: string
  avgLatency: number | null
}

/**
 * Compute tooltip data for the currently hovered slot.
 */
const tooltipData = computed<TooltipInfo | null>(() => {
  if (hoveredSlot.value === null) return null
  const slot = slots.value[hoveredSlot.value]
  if (!slot) return null

  const start = new Date(slot.hour)
  const end = new Date(start.getTime() + 3600_000)

  const timeRange = `${formatTime(start.toISOString())} - ${formatTime(end.toISOString())}`

  let probeInfo: string
  if (!slot.hasData || slot.total === 0) {
    probeInfo = t('status.noData')
  } else {
    probeInfo = `${slot.success}/${slot.total} ${t('status.successful')}`
  }

  let duration = ''
  if (slot.failures.length > 0) {
    if (slot.failures.length === 1) {
      duration = `< 5 ${t('status.minutes')}`
    } else {
      const first = new Date(slot.failures[0].time)
      const last = new Date(slot.failures[slot.failures.length - 1].time)
      duration = `${formatTime(first.toISOString())} - ${formatTime(last.toISOString())}`
    }
  }

  return {
    timeRange,
    probeInfo,
    failures: slot.failures,
    duration,
    avgLatency: slot.hasData && slot.total > 0 ? Math.round(slot.avgLatency) : null
  }
})

/**
 * Format an ISO time string to a short local time representation.
 */
function formatTime(isoString: string): string {
  const d = new Date(isoString)
  return d.toLocaleTimeString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  })
}
</script>
