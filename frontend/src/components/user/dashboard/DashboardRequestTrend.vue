<template>
  <article class="dashboard-request-trend card">
    <header class="dashboard-request-trend-header">
      <div>
        <h2>{{ t('dashboard.requestTrend') }}</h2>
        <p>{{ t('dashboard.requestTrendDescription') }}</p>
      </div>
      <div class="dashboard-period-segmented" role="group" :aria-label="t('dashboard.timeRange')">
        <button
          v-for="days in periods"
          :key="days"
          type="button"
          :class="{ 'is-active': period === days }"
          :aria-pressed="period === days"
          @click="emit('periodChange', days)"
        >
          {{ t('dashboard.dayCount', { count: days }) }}
        </button>
      </div>
    </header>

    <div class="dashboard-request-summary">
      <strong>{{ formatCompact(totalRequests) }}</strong>
      <span><i aria-hidden="true"></i>{{ t('dashboard.requestCount') }}</span>
    </div>

    <div class="dashboard-request-canvas">
      <LoadingSpinner v-if="loading" size="md" />
      <svg
        v-else-if="chartPoints.length"
        ref="chartRef"
        class="dashboard-request-svg"
        :viewBox="`0 0 ${chartWidth} ${chartHeight}`"
        role="img"
        :aria-label="t('dashboard.requestTrend')"
        preserveAspectRatio="none"
      >
        <defs>
          <linearGradient id="dashboard-request-area" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stop-color="currentColor" stop-opacity="0.18" />
            <stop offset="1" stop-color="currentColor" stop-opacity="0" />
          </linearGradient>
        </defs>

        <g class="dashboard-request-grid" aria-hidden="true">
          <g v-for="tick in yTicks" :key="tick.y">
            <line :x1="plotLeft" :x2="chartWidth - plotRight" :y1="tick.y" :y2="tick.y" />
            <text :x="plotLeft - 9" :y="tick.y + 4">{{ formatCompact(tick.value) }}</text>
          </g>
        </g>

        <path class="dashboard-request-area" :d="areaPath" aria-hidden="true" />
        <polyline class="dashboard-request-line" :points="linePoints" aria-hidden="true" />

        <g class="dashboard-request-points">
          <circle v-for="point in chartPoints" :key="point.date" :cx="point.x" :cy="point.y" r="8">
            <title>{{ point.date }}: {{ point.requests.toLocaleString() }}</title>
          </circle>
        </g>

        <g class="dashboard-request-x-labels" aria-hidden="true">
          <text
            v-for="label in xLabels"
            :key="label.date"
            :x="label.x"
            :y="chartHeight - 5"
            :text-anchor="label.anchor"
          >
            {{ formatAxisDate(label.date) }}
          </text>
        </g>
      </svg>
      <p v-else>{{ t('dashboard.noDataAvailable') }}</p>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import type { TrendDataPoint } from '@/types'

const props = defineProps<{
  trend: TrendDataPoint[]
  loading: boolean
  period: 7 | 30 | 90
}>()

const emit = defineEmits<{
  periodChange: [days: 7 | 30 | 90]
}>()

const { t } = useI18n()
const periods = [7, 30, 90] as const
const chartRef = ref<SVGSVGElement | null>(null)
const chartWidth = ref(720)
const chartHeight = ref(250)
const plotLeft = 44
const plotRight = 8
const plotTop = 8
const plotBottom = 28
const plotWidth = computed(() => chartWidth.value - plotLeft - plotRight)
const plotHeight = computed(() => chartHeight.value - plotTop - plotBottom)
let resizeObserver: ResizeObserver | null = null

const totalRequests = computed(() => props.trend.reduce((sum, point) => sum + point.requests, 0))
const maxRequests = computed(() => Math.max(1, ...props.trend.map((point) => point.requests)))

const chartPoints = computed(() => props.trend.map((point, index) => ({
  date: point.date,
  requests: point.requests,
  x: plotLeft + (props.trend.length === 1 ? plotWidth.value / 2 : (index / (props.trend.length - 1)) * plotWidth.value),
  y: plotTop + plotHeight.value - (point.requests / maxRequests.value) * plotHeight.value,
})))

const linePoints = computed(() => chartPoints.value.map((point) => `${point.x},${point.y}`).join(' '))
const areaPath = computed(() => {
  if (!chartPoints.value.length) return ''
  const bottom = plotTop + plotHeight.value
  const first = chartPoints.value[0]
  const last = chartPoints.value[chartPoints.value.length - 1]
  return `M ${first.x} ${bottom} L ${linePoints.value.split(' ').join(' L ')} L ${last.x} ${bottom} Z`
})

const yTicks = computed(() => [0, 0.5, 1].map((ratio) => ({
  value: Math.round(maxRequests.value * (1 - ratio)),
  y: plotTop + plotHeight.value * ratio,
})))

const xLabels = computed(() => {
  const points = chartPoints.value
  if (!points.length) return []
  const indexes = Array.from(new Set([0, Math.floor((points.length - 1) / 2), points.length - 1]))
  return indexes.map((index, labelIndex) => ({
    ...points[index],
    anchor: labelIndex === 0 ? 'start' : labelIndex === indexes.length - 1 ? 'end' : 'middle',
  }))
})

function formatAxisDate(value: string): string {
  const parts = value.split('-')
  return parts.length === 3 ? `${parts[1]}/${parts[2]}` : value
}

function formatCompact(value: number): string {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return value.toLocaleString()
}

watch(chartRef, (element) => {
  resizeObserver?.disconnect()
  resizeObserver = null
  if (!element || typeof ResizeObserver === 'undefined') return
  resizeObserver = new ResizeObserver(([entry]) => {
    if (!entry) return
    chartWidth.value = Math.max(240, Math.round(entry.contentRect.width))
    chartHeight.value = Math.max(160, Math.round(entry.contentRect.height))
  })
  resizeObserver.observe(element)
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  resizeObserver = null
})
</script>
