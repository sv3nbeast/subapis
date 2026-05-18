<template>
  <div class="space-y-3">
    <div v-if="loading" class="space-y-3">
      <div
        v-for="idx in 5"
        :key="idx"
        class="rounded-2xl border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800"
      >
        <div class="flex animate-pulse items-center gap-3">
          <div class="h-9 w-9 rounded-xl bg-gray-100 dark:bg-dark-700"></div>
          <div class="flex-1 space-y-2">
            <div class="h-4 w-44 rounded bg-gray-100 dark:bg-dark-700"></div>
            <div class="h-3 w-72 rounded bg-gray-100 dark:bg-dark-700"></div>
          </div>
          <div class="hidden h-8 w-24 rounded-lg bg-gray-100 dark:bg-dark-700 sm:block"></div>
        </div>
      </div>
    </div>

    <div v-else-if="groups.length === 0" class="rounded-2xl border border-dashed border-gray-200 bg-white/80 p-10 text-center dark:border-dark-700 dark:bg-dark-800/70">
      <div class="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-2xl bg-gray-100 text-gray-400 dark:bg-dark-700 dark:text-gray-500">
        <Icon name="inbox" size="lg" />
      </div>
      <p class="text-sm font-medium text-gray-600 dark:text-gray-300">{{ emptyLabel }}</p>
    </div>

    <template v-else>
      <article
        v-for="entry in groups"
        :key="entry.group.id"
        class="overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm transition-colors hover:border-primary-200 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-500/30"
      >
        <header class="grid gap-3 p-4 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
          <div class="min-w-0">
            <div class="flex min-w-0 flex-wrap items-center gap-2">
              <div
                :class="[
                  'flex h-9 w-9 shrink-0 items-center justify-center rounded-xl',
                  platformIconBgClass(entry.group.platform),
                ]"
              >
                <PlatformIcon :platform="entry.group.platform as GroupPlatform" size="sm" />
              </div>

              <h3 class="min-w-0 max-w-full truncate text-base font-bold text-gray-950 dark:text-white">
                {{ entry.group.name }}
              </h3>

              <span
                :class="[
                  'inline-flex items-center rounded-md border px-2 py-0.5 text-[11px] font-semibold',
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

            <div class="mt-2 flex min-w-0 flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
              <span>{{ t('availableChannels.groupCards.sourceCount') }} {{ entry.channelCount }}</span>
              <span class="h-1 w-1 rounded-full bg-gray-300 dark:bg-dark-600"></span>
              <span>{{ t('availableChannels.groupCards.modelCount') }} {{ entry.modelCount }}</span>
              <span class="h-1 w-1 rounded-full bg-gray-300 dark:bg-dark-600"></span>
              <span class="truncate">
                {{ t('availableChannels.groupCards.groupHint', {
                  platform: platformLabel(entry.group.platform),
                  rate: `${effectiveRate(entry.group)}x`,
                }) }}
              </span>
            </div>

            <div class="mt-2 flex flex-wrap gap-1.5">
              <span
                v-for="model in modelPreview(entry)"
                :key="`${entry.group.id}-${model}`"
                class="inline-flex max-w-[14rem] items-center rounded-full bg-gray-50 px-2 py-1 text-[11px] font-medium text-gray-600 ring-1 ring-gray-100 dark:bg-dark-900/70 dark:text-gray-300 dark:ring-dark-700"
              >
                <span class="truncate">{{ model }}</span>
              </span>
              <span
                v-if="remainingModelCount(entry) > 0"
                class="inline-flex items-center rounded-full bg-gray-100 px-2 py-1 text-[11px] font-bold text-gray-500 dark:bg-dark-700 dark:text-gray-400"
              >
                +{{ remainingModelCount(entry) }}
              </span>
            </div>
          </div>

          <div class="flex items-center justify-between gap-2 lg:justify-end">
            <RouterLink
              to="/channel-status"
              class="inline-flex items-center gap-1 rounded-lg border border-gray-200 px-3 py-2 text-xs font-semibold text-gray-600 transition-colors hover:border-primary-200 hover:text-primary-700 dark:border-dark-700 dark:text-gray-300 dark:hover:border-primary-500/40 dark:hover:text-primary-300"
            >
              {{ t('availableChannels.groupCards.viewStatus') }}
              <Icon name="arrowRight" size="xs" />
            </RouterLink>
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded-lg bg-gray-950 px-3 py-2 text-xs font-bold text-white transition-colors hover:bg-primary-700 dark:bg-primary-500/90 dark:hover:bg-primary-400"
              @click="toggleExpanded(entry.group.id)"
            >
              {{ isExpanded(entry.group.id) ? t('common.collapse') : t('common.expand') }}
              <Icon :name="isExpanded(entry.group.id) ? 'chevronUp' : 'chevronDown'" size="xs" />
            </button>
          </div>
        </header>

        <div
          v-if="isExpanded(entry.group.id)"
          class="border-t border-gray-100 bg-gray-50/60 p-3 dark:border-dark-700 dark:bg-dark-900/35"
        >
          <section
            v-for="channel in entry.channels"
            :key="`${entry.group.id}-${channel.platform}-${channel.channelName}`"
            class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800/90"
          >
            <div class="flex flex-col gap-2 border-b border-gray-100 px-3 py-2.5 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="h-2 w-2 rounded-full bg-emerald-400 shadow-[0_0_0_4px_rgba(52,211,153,0.13)]"></span>
                  <h4 class="truncate text-sm font-bold text-gray-900 dark:text-white">
                    {{ channel.channelName }}
                  </h4>
                  <span class="text-[11px] font-medium text-gray-400 dark:text-gray-500">
                    {{ t('availableChannels.groupCards.serviceSource') }}
                  </span>
                </div>
                <p
                  v-if="channel.channelDescription"
                  class="mt-0.5 line-clamp-1 text-xs text-gray-500 dark:text-gray-400"
                >
                  {{ channel.channelDescription }}
                </p>
              </div>
              <span class="shrink-0 text-xs font-semibold text-gray-500 dark:text-gray-400">
                {{ channel.supportedModels.length }} {{ t('availableChannels.groupCards.modelCount') }}
              </span>
            </div>

            <div v-if="channel.supportedModels.length > 0" class="overflow-x-auto">
              <table class="min-w-[58rem] w-full text-left">
                <thead class="bg-gray-50 text-[11px] font-bold uppercase tracking-wide text-gray-400 dark:bg-dark-900/65 dark:text-gray-500">
                  <tr>
                    <th class="px-3 py-2">{{ t('availableChannels.pricing.model') }}</th>
                    <th class="px-3 py-2">{{ t('availableChannels.pricing.billingMode') }}</th>
                    <th class="px-3 py-2">{{ t('availableChannels.pricing.inputPrice') }} {{ t('availableChannels.pricing.unitPerMillion') }}</th>
                    <th class="px-3 py-2">{{ t('availableChannels.pricing.outputPrice') }} {{ t('availableChannels.pricing.unitPerMillion') }}</th>
                    <th class="px-3 py-2">{{ t('availableChannels.pricing.cacheReadPrice') }} {{ t('availableChannels.pricing.unitPerMillion') }}</th>
                    <th class="px-3 py-2">{{ t('availableChannels.pricing.cacheWritePrice') }} {{ t('availableChannels.pricing.unitPerMillion') }}</th>
                    <th class="px-3 py-2">{{ t('availableChannels.pricing.otherPrice') }}</th>
                  </tr>
                </thead>
                <tbody class="divide-y-0">
                  <SupportedModelPriceRow
                    v-for="model in channel.supportedModels"
                    :key="`${entry.group.id}-${channel.channelName}-${model.platform}-${model.name}`"
                    :model="model"
                    :no-pricing-label="noPricingLabel"
                  />
                </tbody>
              </table>
            </div>

            <div
              v-else
              class="px-3 py-5 text-center text-xs text-gray-400 dark:text-gray-500"
            >
              {{ noModelsLabel }}
            </div>
          </section>
        </div>
      </article>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { GroupPlatform } from '@/types'
import type { UserAvailableGroup } from '@/api/channels'
import {
  platformBadgeClass,
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
const expandedGroupIds = ref<Set<number>>(new Set())
const previewModelLimit = 5

watch(
  () => props.groups.map((entry) => entry.group.id),
  (ids) => {
    const validIds = new Set(ids)
    const next = new Set([...expandedGroupIds.value].filter((id) => validIds.has(id)))
    if (next.size === 0 && ids.length > 0) {
      next.add(ids[0])
    }
    expandedGroupIds.value = next
  },
  { immediate: true },
)

function effectiveRate(group: UserAvailableGroup): number {
  return props.userGroupRates[group.id] ?? group.rate_multiplier ?? 1
}

function isExpanded(groupId: number): boolean {
  return expandedGroupIds.value.has(groupId)
}

function toggleExpanded(groupId: number) {
  const next = new Set(expandedGroupIds.value)
  if (next.has(groupId)) {
    next.delete(groupId)
  } else {
    next.add(groupId)
  }
  expandedGroupIds.value = next
}

function modelPreview(entry: AvailableGroupView): string[] {
  return uniqueModelNames(entry).slice(0, previewModelLimit)
}

function remainingModelCount(entry: AvailableGroupView): number {
  return Math.max(uniqueModelNames(entry).length - previewModelLimit, 0)
}

function uniqueModelNames(entry: AvailableGroupView): string[] {
  const names = new Set<string>()
  for (const channel of entry.channels) {
    for (const model of channel.supportedModels) {
      names.add(model.name)
    }
  }
  return Array.from(names).sort((a, b) => a.localeCompare(b))
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
