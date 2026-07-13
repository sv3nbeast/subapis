<template>
  <div class="min-h-screen overflow-hidden bg-gradient-to-br from-gray-50 via-primary-50/25 to-cyan-50/20 text-gray-950 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950 dark:text-white">
    <div class="pointer-events-none fixed inset-0" aria-hidden="true">
      <div class="absolute -left-40 top-20 h-80 w-80 rounded-full bg-primary-300/10 blur-3xl dark:bg-primary-700/10"></div>
      <div class="absolute -right-32 top-44 h-72 w-72 rounded-full bg-cyan-300/10 blur-3xl dark:bg-cyan-700/10"></div>
    </div>

    <header class="sticky top-0 z-40 border-b border-white/50 bg-white/80 px-4 py-2.5 backdrop-blur-xl dark:border-dark-700/60 dark:bg-dark-950/80 sm:px-6">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-4">
        <RouterLink to="/home" class="flex min-w-0 items-center gap-2.5">
          <span class="h-8 w-8 overflow-hidden rounded-lg bg-white shadow-sm ring-1 ring-gray-200/70 dark:bg-dark-800 dark:ring-dark-700">
            <img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" class="h-full w-full object-contain" />
          </span>
          <span class="truncate text-base font-black tracking-tight">{{ siteName }}</span>
        </RouterLink>

        <div class="flex items-center gap-1.5 sm:gap-2.5">
          <RouterLink to="/home" class="hidden rounded-lg px-3 py-1.5 text-sm font-semibold text-gray-600 hover:bg-white hover:text-primary-700 dark:text-gray-300 dark:hover:bg-dark-800 dark:hover:text-primary-300 sm:inline-flex">
            {{ t('modelMarket.home') }}
          </RouterLink>
          <RouterLink to="/docs" class="hidden rounded-lg px-3 py-1.5 text-sm font-semibold text-gray-600 hover:bg-white hover:text-primary-700 dark:text-gray-300 dark:hover:bg-dark-800 dark:hover:text-primary-300 sm:inline-flex">
            {{ t('home.guide') }}
          </RouterLink>
          <LocaleSwitcher />
          <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-white hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-800 dark:hover:text-white" :title="isDark ? t('home.switchToLight') : t('home.switchToDark')" @click="toggleTheme">
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
          </button>
          <RouterLink :to="isAuthenticated ? dashboardPath : '/login'" class="btn btn-primary px-3.5 py-1.5 text-sm">
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </RouterLink>
        </div>
      </nav>
    </header>

    <main class="relative z-10 mx-auto max-w-7xl px-4 pb-16 pt-7 sm:px-6 lg:pt-9">
      <section class="flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
        <div>
          <div class="inline-flex items-center gap-1.5 rounded-full border border-primary-200 bg-primary-50/85 px-2.5 py-1 text-[11px] font-bold text-primary-700 dark:border-primary-700/60 dark:bg-primary-950/40 dark:text-primary-300">
            <Icon name="sparkles" size="sm" />
            {{ t('modelMarket.eyebrow') }}
          </div>
          <h1 class="mt-3 text-3xl font-black tracking-tight sm:text-4xl">{{ t('modelMarket.title') }}</h1>
          <p class="mt-2 max-w-3xl text-sm leading-6 text-gray-600 dark:text-gray-300 sm:text-base">{{ t('modelMarket.description') }}</p>
        </div>
        <div class="flex flex-wrap gap-2">
          <RouterLink v-if="isAuthenticated" to="/available-channels" class="btn btn-primary px-4 py-2 text-sm">
            {{ t('modelMarket.actions.myGroups') }}
            <Icon name="arrowRight" size="sm" />
          </RouterLink>
          <template v-else>
            <RouterLink to="/login" class="btn btn-primary px-4 py-2 text-sm">{{ t('modelMarket.actions.login') }}</RouterLink>
            <RouterLink to="/register" class="btn btn-secondary px-4 py-2 text-sm">{{ t('modelMarket.actions.register') }}</RouterLink>
          </template>
        </div>
      </section>

      <section class="mt-5 flex flex-wrap items-center gap-2 text-xs font-semibold text-gray-600 dark:text-gray-300">
        <span class="rounded-full border border-gray-200 bg-white/80 px-3 py-1.5 dark:border-dark-700 dark:bg-dark-800/80">
          {{ t('modelMarket.stats.models') }} <b class="ml-1 text-gray-950 dark:text-white">{{ modelEntries.length }}</b>
        </span>
        <span class="rounded-full border border-gray-200 bg-white/80 px-3 py-1.5 dark:border-dark-700 dark:bg-dark-800/80">
          {{ t('modelMarket.stats.groups') }} <b class="ml-1 text-gray-950 dark:text-white">{{ groups.length }}</b>
        </span>
        <span class="rounded-full border border-gray-200 bg-white/80 px-3 py-1.5 dark:border-dark-700 dark:bg-dark-800/80">
          {{ t('modelMarket.stats.families') }} <b class="ml-1 text-gray-950 dark:text-white">{{ familyOptions.length }}</b>
        </span>
      </section>

      <section class="sticky top-[53px] z-30 mt-4 rounded-xl border border-white/70 bg-white/90 p-3 shadow-sm backdrop-blur-xl dark:border-dark-700 dark:bg-dark-800/90">
        <div class="grid gap-2.5 lg:grid-cols-[minmax(18rem,1fr)_12rem_12rem_auto]">
          <div class="relative">
            <Icon name="search" size="md" class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input v-model="searchQuery" class="input h-10 pl-10" :placeholder="t('modelMarket.filters.search')" />
          </div>
          <select v-model="familyFilter" class="input h-10">
            <option value="">{{ t('modelMarket.filters.allFamilies') }}</option>
            <option v-for="family in familyOptions" :key="family" :value="family">{{ familyLabel(family) }}</option>
          </select>
          <select v-model="billingFilter" class="input h-10">
            <option value="">{{ t('modelMarket.filters.allBillingModes') }}</option>
            <option value="token">{{ t('modelMarket.billing.token') }}</option>
            <option value="per_request">{{ t('modelMarket.billing.perRequest') }}</option>
            <option value="image">{{ t('modelMarket.billing.image') }}</option>
          </select>
          <button type="button" class="btn btn-secondary h-10 px-3" :disabled="loading" @click="loadCatalog">
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            <span class="hidden sm:inline">{{ t('common.refresh') }}</span>
          </button>
        </div>
        <p class="mt-2 text-[11px] leading-5 text-gray-500 dark:text-gray-400">
          {{ t('modelMarket.referencePriceHint', { reference: formatRateNumber(referenceRate), settlement: formatRateNumber(settlementRate) }) }}
        </p>
      </section>

      <section v-if="loadError" class="mt-4 flex items-center gap-3 rounded-xl border border-red-200 bg-red-50 p-3 text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
        <Icon name="exclamationTriangle" size="md" />
        <p class="flex-1 text-sm font-medium">{{ loadError }}</p>
        <button class="btn btn-secondary text-sm" @click="loadCatalog">{{ t('modelMarket.retry') }}</button>
      </section>

      <section v-else-if="loading && modelEntries.length === 0" class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        <div v-for="idx in 8" :key="idx" class="h-48 animate-pulse rounded-xl border border-gray-200 bg-white/70 dark:border-dark-700 dark:bg-dark-800/70"></div>
      </section>

      <section v-else-if="filteredModels.length === 0" class="mt-4 rounded-xl border border-dashed border-gray-300 bg-white/70 p-10 text-center dark:border-dark-700 dark:bg-dark-800/65">
        <Icon name="inbox" size="xl" class="mx-auto text-gray-400" />
        <h2 class="mt-3 text-lg font-bold">{{ t('modelMarket.empty.title') }}</h2>
        <p class="mt-1.5 text-sm text-gray-500 dark:text-gray-400">{{ t('modelMarket.empty.description') }}</p>
      </section>

      <section v-else class="mt-4 grid items-start gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        <article v-for="entry in filteredModels" :key="entry.key" class="overflow-hidden rounded-xl border border-gray-200 bg-white/95 shadow-sm transition hover:-translate-y-0.5 hover:shadow-md dark:border-dark-700 dark:bg-dark-800/95">
          <div class="h-0.5" :class="platformAccentBarClass(familyStylePlatform(entry.family))"></div>
          <div class="p-3.5">
            <div class="flex items-start gap-2.5">
              <div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-gray-50 ring-1 ring-gray-100 dark:bg-dark-900 dark:ring-dark-700">
                <ModelIcon :model="entry.name" size="21px" />
              </div>
              <div class="min-w-0 flex-1">
                <h2 class="truncate text-sm font-black" :title="entry.name">{{ entry.name }}</h2>
                <div class="mt-1 flex flex-wrap items-center gap-1">
                  <span class="rounded border px-1.5 py-0.5 text-[10px] font-bold" :class="platformBadgeClass(familyStylePlatform(entry.family))">{{ familyLabel(entry.family) }}</span>
                  <span class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-semibold text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ billingModeLabel(entry.billingMode) }}</span>
                </div>
              </div>
              <span v-if="entrySavings(entry) !== null" class="shrink-0 rounded-full bg-emerald-50 px-2 py-1 text-[10px] font-black text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300" :title="t('modelMarket.savingsHint', { reference: formatRateNumber(referenceRate), settlement: formatRateNumber(settlementRate) })">
                {{ t('modelMarket.maxSavings', { percent: entrySavings(entry) }) }}
              </span>
            </div>

            <div class="mt-3 grid grid-cols-2 divide-x divide-gray-100 border-y border-gray-100 py-2.5 dark:divide-dark-700 dark:border-dark-700">
              <div v-for="(price, index) in priceSummary(entry)" :key="price.label" :class="index === 0 ? 'pr-2' : 'pl-2'">
                <p class="text-[10px] font-semibold text-gray-500 dark:text-gray-400">{{ price.label }}</p>
                <p class="mt-0.5 truncate font-mono text-sm font-black text-gray-950 dark:text-white">{{ price.value }}</p>
                <p v-if="price.official" class="mt-0.5 truncate text-[10px] text-gray-400 dark:text-gray-500">{{ t('modelMarket.officialShort', { price: price.official }) }}</p>
              </div>
            </div>

            <div class="mt-2.5 flex items-center justify-between gap-2 text-[11px]">
              <span class="font-bold text-primary-700 dark:text-primary-300">{{ rateRangeLabel(entry) }}</span>
              <span class="text-gray-500 dark:text-gray-400">{{ t('modelMarket.groupOffers', { count: entry.offers.length }) }}</span>
            </div>

            <button type="button" class="mt-2.5 flex w-full items-center justify-center gap-1.5 rounded-lg border border-gray-200 px-2.5 py-1.5 text-xs font-bold text-gray-700 hover:border-primary-200 hover:text-primary-700 dark:border-dark-700 dark:text-gray-200 dark:hover:border-primary-500/40 dark:hover:text-primary-300" @click="toggleExpanded(entry.key)">
              <span>{{ expandedKeys.has(entry.key) ? t('modelMarket.collapseDetails') : t('modelMarket.viewDetails') }}</span>
              <Icon :name="expandedKeys.has(entry.key) ? 'chevronUp' : 'chevronDown'" size="sm" />
            </button>

            <div v-if="expandedKeys.has(entry.key)" class="mt-2.5 space-y-2 border-t border-gray-100 pt-2.5 dark:border-dark-700">
              <div v-for="(offer, index) in entry.offers" :key="`${entry.key}-${offer.group.name}-${index}`" class="rounded-lg border border-gray-100 bg-gray-50/90 p-2.5 dark:border-dark-700 dark:bg-dark-900/65">
                <div class="border-b border-gray-200/80 pb-2 dark:border-dark-700">
                  <p
                    class="break-words text-xs font-black leading-[1.125rem] [overflow-wrap:anywhere]"
                    data-testid="model-market-group-name"
                    :title="offer.group.name"
                  >
                    {{ offer.group.name }}
                  </p>
                  <div class="mt-1.5 flex flex-wrap items-center gap-1">
                    <span class="rounded-full bg-white px-1.5 py-0.5 text-[9px] font-semibold text-gray-500 ring-1 ring-inset ring-gray-200 dark:bg-dark-800 dark:text-gray-400 dark:ring-dark-700">
                      {{ subscriptionLabel(offer.group.subscription_type) }}
                    </span>
                    <span class="rounded-full bg-primary-50 px-2 py-0.5 text-[10px] font-black text-primary-700 dark:bg-primary-500/15 dark:text-primary-300">
                      {{ t('modelMarket.billingRate', { rate: formatRate(offer.group.rate_multiplier) }) }}
                    </span>
                    <span v-if="offerSavings(offer) !== null" class="rounded-full bg-emerald-50 px-1.5 py-0.5 text-[9px] font-black text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
                      {{ t('modelMarket.saves', { percent: offerSavings(offer) }) }}
                    </span>
                  </div>
                </div>
                <p class="mt-2 text-[11px] font-bold leading-5 text-gray-800 dark:text-gray-200">{{ offerPriceLine(offer) }}</p>
                <p class="text-[10px] leading-4 text-gray-400 dark:text-gray-500">{{ offerOfficialLine(offer) }}</p>
                <p v-if="offerCacheLine(offer)" class="mt-1 text-[10px] leading-4 text-gray-500 dark:text-gray-400">{{ offerCacheLine(offer) }}</p>
                <p v-if="offer.group.peak_rate_enabled" class="mt-1.5 text-[10px] font-semibold leading-4 text-amber-600 dark:text-amber-300">
                  {{ peakRateSummary(offer.group) }}
                </p>
                <div v-if="offer.model.pricing?.intervals?.length" class="mt-2 border-t border-gray-200 pt-1.5 text-[10px] dark:border-dark-700">
                  <p class="font-bold text-gray-600 dark:text-gray-300">{{ t('modelMarket.tieredPricing') }}</p>
                  <p v-for="(tier, tierIndex) in offer.model.pricing.intervals" :key="tierIndex" class="mt-1 flex justify-between gap-2 text-gray-500 dark:text-gray-400">
                    <span>{{ tier.tier_label || formatRange(tier.min_tokens, tier.max_tokens) }}</span>
                    <span class="font-mono">{{ formatTier(tier, offer.model.pricing.billing_mode, offer.group) }}</span>
                  </p>
                </div>
              </div>
            </div>
          </div>
        </article>
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import publicModelsAPI, { type PublicModel, type PublicModelFamily, type PublicModelGroup } from '@/api/publicModels'
import type { UserPricingInterval } from '@/api/channels'
import { useAppStore, useAuthStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { normalizeSiteName } from '@/utils/siteBrand'
import { sanitizeUrl } from '@/utils/url'
import { platformAccentBarClass, platformBadgeClass } from '@/utils/platformColors'

interface ModelOffer { group: PublicModelGroup; model: PublicModel }
interface ModelEntry { key: string; name: string; family: PublicModelFamily; billingMode: string; offers: ModelOffer[] }
interface PricePair { actual: number | null; official: number | null }
interface PriceSummaryItem { label: string; value: string; official?: string }

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const groups = ref<PublicModelGroup[]>([])
const loading = ref(false)
const loadError = ref('')
const searchQuery = ref('')
const familyFilter = ref('')
const billingFilter = ref('')
const expandedKeys = ref(new Set<string>())
const isDark = ref(document.documentElement.classList.contains('dark'))

const siteName = computed(() => normalizeSiteName(appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API'))
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => authStore.isAdmin ? '/admin/dashboard' : '/dashboard')
const referenceRate = computed(() => positiveRate(appStore.cachedPublicSettings?.public_model_market_reference_usd_cny_rate, 7.2))
const settlementRate = computed(() => positiveRate(appStore.cachedPublicSettings?.public_model_market_settlement_usd_cny_rate, 1))

const modelEntries = computed<ModelEntry[]>(() => {
  const map = new Map<string, ModelEntry>()
  for (const group of groups.value) {
    for (const model of group.models) {
      const key = `${model.family}:${model.name.toLowerCase()}`
      let entry = map.get(key)
      if (!entry) {
        entry = { key, name: model.name, family: model.family, billingMode: model.pricing?.billing_mode || '', offers: [] }
        map.set(key, entry)
      } else if (!entry.billingMode && model.pricing?.billing_mode) {
        entry.billingMode = model.pricing.billing_mode
      }
      entry.offers.push({ group, model })
    }
  }
  return [...map.values()]
    .map((entry) => ({ ...entry, offers: [...entry.offers].sort((a, b) => a.group.name.localeCompare(b.group.name)) }))
    .sort((a, b) => a.name.localeCompare(b.name))
})

const familyOptions = computed<PublicModelFamily[]>(() => [...new Set(modelEntries.value.map((entry) => entry.family))].sort((a, b) => familyLabel(a).localeCompare(familyLabel(b))))
const filteredModels = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return modelEntries.value.filter((entry) => {
    if (familyFilter.value && entry.family !== familyFilter.value) return false
    if (billingFilter.value && !entry.offers.some((offer) => offer.model.pricing?.billing_mode === billingFilter.value)) return false
    if (!query) return true
    return entry.name.toLowerCase().includes(query)
      || familyLabel(entry.family).toLowerCase().includes(query)
      || entry.offers.some((offer) => offer.group.name.toLowerCase().includes(query))
  })
})

function positiveRate(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : fallback
}

function familyLabel(family: string): string {
  if (family === 'claude') return 'Claude'
  if (family === 'openai') return 'GPT / OpenAI'
  if (family === 'gemini') return 'Gemini'
  if (family === 'grok') return 'Grok'
  return t('modelMarket.families.other')
}

function familyStylePlatform(family: string): string {
  if (family === 'claude') return 'anthropic'
  if (family === 'openai') return 'openai'
  if (family === 'gemini') return 'gemini'
  if (family === 'grok') return 'grok'
  return ''
}

function billingModeLabel(mode: string): string {
  if (mode === 'per_request') return t('modelMarket.billing.perRequest')
  if (mode === 'image') return t('modelMarket.billing.image')
  if (mode === 'token') return t('modelMarket.billing.token')
  return t('modelMarket.billing.unconfigured')
}

function normalizedGroupRate(rate: number): number {
  return Number.isFinite(rate) && rate >= 0 ? rate : 1
}

function displayFactor(group: PublicModelGroup, peak = false): number {
  const peakFactor = peak && group.peak_rate_enabled ? positiveRate(group.peak_rate_multiplier, 1) : 1
  return normalizedGroupRate(group.rate_multiplier) * peakFactor * settlementRate.value / referenceRate.value
}

function bestPrice(entry: ModelEntry, selector: (model: PublicModel) => number | null, scale: number): PricePair {
  let best: PricePair = { actual: null, official: null }
  for (const offer of entry.offers) {
    const raw = selector(offer.model)
    if (raw == null || !Number.isFinite(raw)) continue
    const official = raw * scale
    const actual = official * displayFactor(offer.group)
    if (best.actual == null || actual < best.actual) best = { actual, official }
  }
  return best
}

function priceItem(label: string, pair: PricePair): PriceSummaryItem | null {
  if (pair.actual == null) return null
  return { label, value: formatUSD(pair.actual), official: pair.official == null ? undefined : formatUSD(pair.official) }
}

function priceSummary(entry: ModelEntry): PriceSummaryItem[] {
  const selector = (field: keyof NonNullable<PublicModel['pricing']>) => (model: PublicModel): number | null => {
    const value = model.pricing?.[field]
    return typeof value === 'number' ? value : null
  }
  let items: Array<PriceSummaryItem | null>
  if (entry.billingMode === 'per_request') {
    items = [priceItem(t('modelMarket.prices.perRequest'), bestPrice(entry, selector('per_request_price'), 1))]
  } else if (entry.billingMode === 'image') {
    items = [priceItem(t('modelMarket.prices.perImage'), bestPrice(entry, selector('image_output_price'), 1))]
  } else {
    items = [
      priceItem(t('modelMarket.prices.input'), bestPrice(entry, selector('input_price'), 1_000_000)),
      priceItem(t('modelMarket.prices.output'), bestPrice(entry, selector('output_price'), 1_000_000)),
    ]
  }
  const configured = items.filter((item): item is PriceSummaryItem => item !== null)
  if (configured.length === 0) return [{ label: t('modelMarket.prices.status'), value: t('modelMarket.billing.unconfigured') }]
  if (configured.length === 1) configured.push({ label: t('modelMarket.prices.status'), value: t('modelMarket.usdEquivalent') })
  return configured.slice(0, 2)
}

function formatUSD(value: number | null): string {
  if (value == null || !Number.isFinite(value)) return '-'
  const abs = Math.abs(value)
  const maximumFractionDigits = abs >= 0.1 ? 2 : abs >= 0.001 ? 4 : 6
  return `$${value.toLocaleString(undefined, { minimumFractionDigits: 0, maximumFractionDigits })}`
}

function formatRateNumber(value: number): string {
  return value.toLocaleString(undefined, { minimumFractionDigits: value % 1 === 0 ? 1 : 0, maximumFractionDigits: 2 })
}

function formatRate(rate: number): string {
  const value = normalizedGroupRate(rate)
  return `${value.toLocaleString(undefined, { minimumFractionDigits: value % 1 === 0 ? 1 : 0, maximumFractionDigits: 2 })}×`
}

function rateRangeLabel(entry: ModelEntry): string {
  const rates = entry.offers.map((offer) => normalizedGroupRate(offer.group.rate_multiplier))
  const min = Math.min(...rates)
  const max = Math.max(...rates)
  return min === max
    ? t('modelMarket.billingRate', { rate: formatRate(min) })
    : t('modelMarket.billingRateRange', { min: formatRate(min), max: formatRate(max) })
}

function savingsForRate(rate: number): number | null {
  const saving = (1 - normalizedGroupRate(rate) * settlementRate.value / referenceRate.value) * 100
  if (!Number.isFinite(saving) || saving <= 0) return null
  return Math.min(100, Math.round(saving))
}

function modelHasComparablePrice(model: PublicModel): boolean {
  const pricing = model.pricing
  return Boolean(pricing && [pricing.input_price, pricing.output_price, pricing.per_request_price, pricing.image_output_price].some((value) => value != null))
}

function offerSavings(offer: ModelOffer): number | null {
  return modelHasComparablePrice(offer.model) ? savingsForRate(offer.group.rate_multiplier) : null
}

function entrySavings(entry: ModelEntry): number | null {
  const values = entry.offers.map((offer) => offerSavings(offer)).filter((value): value is number => value !== null)
  return values.length ? Math.max(...values) : null
}

function subscriptionLabel(type: string): string {
  return type === 'subscription' ? t('modelMarket.subscription') : t('modelMarket.payAsYouGo')
}

function adjustedPrice(value: number | null, scale: number, group: PublicModelGroup, peak = false): string {
  return value == null ? '-' : formatUSD(value * scale * displayFactor(group, peak))
}

function officialPrice(value: number | null, scale: number): string {
  return value == null ? '-' : formatUSD(value * scale)
}

function offerPriceLine(offer: ModelOffer): string {
  const pricing = offer.model.pricing
  if (!pricing) return t('modelMarket.billing.unconfigured')
  if (pricing.billing_mode === 'per_request') return t('modelMarket.offerPrice.perRequest', { price: adjustedPrice(pricing.per_request_price, 1, offer.group) })
  if (pricing.billing_mode === 'image') return t('modelMarket.offerPrice.perImage', { price: adjustedPrice(pricing.image_output_price, 1, offer.group) })
  return t('modelMarket.offerPrice.token', {
    input: adjustedPrice(pricing.input_price, 1_000_000, offer.group),
    output: adjustedPrice(pricing.output_price, 1_000_000, offer.group),
  })
}

function offerOfficialLine(offer: ModelOffer): string {
  const pricing = offer.model.pricing
  if (!pricing) return ''
  if (pricing.billing_mode === 'per_request') return t('modelMarket.officialPrice.perRequest', { price: officialPrice(pricing.per_request_price, 1) })
  if (pricing.billing_mode === 'image') return t('modelMarket.officialPrice.perImage', { price: officialPrice(pricing.image_output_price, 1) })
  return t('modelMarket.officialPrice.token', {
    input: officialPrice(pricing.input_price, 1_000_000),
    output: officialPrice(pricing.output_price, 1_000_000),
  })
}

function offerCacheLine(offer: ModelOffer): string {
  const pricing = offer.model.pricing
  if (!pricing || pricing.billing_mode !== 'token') return ''
  const parts: string[] = []
  if (pricing.cache_read_price != null) parts.push(t('modelMarket.cache.read', { price: adjustedPrice(pricing.cache_read_price, 1_000_000, offer.group) }))
  if (pricing.cache_write_price != null) parts.push(t('modelMarket.cache.write', { price: adjustedPrice(pricing.cache_write_price, 1_000_000, offer.group) }))
  return parts.join(' · ')
}

function peakRateSummary(group: PublicModelGroup): string {
  const window = formatPeakRateWindow(group, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
  const effective = normalizedGroupRate(group.rate_multiplier) * positiveRate(group.peak_rate_multiplier, 1)
  return t('modelMarket.peakRateEffective', { window, rate: formatRate(effective) })
}

function formatRange(min: number, max: number | null): string {
  return max == null ? `${min.toLocaleString()}+` : `${min.toLocaleString()}–${max.toLocaleString()}`
}

function formatTier(tier: UserPricingInterval, mode: string, group: PublicModelGroup): string {
  if (mode === 'per_request' || mode === 'image') return `${adjustedPrice(tier.per_request_price, 1, group)} / request`
  return `${adjustedPrice(tier.input_price, 1_000_000, group)} / ${adjustedPrice(tier.output_price, 1_000_000, group)}`
}

function toggleExpanded(key: string) {
  const next = new Set(expandedKeys.value)
  next.has(key) ? next.delete(key) : next.add(key)
  expandedKeys.value = next
}

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

async function loadCatalog() {
  loading.value = true
  loadError.value = ''
  try {
    groups.value = (await publicModelsAPI.getPublicModels()).groups
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, t('modelMarket.loadError'))
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  authStore.checkAuth()
  void loadCatalog()
})
</script>
