<template>
  <div class="text-xs" data-testid="monitor-observation">
    <span class="inline-flex rounded-full px-2 py-1 font-semibold" :class="statusBadgeClass(observation.badgeStatus as MonitorStatus | '')">
      {{ observation.key === 'quota' ? statusLabel(item.primary_status as MonitorStatus) : t('monitorCommon.observation.' + observation.key) }}
    </span>
    <div v-if="item.primary_checked_at" class="mt-1 text-gray-500 dark:text-gray-400">
      {{ t('monitorCommon.observation.checked', { time: formatRelativeTime(item.primary_checked_at) }) }}
    </div>
    <div v-if="item.interval_seconds" class="mt-1 text-gray-500 dark:text-gray-400">
      {{ t('monitorCommon.observation.interval', { n: item.interval_seconds }) }}
      <span v-if="item.probe_path" class="font-mono"> · {{ item.probe_path }}</span>
    </div>
    <p class="mt-1 text-gray-500 dark:text-gray-400">{{ t('monitorCommon.observation.scope') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useNow } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import type { MonitorStatus } from '@/api/admin/channelMonitor'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import { monitorObservation, type MonitorObservationSource } from '@/utils/monitorObservation'
const props = defineProps<{ item: MonitorObservationSource }>()
const { t } = useI18n()
const { statusBadgeClass, statusLabel, formatRelativeTime } = useChannelMonitorFormat()
const now = useNow({ interval: 30000 })
const observation = computed(() => monitorObservation(props.item, now.value.getTime()))
</script>
