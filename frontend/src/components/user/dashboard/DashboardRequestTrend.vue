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
      <Line v-else-if="chartData" :data="chartData" :options="chartOptions" />
      <p v-else>{{ t('dashboard.noDataAvailable') }}</p>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CategoryScale,
  Chart as ChartJS,
  Filler,
  Legend,
  LinearScale,
  LineElement,
  PointElement,
  Tooltip,
  type ChartData,
  type ChartOptions,
} from 'chart.js'
import { Line } from 'vue-chartjs'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import type { TrendDataPoint } from '@/types'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend, Filler)

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
const isDark = ref(document.documentElement.classList.contains('dark'))
let themeObserver: MutationObserver | null = null

const totalRequests = computed(() => props.trend.reduce((sum, point) => sum + point.requests, 0))

const chartData = computed<ChartData<'line'> | null>(() => {
  if (!props.trend.length) return null

  return {
    labels: props.trend.map((point) => point.date),
    datasets: [
      {
        label: t('dashboard.requestCount'),
        data: props.trend.map((point) => point.requests),
        borderColor: isDark.value ? '#4da3ff' : '#087af5',
        backgroundColor: isDark.value ? 'rgba(77, 163, 255, 0.08)' : 'rgba(8, 122, 245, 0.06)',
        borderWidth: 2.25,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointHitRadius: 16,
        tension: 0.34,
        fill: true,
      },
    ],
  }
})

const chartOptions = computed<ChartOptions<'line'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  animation: {
    duration: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 280,
  },
  interaction: {
    intersect: false,
    mode: 'index',
  },
  plugins: {
    legend: { display: false },
    tooltip: {
      displayColors: false,
      callbacks: {
        label: (context) => `${t('dashboard.requestCount')}: ${Number(context.raw).toLocaleString()}`,
      },
    },
  },
  scales: {
    x: {
      border: { display: false },
      grid: { display: false },
      ticks: {
        color: isDark.value ? '#818188' : '#8b8b92',
        font: { size: 10 },
        maxRotation: 0,
        maxTicksLimit: 6,
      },
    },
    y: {
      beginAtZero: true,
      border: { display: false },
      grid: {
        color: isDark.value ? 'rgba(255, 255, 255, 0.07)' : 'rgba(24, 24, 27, 0.07)',
        tickBorderDash: [3, 4],
      },
      ticks: {
        color: isDark.value ? '#818188' : '#8b8b92',
        font: { size: 10 },
        maxTicksLimit: 5,
        callback: (value) => formatCompact(Number(value)),
      },
    },
  },
}))

function formatCompact(value: number): string {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return value.toLocaleString()
}

onMounted(() => {
  themeObserver = new MutationObserver(() => {
    isDark.value = document.documentElement.classList.contains('dark')
  })
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
})

onBeforeUnmount(() => {
  themeObserver?.disconnect()
  themeObserver = null
})
</script>
