<template>
  <tr class="border-t border-gray-100 transition-colors hover:bg-primary-50/35 dark:border-dark-700/70 dark:hover:bg-dark-700/35">
    <td class="min-w-[15rem] px-3 py-2.5">
      <div class="min-w-0">
        <div class="truncate text-sm font-semibold text-gray-900 dark:text-white">
          {{ model.name }}
        </div>
        <div
          v-if="hasIntervals"
          class="mt-0.5 text-[11px] text-gray-400 dark:text-gray-500"
        >
          {{ t('availableChannels.groupCards.intervalHint', { count: model.pricing?.intervals.length || 0 }) }}
        </div>
      </div>
    </td>

    <td class="whitespace-nowrap px-3 py-2.5">
      <span
        :class="[
          'inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-bold',
          billingModeClass,
        ]"
      >
        {{ billingModeLabel }}
      </span>
    </td>

    <td class="whitespace-nowrap px-3 py-2.5 font-mono text-xs font-semibold text-gray-700 dark:text-gray-200">
      {{ priceValues.input }}
    </td>
    <td class="whitespace-nowrap px-3 py-2.5 font-mono text-xs font-semibold text-gray-700 dark:text-gray-200">
      {{ priceValues.output }}
    </td>
    <td class="whitespace-nowrap px-3 py-2.5 font-mono text-xs font-semibold text-gray-700 dark:text-gray-200">
      {{ priceValues.cacheRead }}
    </td>
    <td class="whitespace-nowrap px-3 py-2.5 font-mono text-xs font-semibold text-gray-700 dark:text-gray-200">
      {{ priceValues.cacheWrite }}
    </td>
    <td class="min-w-[7.5rem] whitespace-nowrap px-3 py-2.5 text-xs font-medium text-gray-600 dark:text-gray-300">
      {{ priceValues.other }}
    </td>
  </tr>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UserSupportedModel } from '@/api/channels'
import {
  BILLING_MODE_IMAGE,
  BILLING_MODE_PER_REQUEST,
  BILLING_MODE_TOKEN,
} from '@/constants/channel'
import { formatScaled } from '@/utils/pricing'

const props = defineProps<{
  model: UserSupportedModel
  noPricingLabel: string
}>()

const { t } = useI18n()
const perMillionScale = 1_000_000

const hasIntervals = computed(() => (props.model.pricing?.intervals?.length || 0) > 0)

const billingModeLabel = computed(() => {
  switch (props.model.pricing?.billing_mode) {
    case BILLING_MODE_TOKEN:
      return t('availableChannels.pricing.billingModeToken')
    case BILLING_MODE_PER_REQUEST:
      return t('availableChannels.pricing.billingModePerRequest')
    case BILLING_MODE_IMAGE:
      return t('availableChannels.pricing.billingModeImage')
    default:
      return t('availableChannels.groupCards.unknownBilling')
  }
})

const billingModeClass = computed(() => {
  switch (props.model.pricing?.billing_mode) {
    case BILLING_MODE_PER_REQUEST:
      return 'bg-cyan-100 text-cyan-700 dark:bg-cyan-500/15 dark:text-cyan-300'
    case BILLING_MODE_IMAGE:
      return 'bg-blue-100 text-blue-700 dark:bg-blue-500/15 dark:text-blue-300'
    case BILLING_MODE_TOKEN:
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
    default:
      return 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'
  }
})

const priceValues = computed(() => {
  const pricing = props.model.pricing
  if (!pricing) {
    return {
      input: '-',
      output: '-',
      cacheRead: '-',
      cacheWrite: '-',
      other: props.noPricingLabel,
    }
  }

  if (pricing.billing_mode === BILLING_MODE_PER_REQUEST) {
    return {
      input: '-',
      output: '-',
      cacheRead: '-',
      cacheWrite: '-',
      other: formatPrice(pricing.per_request_price, 1, t('availableChannels.pricing.unitPerRequest')),
    }
  }

  if (pricing.billing_mode === BILLING_MODE_IMAGE) {
    return {
      input: '-',
      output: '-',
      cacheRead: '-',
      cacheWrite: '-',
      other: formatPrice(pricing.image_output_price, 1, t('availableChannels.pricing.unitPerRequest')),
    }
  }

  return {
    input: formatScaled(pricing.input_price, perMillionScale),
    output: formatScaled(pricing.output_price, perMillionScale),
    cacheRead: formatScaled(pricing.cache_read_price, perMillionScale),
    cacheWrite: formatScaled(pricing.cache_write_price, perMillionScale),
    other: '-',
  }
})

function formatPrice(value: number | null, scale: number, unit: string): string {
  if (value == null) return '-'
  return `${formatScaled(value, scale)} ${unit}`
}
</script>
