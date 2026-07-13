<template>
  <div class="min-h-screen overflow-hidden bg-gradient-to-br from-gray-50 via-primary-50/35 to-cyan-50/25 text-gray-950 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950 dark:text-white">
    <div class="pointer-events-none fixed inset-0" aria-hidden="true">
      <div class="absolute -left-40 top-20 h-96 w-96 rounded-full bg-primary-300/15 blur-3xl dark:bg-primary-700/10"></div>
      <div class="absolute -right-32 top-48 h-80 w-80 rounded-full bg-cyan-300/15 blur-3xl dark:bg-cyan-700/10"></div>
    </div>

    <header class="sticky top-0 z-40 border-b border-white/50 bg-white/75 px-4 py-3 backdrop-blur-xl dark:border-dark-700/60 dark:bg-dark-950/75 sm:px-6">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-4">
        <RouterLink to="/home" class="flex min-w-0 items-center gap-2.5">
          <span class="h-9 w-9 overflow-hidden rounded-xl bg-white shadow-md ring-1 ring-gray-200/70 dark:bg-dark-800 dark:ring-dark-700">
            <img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" class="h-full w-full object-contain" />
          </span>
          <span class="truncate text-base font-black tracking-tight sm:text-lg">{{ siteName }}</span>
        </RouterLink>

        <div class="flex items-center gap-2 sm:gap-3">
          <RouterLink to="/home" class="hidden rounded-lg px-3 py-2 text-sm font-semibold text-gray-600 hover:bg-white hover:text-primary-700 dark:text-gray-300 dark:hover:bg-dark-800 dark:hover:text-primary-300 sm:inline-flex">
            {{ t('modelMarket.home') }}
          </RouterLink>
          <RouterLink to="/docs" class="hidden rounded-lg px-3 py-2 text-sm font-semibold text-gray-600 hover:bg-white hover:text-primary-700 dark:text-gray-300 dark:hover:bg-dark-800 dark:hover:text-primary-300 sm:inline-flex">
            {{ t('home.guide') }}
          </RouterLink>
          <LocaleSwitcher />
          <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-white hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-800 dark:hover:text-white" :title="isDark ? t('home.switchToLight') : t('home.switchToDark')" @click="toggleTheme">
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
          </button>
          <RouterLink :to="isAuthenticated ? dashboardPath : '/login'" class="btn btn-primary px-4 py-2 text-sm">
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </RouterLink>
        </div>
      </nav>
    </header>

    <main class="relative z-10 mx-auto max-w-7xl px-4 pb-20 pt-10 sm:px-6 lg:pt-14">
      <section class="grid items-end gap-6 lg:grid-cols-[minmax(0,1fr)_auto]">
        <div>
          <div class="inline-flex items-center gap-2 rounded-full border border-primary-200 bg-primary-50/85 px-3 py-1.5 text-xs font-bold text-primary-700 dark:border-primary-700/60 dark:bg-primary-950/40 dark:text-primary-300">
            <Icon name="sparkles" size="sm" />
            {{ t('modelMarket.eyebrow') }}
          </div>
          <h1 class="mt-4 text-3xl font-black tracking-tight sm:text-5xl">{{ t('modelMarket.title') }}</h1>
          <p class="mt-4 max-w-3xl text-base leading-7 text-gray-600 dark:text-gray-300">{{ t('modelMarket.description') }}</p>
        </div>
        <div class="flex flex-wrap gap-3">
          <RouterLink v-if="isAuthenticated" to="/available-channels" class="btn btn-primary px-5 py-2.5 text-sm">
            {{ t('modelMarket.actions.myGroups') }}
            <Icon name="arrowRight" size="sm" />
          </RouterLink>
          <template v-else>
            <RouterLink to="/login" class="btn btn-primary px-5 py-2.5 text-sm">{{ t('modelMarket.actions.login') }}</RouterLink>
            <RouterLink to="/register" class="btn btn-secondary px-5 py-2.5 text-sm">{{ t('modelMarket.actions.register') }}</RouterLink>
          </template>
        </div>
      </section>

      <section class="mt-8 grid gap-3 sm:grid-cols-3">
        <div class="rounded-2xl border border-white/70 bg-white/80 p-4 shadow-sm backdrop-blur dark:border-dark-700 dark:bg-dark-800/75">
          <p class="text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('modelMarket.stats.models') }}</p>
          <p class="mt-1 text-2xl font-black">{{ modelEntries.length }}</p>
        </div>
        <div class="rounded-2xl border border-white/70 bg-white/80 p-4 shadow-sm backdrop-blur dark:border-dark-700 dark:bg-dark-800/75">
          <p class="text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('modelMarket.stats.groups') }}</p>
          <p class="mt-1 text-2xl font-black">{{ groups.length }}</p>
        </div>
        <div class="rounded-2xl border border-white/70 bg-white/80 p-4 shadow-sm backdrop-blur dark:border-dark-700 dark:bg-dark-800/75">
          <p class="text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('modelMarket.stats.platforms') }}</p>
          <p class="mt-1 text-2xl font-black">{{ platformOptions.length }}</p>
        </div>
      </section>

      <section class="mt-6 rounded-2xl border border-white/70 bg-white/85 p-4 shadow-sm backdrop-blur dark:border-dark-700 dark:bg-dark-800/80">
        <div class="grid gap-3 lg:grid-cols-[minmax(18rem,1fr)_13rem_13rem_auto]">
          <div class="relative">
            <Icon name="search" size="md" class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input v-model="searchQuery" class="input pl-10" :placeholder="t('modelMarket.filters.search')" />
          </div>
          <select v-model="platformFilter" class="input">
            <option value="">{{ t('modelMarket.filters.allPlatforms') }}</option>
            <option v-for="platform in platformOptions" :key="platform" :value="platform">{{ platformLabel(platform) }}</option>
          </select>
          <select v-model="billingFilter" class="input">
            <option value="">{{ t('modelMarket.filters.allBillingModes') }}</option>
            <option value="token">{{ t('modelMarket.billing.token') }}</option>
            <option value="per_request">{{ t('modelMarket.billing.perRequest') }}</option>
            <option value="image">{{ t('modelMarket.billing.image') }}</option>
          </select>
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadCatalog">
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
        </div>
        <p class="mt-3 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('modelMarket.referencePriceHint') }}</p>
      </section>

      <section v-if="loadError" class="mt-6 flex items-center gap-3 rounded-2xl border border-red-200 bg-red-50 p-4 text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
        <Icon name="exclamationTriangle" size="md" />
        <p class="flex-1 text-sm font-medium">{{ loadError }}</p>
        <button class="btn btn-secondary text-sm" @click="loadCatalog">{{ t('modelMarket.retry') }}</button>
      </section>

      <section v-else-if="loading && modelEntries.length === 0" class="mt-6 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <div v-for="idx in 6" :key="idx" class="h-72 animate-pulse rounded-2xl border border-gray-200 bg-white/70 dark:border-dark-700 dark:bg-dark-800/70"></div>
      </section>

      <section v-else-if="filteredModels.length === 0" class="mt-6 rounded-2xl border border-dashed border-gray-300 bg-white/70 p-12 text-center dark:border-dark-700 dark:bg-dark-800/65">
        <Icon name="inbox" size="xl" class="mx-auto text-gray-400" />
        <h2 class="mt-4 text-lg font-bold">{{ t('modelMarket.empty.title') }}</h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('modelMarket.empty.description') }}</p>
      </section>

      <section v-else class="mt-6 grid items-start gap-4 md:grid-cols-2 xl:grid-cols-3">
        <article v-for="entry in filteredModels" :key="entry.key" class="overflow-hidden rounded-2xl border border-gray-200 bg-white/90 shadow-sm transition hover:-translate-y-0.5 hover:shadow-lg dark:border-dark-700 dark:bg-dark-800/90">
          <div class="h-1" :class="platformAccentBarClass(entry.platform)"></div>
          <div class="p-5">
            <div class="flex items-start gap-3">
              <div class="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-gray-50 ring-1 ring-gray-100 dark:bg-dark-900 dark:ring-dark-700">
                <ModelIcon :model="entry.name" size="24px" />
              </div>
              <div class="min-w-0 flex-1">
                <h2 class="truncate text-base font-black" :title="entry.name">{{ entry.name }}</h2>
                <div class="mt-1.5 flex flex-wrap items-center gap-1.5">
                  <span class="rounded-md border px-2 py-0.5 text-[11px] font-semibold" :class="platformBadgeClass(entry.platform)">{{ platformLabel(entry.platform) }}</span>
                  <span class="rounded-md bg-gray-100 px-2 py-0.5 text-[11px] font-semibold text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ billingModeLabel(entry.billingMode) }}</span>
                </div>
              </div>
            </div>

            <div class="mt-5 grid grid-cols-2 gap-2">
              <div v-for="price in priceSummary(entry)" :key="price.label" class="rounded-xl bg-gray-50 p-3 dark:bg-dark-900/60">
                <p class="text-[11px] font-medium text-gray-500 dark:text-gray-400">{{ price.label }}</p>
                <p class="mt-1 truncate font-mono text-sm font-bold">{{ price.value }}</p>
              </div>
            </div>

            <button type="button" class="mt-4 flex w-full items-center justify-between rounded-xl border border-gray-200 px-3 py-2.5 text-sm font-bold text-gray-700 hover:border-primary-200 hover:text-primary-700 dark:border-dark-700 dark:text-gray-200 dark:hover:border-primary-500/40 dark:hover:text-primary-300" @click="toggleExpanded(entry.key)">
              <span>{{ t('modelMarket.groupOffers', { count: entry.offers.length }) }}</span>
              <Icon :name="expandedKeys.has(entry.key) ? 'chevronUp' : 'chevronDown'" size="sm" />
            </button>

            <div v-if="expandedKeys.has(entry.key)" class="mt-3 space-y-2">
              <div v-for="offer in entry.offers" :key="`${entry.key}-${offer.group.name}`" class="rounded-xl border border-gray-100 p-3 dark:border-dark-700">
                <div class="flex items-center justify-between gap-3">
                  <div class="min-w-0">
                    <p class="truncate text-sm font-bold">{{ offer.group.name }}</p>
                    <p class="mt-0.5 text-[11px] text-gray-500 dark:text-gray-400">{{ subscriptionLabel(offer.group.subscription_type) }}</p>
                  </div>
                  <span class="shrink-0 rounded-full bg-primary-50 px-2.5 py-1 text-xs font-black text-primary-700 dark:bg-primary-500/15 dark:text-primary-300">{{ formatRate(offer.group.rate_multiplier) }}</span>
                </div>
                <p v-if="offer.group.peak_rate_enabled" class="mt-2 text-[11px] text-amber-600 dark:text-amber-300">
                  {{ t('modelMarket.peakRate', { window: peakRateWindow(offer.group) }) }}
                </p>
                <p class="mt-2 text-[11px] leading-5 text-gray-500 dark:text-gray-400">
                  {{ offerPriceLine(offer.model) }}
                </p>
                <div v-if="offer.model.pricing?.intervals?.length" class="mt-3 border-t border-gray-100 pt-2 text-[11px] dark:border-dark-700">
                  <p class="font-bold text-gray-600 dark:text-gray-300">{{ t('modelMarket.tieredPricing') }}</p>
                  <p v-for="(tier, index) in offer.model.pricing.intervals" :key="index" class="mt-1 flex justify-between gap-3 text-gray-500 dark:text-gray-400">
                    <span>{{ tier.tier_label || formatRange(tier.min_tokens, tier.max_tokens) }}</span>
                    <span class="font-mono">{{ formatTier(tier, offer.model.pricing.billing_mode) }}</span>
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
import publicModelsAPI, { type PublicModelGroup } from '@/api/publicModels'
import type { UserPricingInterval, UserSupportedModel } from '@/api/channels'
import { useAppStore, useAuthStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatScaled } from '@/utils/pricing'
import { formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { normalizeSiteName } from '@/utils/siteBrand'
import { sanitizeUrl } from '@/utils/url'
import { platformAccentBarClass, platformBadgeClass, platformLabel } from '@/utils/platformColors'

interface ModelOffer { group: PublicModelGroup; model: UserSupportedModel }
interface ModelEntry { key: string; name: string; platform: string; billingMode: string; offers: ModelOffer[] }

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const groups = ref<PublicModelGroup[]>([])
const loading = ref(false)
const loadError = ref('')
const searchQuery = ref('')
const platformFilter = ref('')
const billingFilter = ref('')
const expandedKeys = ref(new Set<string>())
const isDark = ref(document.documentElement.classList.contains('dark'))

const siteName = computed(() => normalizeSiteName(appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API'))
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => authStore.isAdmin ? '/admin/dashboard' : '/dashboard')

const modelEntries = computed<ModelEntry[]>(() => {
  const map = new Map<string, ModelEntry>()
  for (const group of groups.value) {
    for (const model of group.models) {
      const key = `${model.platform}:${model.name.toLowerCase()}`
      let entry = map.get(key)
      if (!entry) {
        entry = { key, name: model.name, platform: model.platform, billingMode: model.pricing?.billing_mode || '', offers: [] }
        map.set(key, entry)
      }
      entry.offers.push({ group, model })
    }
  }
  return [...map.values()].sort((a, b) => a.name.localeCompare(b.name))
})

const platformOptions = computed(() => [...new Set(modelEntries.value.map((entry) => entry.platform))].sort((a, b) => platformLabel(a).localeCompare(platformLabel(b))))
const filteredModels = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return modelEntries.value.filter((entry) => {
    if (platformFilter.value && entry.platform !== platformFilter.value) return false
    if (billingFilter.value && !entry.offers.some((offer) => offer.model.pricing?.billing_mode === billingFilter.value)) return false
    if (!query) return true
    return entry.name.toLowerCase().includes(query) || platformLabel(entry.platform).toLowerCase().includes(query) || entry.offers.some((offer) => offer.group.name.toLowerCase().includes(query))
  })
})

function billingModeLabel(mode: string): string {
  if (mode === 'per_request') return t('modelMarket.billing.perRequest')
  if (mode === 'image') return t('modelMarket.billing.image')
  if (mode === 'token') return t('modelMarket.billing.token')
  return t('modelMarket.billing.unconfigured')
}

function priceSummary(entry: ModelEntry) {
  const pricing = entry.offers.find((offer) => offer.model.pricing)?.model.pricing
  if (!pricing) return [{ label: t('modelMarket.prices.status'), value: t('modelMarket.billing.unconfigured') }]
  if (pricing.billing_mode === 'per_request') return [{ label: t('modelMarket.prices.perRequest'), value: formatScaled(pricing.per_request_price, 1) }]
  if (pricing.billing_mode === 'image') return [{ label: t('modelMarket.prices.perImage'), value: formatScaled(pricing.image_output_price, 1) }]
  return [
    { label: t('modelMarket.prices.input'), value: `${formatScaled(pricing.input_price, 1_000_000)} / 1M` },
    { label: t('modelMarket.prices.output'), value: `${formatScaled(pricing.output_price, 1_000_000)} / 1M` },
    { label: t('modelMarket.prices.cacheRead'), value: `${formatScaled(pricing.cache_read_price, 1_000_000)} / 1M` },
    { label: t('modelMarket.prices.cacheWrite'), value: `${formatScaled(pricing.cache_write_price, 1_000_000)} / 1M` },
  ]
}

function subscriptionLabel(type: string) { return type === 'subscription' ? t('modelMarket.subscription') : t('modelMarket.payAsYouGo') }
function formatRate(rate: number) { return `${Number.isFinite(rate) ? rate : 1}x` }
function peakRateWindow(group: PublicModelGroup) { return formatPeakRateWindow(group, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset)) }
function offerPriceLine(model: UserSupportedModel): string {
  const pricing = model.pricing
  if (!pricing) return t('modelMarket.billing.unconfigured')
  if (pricing.billing_mode === 'per_request') {
    return t('modelMarket.offerPrice.perRequest', { price: formatScaled(pricing.per_request_price, 1) })
  }
  if (pricing.billing_mode === 'image') {
    return t('modelMarket.offerPrice.perImage', { price: formatScaled(pricing.image_output_price, 1) })
  }
  return t('modelMarket.offerPrice.token', {
    input: formatScaled(pricing.input_price, 1_000_000),
    output: formatScaled(pricing.output_price, 1_000_000),
  })
}
function formatRange(min: number, max: number | null) { return max == null ? `${min.toLocaleString()}+` : `${min.toLocaleString()}–${max.toLocaleString()}` }
function formatTier(tier: UserPricingInterval, mode: string) {
  if (mode === 'per_request') return `${formatScaled(tier.per_request_price, 1)} / request`
  return `${formatScaled(tier.input_price, 1_000_000)} / ${formatScaled(tier.output_price, 1_000_000)}`
}
function toggleExpanded(key: string) { const next = new Set(expandedKeys.value); next.has(key) ? next.delete(key) : next.add(key); expandedKeys.value = next }
function toggleTheme() { isDark.value = !isDark.value; document.documentElement.classList.toggle('dark', isDark.value); localStorage.setItem('theme', isDark.value ? 'dark' : 'light') }

async function loadCatalog() {
  loading.value = true
  loadError.value = ''
  try { groups.value = (await publicModelsAPI.getPublicModels()).groups }
  catch (error) { loadError.value = extractApiErrorMessage(error, t('modelMarket.loadError')) }
  finally { loading.value = false }
}

onMounted(() => { authStore.checkAuth(); void loadCatalog() })
</script>
