<template>
  <div
    class="group/model rounded-xl border border-gray-100 bg-white/80 p-3 transition-all duration-200 hover:-translate-y-0.5 hover:border-primary-200 hover:shadow-sm dark:border-dark-700 dark:bg-dark-900/55 dark:hover:border-primary-500/30"
  >
    <div class="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <span class="truncate text-sm font-semibold text-gray-900 dark:text-white">
            {{ model.name }}
          </span>
          <span
            :class="[
              'inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold',
              billingModeClass,
            ]"
          >
            {{ billingModeLabel }}
          </span>
        </div>
        <p
          v-if="hasIntervals"
          class="mt-1 text-xs text-gray-500 dark:text-gray-400"
        >
          {{ t('availableChannels.groupCards.intervalHint', { count: model.pricing?.intervals.length || 0 }) }}
        </p>
        <p v-else-if="!model.pricing" class="mt-1 text-xs text-gray-400 dark:text-gray-500">
          {{ noPricingLabel }}
        </p>
      </div>

      <div class="grid min-w-0 grid-cols-2 gap-2 text-xs sm:min-w-[22rem] sm:grid-cols-4">
        <div
          v-for="item in priceItems"
          :key="item.key"
          class="rounded-lg bg-gray-50 px-2.5 py-2 dark:bg-dark-800/80"
        >
          <div class="text-[10px] font-medium text-gray-400 dark:text-gray-500">
            {{ item.label }}
          </div>
          <div class="mt-0.5 font-semibold text-gray-800 dark:text-gray-100">
            {{ item.value }}
          </div>
        </div>
      </div>
    </div>
  </div>
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

const priceItems = computed(() => {
  const pricing = props.model.pricing
  if (!pricing) {
    return [
      {
        key: 'none',
        label: t('availableChannels.groupCards.priceStatus'),
        value: props.noPricingLabel,
      },
    ]
  }

  if (pricing.billing_mode === BILLING_MODE_PER_REQUEST) {
    return [
      {
        key: 'per_request',
        label: t('availableChannels.pricing.perRequestPrice'),
        value: formatPrice(pricing.per_request_price, 1, t('availableChannels.pricing.unitPerRequest')),
      },
    ]
  }

  if (pricing.billing_mode === BILLING_MODE_IMAGE) {
    return [
      {
        key: 'image',
        label: t('availableChannels.pricing.imageOutputPrice'),
        value: formatPrice(pricing.image_output_price, 1, t('availableChannels.pricing.unitPerRequest')),
      },
    ]
  }

  return [
    {
      key: 'input',
      label: t('availableChannels.pricing.inputPrice'),
      value: formatPrice(pricing.input_price, perMillionScale, t('availableChannels.pricing.unitPerMillion')),
    },
    {
      key: 'output',
      label: t('availableChannels.pricing.outputPrice'),
      value: formatPrice(pricing.output_price, perMillionScale, t('availableChannels.pricing.unitPerMillion')),
    },
    {
      key: 'cache_read',
      label: t('availableChannels.pricing.cacheReadPrice'),
      value: formatPrice(pricing.cache_read_price, perMillionScale, t('availableChannels.pricing.unitPerMillion')),
    },
    {
      key: 'cache_write',
      label: t('availableChannels.pricing.cacheWritePrice'),
      value: formatPrice(pricing.cache_write_price, perMillionScale, t('availableChannels.pricing.unitPerMillion')),
    },
  ]
})

function formatPrice(value: number | null, scale: number, unit: string): string {
  if (value == null) return '-'
  return `${formatScaled(value, scale)} ${unit}`
}
</script>
