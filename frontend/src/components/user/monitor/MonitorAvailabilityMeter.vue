<template>
  <div :class="compact ? 'min-w-[6.5rem]' : ''">
    <div
      v-if="!compact && (label || showStatus)"
      class="flex min-h-5 items-center justify-between gap-3"
    >
      <span class="text-[11px] font-semibold uppercase tracking-widest text-gray-400">
        {{ label }}
      </span>
      <span
        v-if="showStatus"
        class="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-semibold"
        :class="palette.badge"
      >
        <span class="h-1.5 w-1.5 rounded-full" :class="palette.dot"></span>
        {{ levelLabel }}
      </span>
    </div>

    <div
      class="flex justify-between gap-3"
      :class="compact ? 'items-center' : 'mt-2 items-end'"
    >
      <div class="flex items-baseline gap-0.5" :class="palette.text">
        <span
          class="font-bold tabular-nums leading-none"
          :class="compact ? 'text-sm' : 'text-3xl tracking-tight'"
        >
          {{ displayValue }}
        </span>
        <span v-if="hasData" :class="compact ? 'text-[10px] font-semibold' : 'text-sm font-semibold'">
          %
        </span>
      </div>

      <span
        v-if="!compact && note"
        class="max-w-[45%] truncate text-right text-[11px] text-gray-400"
      >
        {{ note }}
      </span>
      <span
        v-else-if="compact"
        class="h-1.5 w-1.5 flex-none rounded-full"
        :class="palette.dot"
        :title="levelLabel"
      ></span>
    </div>

    <div
      class="relative overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700/80"
      :class="compact ? 'mt-1.5 h-1.5' : 'mt-2.5 h-2'"
      :role="hasData ? 'progressbar' : undefined"
      :aria-label="ariaLabel"
      :aria-valuemin="hasData ? 0 : undefined"
      :aria-valuemax="hasData ? 100 : undefined"
      :aria-valuenow="normalizedValue ?? undefined"
    >
      <div
        class="h-full rounded-full transition-[width] duration-500 ease-out"
        :class="palette.bar"
        :style="{ width: `${normalizedValue ?? 0}%` }"
      ></div>
    </div>

    <div
      v-if="!compact"
      class="mt-1.5 flex items-center justify-between text-[10px] text-gray-400"
    >
      <span>{{ hasData ? levelDescription : t('monitorCommon.availability.noSamples') }}</span>
      <span class="tabular-nums">{{ t('monitorCommon.availability.target', { n: target }) }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  availabilityLevel,
  normalizeAvailability,
  type AvailabilityLevel,
} from '@/utils/availability'

const props = withDefaults(defineProps<{
  value: number | null | undefined
  label?: string
  note?: string
  target?: number
  compact?: boolean
  showStatus?: boolean
}>(), {
  label: '',
  note: '',
  target: 99,
  compact: false,
  showStatus: true,
})

const { t } = useI18n()

const PALETTES: Record<AvailabilityLevel, {
  text: string
  badge: string
  dot: string
  bar: string
}> = {
  excellent: {
    text: 'text-emerald-600 dark:text-emerald-300',
    badge: 'bg-emerald-50 text-emerald-700 ring-1 ring-inset ring-emerald-200/70 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/20',
    dot: 'bg-emerald-500',
    bar: 'bg-gradient-to-r from-emerald-400 to-emerald-500',
  },
  stable: {
    text: 'text-sky-600 dark:text-sky-300',
    badge: 'bg-sky-50 text-sky-700 ring-1 ring-inset ring-sky-200/70 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-500/20',
    dot: 'bg-sky-500',
    bar: 'bg-gradient-to-r from-sky-400 to-sky-500',
  },
  unstable: {
    text: 'text-amber-600 dark:text-amber-300',
    badge: 'bg-amber-50 text-amber-700 ring-1 ring-inset ring-amber-200/70 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/20',
    dot: 'bg-amber-500',
    bar: 'bg-gradient-to-r from-amber-400 to-amber-500',
  },
  critical: {
    text: 'text-red-600 dark:text-red-300',
    badge: 'bg-red-50 text-red-700 ring-1 ring-inset ring-red-200/70 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-500/20',
    dot: 'bg-red-500',
    bar: 'bg-gradient-to-r from-red-400 to-red-500',
  },
  noData: {
    text: 'text-gray-400 dark:text-gray-500',
    badge: 'bg-gray-100 text-gray-500 ring-1 ring-inset ring-gray-200/70 dark:bg-dark-700 dark:text-gray-400 dark:ring-dark-600',
    dot: 'bg-gray-400 dark:bg-gray-500',
    bar: 'bg-gray-300 dark:bg-dark-600',
  },
}

const normalizedValue = computed(() => normalizeAvailability(props.value))
const hasData = computed(() => normalizedValue.value !== null)
const level = computed(() => availabilityLevel(props.value))
const palette = computed(() => PALETTES[level.value])
const displayValue = computed(() => normalizedValue.value == null ? '--' : normalizedValue.value.toFixed(2))
const levelLabel = computed(() => t(`monitorCommon.availability.level.${level.value}`))
const levelDescription = computed(() => t(`monitorCommon.availability.description.${level.value}`))
const ariaLabel = computed(() => t('monitorCommon.availability.ariaLabel', {
  label: props.label || t('monitorCommon.availabilityPrefix'),
  value: hasData.value ? `${displayValue.value}%` : t('monitorCommon.availability.noSamples'),
  level: levelLabel.value,
}))
</script>
