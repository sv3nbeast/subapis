<template>
  <div class="space-y-5">
    <div v-if="loading" class="grid gap-5 xl:grid-cols-2">
      <div
        v-for="idx in 4"
        :key="idx"
        class="overflow-hidden rounded-3xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800"
      >
        <div class="flex animate-pulse items-start gap-3">
          <div class="h-12 w-12 rounded-2xl bg-gray-100 dark:bg-dark-700"></div>
          <div class="flex-1 space-y-3">
            <div class="h-4 w-40 rounded bg-gray-100 dark:bg-dark-700"></div>
            <div class="h-3 w-64 rounded bg-gray-100 dark:bg-dark-700"></div>
          </div>
        </div>
        <div class="mt-5 space-y-3">
          <div class="h-16 animate-pulse rounded-2xl bg-gray-50 dark:bg-dark-900/60"></div>
          <div class="h-16 animate-pulse rounded-2xl bg-gray-50 dark:bg-dark-900/60"></div>
        </div>
      </div>
    </div>

    <div v-else-if="groups.length === 0" class="rounded-3xl border border-dashed border-gray-200 bg-white/80 p-12 text-center dark:border-dark-700 dark:bg-dark-800/70">
      <div class="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-gray-100 text-gray-400 dark:bg-dark-700 dark:text-gray-500">
        <Icon name="inbox" size="xl" />
      </div>
      <p class="text-sm font-medium text-gray-600 dark:text-gray-300">{{ emptyLabel }}</p>
    </div>

    <div v-else class="grid gap-5 xl:grid-cols-2">
      <article
        v-for="entry in groups"
        :key="entry.group.id"
        class="available-group-card group relative overflow-hidden rounded-3xl border bg-white shadow-sm transition-all duration-300 hover:-translate-y-1 hover:shadow-xl dark:bg-dark-800"
        :class="platformBorderClass(entry.group.platform)"
      >
        <div :class="['h-1.5', platformAccentBarClass(entry.group.platform)]"></div>
        <div class="space-y-5 p-5">
          <header class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <div
                  :class="[
                    'flex h-10 w-10 items-center justify-center rounded-2xl',
                    platformIconBgClass(entry.group.platform),
                  ]"
                >
                  <PlatformIcon :platform="entry.group.platform as GroupPlatform" size="md" />
                </div>
                <div class="min-w-0">
                  <h3 class="truncate text-base font-bold text-gray-950 dark:text-white">
                    {{ entry.group.name }}
                  </h3>
                  <div class="mt-1 flex flex-wrap items-center gap-1.5">
                    <span
                      :class="[
                        'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-semibold',
                        platformBadgeClass(entry.group.platform),
                      ]"
                    >
                      {{ platformLabel(entry.group.platform) }}
                    </span>
                    <span
                      :class="[
                        'inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-semibold',
                        entry.group.is_exclusive
                          ? 'bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300'
                          : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300',
                      ]"
                    >
                      <Icon :name="entry.group.is_exclusive ? 'shield' : 'globe'" size="xs" />
                      {{ entry.group.is_exclusive ? t('availableChannels.exclusive') : t('availableChannels.public') }}
                    </span>
                    <span class="inline-flex items-center rounded-md bg-primary-100 px-2 py-0.5 text-[11px] font-bold text-primary-700 dark:bg-primary-500/15 dark:text-primary-300">
                      {{ effectiveRate(entry.group) }}x
                    </span>
                  </div>
                </div>
              </div>
              <p class="mt-3 text-sm leading-6 text-gray-500 dark:text-gray-400">
                {{ t('availableChannels.groupCards.groupHint', {
                  platform: platformLabel(entry.group.platform),
                  rate: `${effectiveRate(entry.group)}x`,
                }) }}
              </p>
            </div>

            <div class="grid min-w-[11rem] grid-cols-2 gap-2 text-center">
              <div class="rounded-2xl bg-gray-50 px-3 py-2 dark:bg-dark-900/70">
                <div class="text-lg font-black text-gray-950 dark:text-white">{{ entry.channelCount }}</div>
                <div class="text-[10px] font-medium text-gray-400 dark:text-gray-500">
                  {{ t('availableChannels.groupCards.sourceCount') }}
                </div>
              </div>
              <div class="rounded-2xl bg-gray-50 px-3 py-2 dark:bg-dark-900/70">
                <div class="text-lg font-black text-gray-950 dark:text-white">{{ entry.modelCount }}</div>
                <div class="text-[10px] font-medium text-gray-400 dark:text-gray-500">
                  {{ t('availableChannels.groupCards.modelCount') }}
                </div>
              </div>
            </div>
          </header>

          <div class="space-y-4">
            <section
              v-for="channel in entry.channels"
              :key="`${entry.group.id}-${channel.platform}-${channel.channelName}`"
              class="rounded-2xl border border-gray-100 bg-gray-50/80 p-4 dark:border-dark-700 dark:bg-dark-900/35"
            >
              <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="h-2 w-2 rounded-full bg-emerald-400 shadow-[0_0_0_4px_rgba(52,211,153,0.14)]"></span>
                    <h4 class="truncate text-sm font-semibold text-gray-900 dark:text-white">
                      {{ channel.channelName }}
                    </h4>
                    <span class="text-[11px] text-gray-400 dark:text-gray-500">
                      {{ t('availableChannels.groupCards.serviceSource') }}
                    </span>
                  </div>
                  <p
                    v-if="channel.channelDescription"
                    class="mt-1 line-clamp-2 text-xs leading-5 text-gray-500 dark:text-gray-400"
                  >
                    {{ channel.channelDescription }}
                  </p>
                </div>
                <RouterLink
                  to="/channel-status"
                  class="inline-flex shrink-0 items-center gap-1 text-xs font-semibold text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-300 dark:hover:text-primary-200"
                >
                  {{ t('availableChannels.groupCards.viewStatus') }}
                  <Icon name="arrowRight" size="xs" />
                </RouterLink>
              </div>

              <div class="mt-4 space-y-2">
                <SupportedModelPriceRow
                  v-for="model in channel.supportedModels"
                  :key="`${entry.group.id}-${channel.channelName}-${model.platform}-${model.name}`"
                  :model="model"
                  :no-pricing-label="noPricingLabel"
                />
                <div
                  v-if="channel.supportedModels.length === 0"
                  class="rounded-xl border border-dashed border-gray-200 bg-white/70 px-3 py-4 text-center text-xs text-gray-400 dark:border-dark-700 dark:bg-dark-800/60 dark:text-gray-500"
                >
                  {{ noModelsLabel }}
                </div>
              </div>
            </section>
          </div>
        </div>
      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { GroupPlatform } from '@/types'
import type { UserAvailableGroup } from '@/api/channels'
import {
  platformAccentBarClass,
  platformBadgeClass,
  platformBorderClass,
  platformLabel,
} from '@/utils/platformColors'
import SupportedModelPriceRow from './SupportedModelPriceRow.vue'
import type { AvailableGroupView } from './availableGroupTypes'

const props = defineProps<{
  groups: AvailableGroupView[]
  loading: boolean
  emptyLabel: string
  noModelsLabel: string
  noPricingLabel: string
  userGroupRates: Record<number, number>
}>()

const { t } = useI18n()

function effectiveRate(group: UserAvailableGroup): number {
  return props.userGroupRates[group.id] ?? group.rate_multiplier ?? 1
}

function platformIconBgClass(platform: string): string {
  switch (platform) {
    case 'anthropic':
      return 'bg-orange-100 text-orange-600 dark:bg-orange-500/15 dark:text-orange-300'
    case 'openai':
      return 'bg-emerald-100 text-emerald-600 dark:bg-emerald-500/15 dark:text-emerald-300'
    case 'gemini':
      return 'bg-blue-100 text-blue-600 dark:bg-blue-500/15 dark:text-blue-300'
    case 'antigravity':
      return 'bg-violet-100 text-violet-600 dark:bg-violet-500/15 dark:text-violet-300'
    default:
      return 'bg-primary-100 text-primary-600 dark:bg-primary-500/15 dark:text-primary-300'
  }
}
</script>

<style scoped>
.available-group-card::before {
  content: '';
  position: absolute;
  inset: 0;
  pointer-events: none;
  background:
    radial-gradient(circle at 12% 0%, rgba(20, 184, 166, 0.11), transparent 26%),
    linear-gradient(135deg, rgba(255, 255, 255, 0.48), transparent 34%);
  opacity: 0;
  transition: opacity 0.25s ease;
}

.available-group-card:hover::before {
  opacity: 1;
}
</style>
