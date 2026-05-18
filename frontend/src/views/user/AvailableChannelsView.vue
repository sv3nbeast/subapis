<template>
  <AppLayout>
    <div class="space-y-6">
      <section class="available-groups-hero overflow-hidden rounded-3xl border border-primary-100/80 bg-white/85 p-5 shadow-sm dark:border-primary-500/15 dark:bg-dark-800/85 sm:p-6">
        <div class="flex flex-col gap-5 xl:flex-row xl:items-end xl:justify-between">
          <div class="max-w-3xl">
            <div class="mb-3 inline-flex items-center gap-2 rounded-full bg-primary-100 px-3 py-1 text-xs font-semibold text-primary-700 dark:bg-primary-500/15 dark:text-primary-300">
              <Icon name="key" size="xs" />
              {{ t('availableChannels.eyebrow') }}
            </div>
            <h1 class="text-2xl font-black tracking-tight text-gray-950 dark:text-white sm:text-3xl">
              {{ t('availableChannels.title') }}
            </h1>
            <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">
              {{ t('availableChannels.description') }}
            </p>
          </div>

          <div class="flex flex-col gap-3 lg:flex-row lg:items-center">
            <div class="relative w-full lg:w-80">
              <Icon
                name="search"
                size="md"
                class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
              />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('availableChannels.searchPlaceholder')"
                class="input bg-white/90 pl-10 dark:bg-dark-900/80"
              />
            </div>

            <select v-model="platformFilter" class="input w-full bg-white/90 lg:w-40 dark:bg-dark-900/80">
              <option value="">{{ t('availableChannels.filters.allPlatforms') }}</option>
              <option
                v-for="platform in platformOptions"
                :key="platform"
                :value="platform"
              >
                {{ platformLabel(platform) }}
              </option>
            </select>

            <select v-model="groupTypeFilter" class="input w-full bg-white/90 lg:w-36 dark:bg-dark-900/80">
              <option value="all">{{ t('availableChannels.filters.allGroups') }}</option>
              <option value="public">{{ t('availableChannels.public') }}</option>
              <option value="exclusive">{{ t('availableChannels.exclusive') }}</option>
            </select>

            <button
              @click="loadChannels"
              :disabled="loading"
              class="btn btn-secondary shrink-0"
              :title="t('common.refresh', 'Refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
              <span class="lg:hidden">{{ t('common.refresh') }}</span>
            </button>
          </div>
        </div>
      </section>

      <section class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <div
          v-for="stat in summaryStats"
          :key="stat.key"
          class="rounded-2xl border border-gray-100 bg-white/85 p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800/80"
        >
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ stat.label }}</p>
              <p class="mt-1 text-2xl font-black text-gray-950 dark:text-white">{{ stat.value }}</p>
            </div>
            <div class="flex h-10 w-10 items-center justify-center rounded-2xl bg-primary-100 text-primary-600 dark:bg-primary-500/15 dark:text-primary-300">
              <Icon :name="stat.icon" size="md" />
            </div>
          </div>
        </div>
      </section>

      <AvailableGroupCards
        :groups="filteredGroupViews"
        :loading="loading"
        :user-group-rates="userGroupRates"
        :no-pricing-label="t('availableChannels.noPricing')"
        :no-models-label="t('availableChannels.noModels')"
        :empty-label="t('availableChannels.empty')"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import AvailableGroupCards from '@/components/channels/AvailableGroupCards.vue'
import userChannelsAPI, {
  type UserAvailableChannel,
  type UserAvailableGroup,
  type UserSupportedModel,
} from '@/api/channels'
import userGroupsAPI from '@/api/groups'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformLabel } from '@/utils/platformColors'
import type {
  AvailableGroupChannelView,
  AvailableGroupView,
} from '@/components/channels/availableGroupTypes'

const { t } = useI18n()
const appStore = useAppStore()

const channels = ref<UserAvailableChannel[]>([])
const userGroupRates = ref<Record<number, number>>({})
const loading = ref(false)
const searchQuery = ref('')
const platformFilter = ref('')
const groupTypeFilter = ref<'all' | 'public' | 'exclusive'>('all')

const groupViews = computed<AvailableGroupView[]>(() => buildGroupViews(channels.value))

const platformOptions = computed(() => {
  const platforms = new Set<string>()
  for (const entry of groupViews.value) {
    if (entry.group.platform) platforms.add(entry.group.platform)
  }
  return Array.from(platforms).sort((a, b) => platformLabel(a).localeCompare(platformLabel(b)))
})

const filteredGroupViews = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  return groupViews.value.filter((entry) => {
    if (platformFilter.value && entry.group.platform !== platformFilter.value) return false
    if (groupTypeFilter.value === 'public' && entry.group.is_exclusive) return false
    if (groupTypeFilter.value === 'exclusive' && !entry.group.is_exclusive) return false
    if (!q) return true

    return (
      entry.group.name.toLowerCase().includes(q) ||
      entry.group.platform.toLowerCase().includes(q) ||
      platformLabel(entry.group.platform).toLowerCase().includes(q) ||
      entry.channels.some((ch) =>
        ch.channelName.toLowerCase().includes(q) ||
        (ch.channelDescription || '').toLowerCase().includes(q) ||
        ch.supportedModels.some((model) => model.name.toLowerCase().includes(q)),
      )
    )
  })
})

const summaryStats = computed(() => {
  const visible = filteredGroupViews.value
  const sourceNames = new Set<string>()
  const models = new Set<string>()
  let exclusiveCount = 0

  for (const entry of visible) {
    if (entry.group.is_exclusive) exclusiveCount += 1
    for (const ch of entry.channels) {
      sourceNames.add(`${entry.group.platform}:${ch.channelName}`)
      for (const model of ch.supportedModels) {
        models.add(`${entry.group.platform}:${model.name}`)
      }
    }
  }

  return [
    {
      key: 'groups',
      label: t('availableChannels.stats.groups'),
      value: visible.length,
      icon: 'key' as const,
    },
    {
      key: 'models',
      label: t('availableChannels.stats.models'),
      value: models.size,
      icon: 'cpu' as const,
    },
    {
      key: 'sources',
      label: t('availableChannels.stats.sources'),
      value: sourceNames.size,
      icon: 'server' as const,
    },
    {
      key: 'exclusive',
      label: t('availableChannels.stats.exclusive'),
      value: exclusiveCount,
      icon: 'shield' as const,
    },
  ]
})

function buildGroupViews(source: UserAvailableChannel[]): AvailableGroupView[] {
  const map = new Map<number, { group: UserAvailableGroup; channels: AvailableGroupChannelView[] }>()

  for (const channel of source) {
    for (const section of channel.platforms) {
      for (const group of section.groups) {
        if (!map.has(group.id)) {
          map.set(group.id, { group, channels: [] })
        }
        map.get(group.id)!.channels.push({
          channelName: channel.name,
          channelDescription: channel.description,
          platform: section.platform,
          supportedModels: dedupeModels(section.supported_models),
        })
      }
    }
  }

  return Array.from(map.values())
    .map(({ group, channels }) => {
      const modelKeys = new Set<string>()
      const billingModes = new Set<string>()
      for (const ch of channels) {
        for (const model of ch.supportedModels) {
          modelKeys.add(`${ch.platform}:${model.name}`)
          if (model.pricing?.billing_mode) billingModes.add(model.pricing.billing_mode)
        }
      }
      return {
        group,
        channels: channels.sort((a, b) => a.channelName.localeCompare(b.channelName)),
        channelCount: channels.length,
        modelCount: modelKeys.size,
        billingModes: Array.from(billingModes),
      }
    })
    .sort((a, b) => {
      if (a.group.is_exclusive !== b.group.is_exclusive) {
        return a.group.is_exclusive ? -1 : 1
      }
      return a.group.name.localeCompare(b.group.name)
    })
}

function dedupeModels(models: UserSupportedModel[]): UserSupportedModel[] {
  const seen = new Set<string>()
  const out: UserSupportedModel[] = []
  for (const model of models) {
    const key = `${model.platform}:${model.name}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(model)
  }
  return out.sort((a, b) => a.name.localeCompare(b.name))
}

async function loadChannels() {
  loading.value = true
  try {
    // 渠道列表和用户专属倍率并发拉取。专属倍率失败不阻塞分组展示，
    // 失败时降级为显示分组默认倍率。
    const [list, rates] = await Promise.all([
      userChannelsAPI.getAvailable(),
      userGroupsAPI.getUserGroupRates().catch((err: unknown) => {
        console.error('Failed to load user group rates:', err)
        return {} as Record<number, number>
      }),
    ])
    channels.value = list
    userGroupRates.value = rates
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

onMounted(loadChannels)
</script>

<style scoped>
.available-groups-hero {
  position: relative;
}

.available-groups-hero::before {
  content: '';
  position: absolute;
  inset: 0;
  pointer-events: none;
  background:
    radial-gradient(circle at 0% 0%, rgba(20, 184, 166, 0.17), transparent 30%),
    radial-gradient(circle at 92% 12%, rgba(6, 182, 212, 0.14), transparent 28%);
}

.available-groups-hero > * {
  position: relative;
  z-index: 1;
}
</style>
