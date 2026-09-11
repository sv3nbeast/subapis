<template>
  <tr class="border-t border-gray-100 transition-colors hover:bg-primary-50/35 dark:border-dark-700/70 dark:hover:bg-dark-700/35">
    <td class="min-w-[15rem] px-3 py-2.5">
      <div class="min-w-0">
        <div class="flex min-w-0 flex-wrap items-center gap-1.5">
          <span class="truncate text-sm font-semibold text-gray-900 dark:text-white">
            {{ model.name }}
          </span>
          <span
            v-if="hasContextTiers"
            data-testid="long-context-badge"
            class="inline-flex items-center rounded-md bg-amber-50 px-1.5 py-0.5 text-[10px] font-semibold text-amber-700 dark:bg-amber-500/15 dark:text-amber-300"
            :title="t('availableChannels.groupCards.longContextTierHint')"
          >
            {{ t('availableChannels.groupCards.longContextBadge') }}
          </span>
        </div>
        <div
          v-if="hasContextTiers && !longContextEnabled"
          data-testid="long-context-disabled-hint"
          class="mt-0.5 text-[11px] text-gray-400 dark:text-gray-500"
        >
          {{ t('availableChannels.groupCards.longContextDisabledHint') }}
        </div>
        <div
          v-else-if="hasIntervals && !hasContextTiers"
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

    <!-- 输入 / 输出 / 缓存读取 / 缓存写入：有上下文阶梯且分组启用时逐档一行，否则单一基础价 -->
    <td
      v-for="column in tokenColumns"
      :key="column.key"
      class="whitespace-nowrap px-3 py-2.5 font-mono text-xs font-semibold text-gray-700 dark:text-gray-200"
      :data-testid="`price-${column.key}`"
    >
      <template v-if="showContextTiers">
        <div
          v-for="(tier, idx) in contextTiers"
          :key="idx"
          class="leading-5"
          :title="t('availableChannels.groupCards.longContextTierHint')"
        >
          <span class="mr-1 font-sans font-normal text-gray-400 dark:text-gray-500">{{ formatContextTierLabel(tier) }}</span>{{ column.tier(tier) }}
        </div>
      </template>
      <template v-else>{{ column.flat }}</template>
    </td>
    <td class="min-w-[7.5rem] whitespace-nowrap px-3 py-2.5 text-xs font-medium text-gray-600 dark:text-gray-300">
      {{ priceValues.other }}
    </td>
  </tr>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UserPricingInterval, UserSupportedModel } from '@/api/channels'
import {
  BILLING_MODE_IMAGE,
  BILLING_MODE_PER_REQUEST,
  BILLING_MODE_TOKEN,
} from '@/constants/channel'
import { formatContextTierLabel, formatScaled } from '@/utils/pricing'

const props = withDefaults(
  defineProps<{
    model: UserSupportedModel
    noPricingLabel: string
    /**
     * 所属分组是否启用长上下文阶梯计费。关闭时实收只按基础档，
     * 阶梯只以徽章 + 提示披露，价格列不展开档位。
     */
    longContextEnabled?: boolean
  }>(),
  { longContextEnabled: true },
)

const { t } = useI18n()
const perMillionScale = 1_000_000

const hasIntervals = computed(() => (props.model.pricing?.intervals?.length || 0) > 0)

/** token 计费下按上下文分档的阶梯（后端按实收口径折出的逐档绝对单价），单档不算阶梯。 */
const contextTiers = computed<UserPricingInterval[]>(() => {
  const pricing = props.model.pricing
  if (!pricing || pricing.billing_mode !== BILLING_MODE_TOKEN) return []
  const tiers = [...(pricing.intervals ?? [])].sort((a, b) => a.min_tokens - b.min_tokens)
  return tiers.length > 1 ? tiers : []
})
const hasContextTiers = computed(() => contextTiers.value.length > 0)
const showContextTiers = computed(() => hasContextTiers.value && props.longContextEnabled)

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
    cacheWrite: formatCacheWrite(pricing),
    other: '-',
  }
})

interface TokenColumn {
  key: 'input' | 'output' | 'cacheRead' | 'cacheWrite'
  /** 单一基础价（无阶梯或分组关闭阶梯时展示）。 */
  flat: string
  /** 某一档的单价。 */
  tier: (iv: UserPricingInterval) => string
}

/** 四个 token 价格列按同一顺序渲染：输入 / 输出 / 缓存读取 / 缓存写入。 */
const tokenColumns = computed<TokenColumn[]>(() => [
  { key: 'input', flat: priceValues.value.input, tier: (iv) => formatScaled(iv.input_price, perMillionScale) },
  { key: 'output', flat: priceValues.value.output, tier: (iv) => formatScaled(iv.output_price, perMillionScale) },
  { key: 'cacheRead', flat: priceValues.value.cacheRead, tier: (iv) => formatScaled(iv.cache_read_price, perMillionScale) },
  { key: 'cacheWrite', flat: priceValues.value.cacheWrite, tier: (iv) => formatScaled(iv.cache_write_price, perMillionScale) },
])

function formatCacheWrite(pricing: NonNullable<UserSupportedModel['pricing']>): string {
  const write5m = pricing.cache_write_5m_price
  const write1h = pricing.cache_write_1h_price
  if (write5m != null || write1h != null) {
    const fallback = pricing.cache_write_price
    return `5m ${formatScaled(write5m ?? fallback, perMillionScale)} / 1h ${formatScaled(write1h ?? fallback, perMillionScale)}`
  }
  return formatScaled(pricing.cache_write_price, perMillionScale)
}

function formatPrice(value: number | null, scale: number, unit: string): string {
  if (value == null) return '-'
  return `${formatScaled(value, scale)} ${unit}`
}
</script>
