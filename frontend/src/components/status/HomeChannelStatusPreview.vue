<template>
  <div class="home-monitor-preview">
    <div v-if="loading && items.length === 0" class="home-monitor-grid">
      <div
        v-for="idx in 4"
        :key="idx"
        class="home-monitor-card home-monitor-card-skeleton animate-pulse"
      >
        <div class="flex items-start gap-3">
          <div class="h-11 w-11 rounded-2xl bg-primary-100/80 dark:bg-primary-950/40"></div>
          <div class="flex-1 space-y-2">
            <div class="h-4 w-24 rounded bg-gray-200 dark:bg-dark-700"></div>
            <div class="h-3 w-36 rounded bg-gray-100 dark:bg-dark-800"></div>
          </div>
          <div class="h-7 w-16 rounded-full bg-emerald-100 dark:bg-emerald-500/15"></div>
        </div>
        <div class="mt-5 grid grid-cols-3 gap-2">
          <div class="home-monitor-metric-skeleton"></div>
          <div class="home-monitor-metric-skeleton"></div>
          <div class="home-monitor-metric-skeleton"></div>
        </div>
        <div class="mt-5 h-2 rounded-full bg-gray-100 dark:bg-dark-800"></div>
      </div>
    </div>

    <div v-else-if="items.length === 0" class="home-monitor-empty">
      <div class="home-monitor-empty-icon">
        <Icon name="chart" size="lg" />
      </div>
      <div>
        <p class="home-monitor-empty-title">{{ t('channelStatus.empty.title') }}</p>
        <p class="home-monitor-empty-description">{{ t('channelStatus.empty.description') }}</p>
      </div>
    </div>

    <div v-else class="home-monitor-grid">
      <button
        v-for="item in previewItems"
        :key="item.id"
        type="button"
        class="home-monitor-card"
        @click="goToMonitorPage"
      >
        <div class="home-monitor-card-glow"></div>
        <div class="flex items-start gap-3">
          <span
            class="home-monitor-provider-mark"
            :class="[providerGradient(item.provider), providerTintClass(item.provider)]"
          >
            <ProviderIcon :provider="item.provider" :size="22" />
          </span>

          <div class="min-w-0 flex-1">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="home-monitor-name">{{ item.name }}</div>
                <div class="home-monitor-provider-line">
                  <span
                    class="inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-bold"
                    :class="providerBadgeClass(item.provider)"
                  >
                    {{ providerLabel(item.provider) }}
                  </span>
                  <span class="truncate">{{ item.primary_model }}</span>
                </div>
              </div>

              <span class="home-monitor-status-badge" :class="statusBadgeClass(item.primary_status)">
                {{ statusLabel(item.primary_status) }}
              </span>
            </div>

            <div class="home-monitor-metrics">
              <div class="home-monitor-metric-card">
                <span class="home-monitor-metric-label">{{ t('monitorCommon.dialogLatency') }}</span>
                <strong>{{ formatLatency(item.primary_latency_ms) }}<small>ms</small></strong>
              </div>
              <div class="home-monitor-metric-card">
                <span class="home-monitor-metric-label">{{ t('monitorCommon.endpointPing') }}</span>
                <strong>{{ formatLatency(item.primary_ping_latency_ms) }}<small>ms</small></strong>
              </div>
              <div class="home-monitor-metric-card">
                <span class="home-monitor-metric-label">{{ availabilityLabel }}</span>
                <strong>{{ formatPercent(item.availability_7d) }}</strong>
              </div>
            </div>

            <div class="home-monitor-timeline">
              <span
                v-for="(point, index) in timelinePoints(item)"
                :key="`${item.id}-${index}`"
                class="home-monitor-timeline-slot"
                :class="timelineSlotClass(point.status)"
              ></span>
            </div>

            <div class="home-monitor-foot">
              <span>{{ t('monitorCommon.past') }}</span>
              <span>{{ extraModelsLabel(item.extra_models.length) }}</span>
              <span>{{ t('monitorCommon.now') }}</span>
            </div>
          </div>
        </div>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import type { MonitorStatus } from '@/api/admin/channelMonitor'
import type { PublicMonitorTimelinePoint, PublicMonitorView } from '@/api/publicChannelMonitor'
import { useChannelMonitorFormat, providerGradient } from '@/composables/useChannelMonitorFormat'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  PROVIDER_ANTHROPIC,
  PROVIDER_GEMINI,
  PROVIDER_OPENAI,
  STATUS_DEGRADED,
  STATUS_ERROR,
  STATUS_FAILED,
} from '@/constants/channelMonitor'

const props = defineProps<{
  items: PublicMonitorView[]
  loading: boolean
}>()

const router = useRouter()
const { t } = useI18n()
const { providerBadgeClass, providerLabel, statusBadgeClass, statusLabel, formatLatency, formatPercent } = useChannelMonitorFormat()

const availabilityLabel = computed(() => `${t('monitorCommon.availabilityPrefix')} · 7d`)

const previewItems = computed(() => {
  const priority = [PROVIDER_ANTHROPIC, PROVIDER_OPENAI, PROVIDER_GEMINI]
  return [...props.items]
    .sort((a, b) => priority.indexOf(a.provider) - priority.indexOf(b.provider))
    .slice(0, 4)
})

function providerTintClass(provider: string): string {
  switch (provider) {
    case PROVIDER_OPENAI:
      return 'text-emerald-600 dark:text-emerald-300'
    case PROVIDER_ANTHROPIC:
      return 'text-orange-600 dark:text-orange-300'
    case PROVIDER_GEMINI:
      return 'text-sky-600 dark:text-sky-300'
    default:
      return 'text-gray-500 dark:text-gray-300'
  }
}

function timelinePoints(item: PublicMonitorView): PublicMonitorTimelinePoint[] {
  const points = [...(item.timeline || [])].reverse().slice(0, 24)
  if (points.length >= 24) return points
  return [
    ...Array.from({ length: 24 - points.length }, () => ({
      status: '' as MonitorStatus,
      latency_ms: null,
      ping_latency_ms: null,
      checked_at: '',
    })),
    ...points,
  ]
}

function timelineSlotClass(status: MonitorStatus | ''): string {
  switch (status) {
    case STATUS_DEGRADED:
      return 'is-degraded'
    case STATUS_FAILED:
      return 'is-failed'
    case STATUS_ERROR:
      return 'is-error'
    case 'operational':
      return 'is-operational'
    default:
      return 'is-empty'
  }
}

function extraModelsLabel(count: number): string {
  if (count <= 0) return t('monitorCommon.extraModelsEmpty')
  return t('monitorCommon.extraModelsCount', { n: count })
}

function goToMonitorPage() {
  router.push('/monitor')
}
</script>

<style scoped>
.home-monitor-preview {
  width: 100%;
}

.home-monitor-grid {
  display: grid;
  gap: 1rem;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.home-monitor-card {
  position: relative;
  overflow: hidden;
  width: 100%;
  text-align: left;
  border-radius: 1.6rem;
  border: 1px solid rgba(20, 184, 166, 0.12);
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.94), rgba(240, 253, 250, 0.9)),
    rgba(255, 255, 255, 0.9);
  box-shadow: 0 18px 44px rgba(15, 23, 42, 0.08);
  padding: 1.1rem;
  transition:
    transform 220ms ease,
    box-shadow 220ms ease,
    border-color 220ms ease;
}

.home-monitor-card:hover {
  transform: translateY(-3px);
  box-shadow: 0 26px 52px rgba(15, 23, 42, 0.12);
  border-color: rgba(20, 184, 166, 0.24);
}

.home-monitor-card-glow {
  position: absolute;
  inset: auto -18% -42% auto;
  width: 10rem;
  height: 10rem;
  border-radius: 999px;
  background: radial-gradient(circle, rgba(103, 232, 249, 0.22), transparent 72%);
  pointer-events: none;
}

.home-monitor-provider-mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  width: 2.8rem;
  height: 2.8rem;
  border-radius: 1rem;
  box-shadow: 0 14px 26px rgba(20, 184, 166, 0.16);
}

.home-monitor-name {
  font-size: 1.02rem;
  line-height: 1.2;
  font-weight: 900;
  letter-spacing: -0.03em;
  color: #0f172a;
}

.home-monitor-provider-line {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  margin-top: 0.45rem;
  min-width: 0;
  color: #64748b;
  font-size: 0.76rem;
  font-weight: 600;
}

.home-monitor-status-badge {
  flex: 0 0 auto;
  padding: 0.45rem 0.68rem;
  border-radius: 999px;
  font-size: 0.68rem;
  line-height: 1;
  font-weight: 800;
}

.home-monitor-metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.55rem;
  margin-top: 0.95rem;
}

.home-monitor-metric-card {
  border-radius: 1rem;
  border: 1px solid rgba(20, 184, 166, 0.07);
  background: rgba(245, 255, 251, 0.92);
  padding: 0.68rem 0.72rem;
}

.home-monitor-metric-label {
  display: block;
  font-size: 0.66rem;
  color: #64748b;
  font-weight: 700;
}

.home-monitor-metric-card strong {
  display: inline-flex;
  align-items: flex-end;
  gap: 0.12rem;
  margin-top: 0.42rem;
  color: #052f2c;
  font-size: 1rem;
  line-height: 1;
  font-weight: 900;
  letter-spacing: -0.04em;
}

.home-monitor-metric-card small {
  font-size: 0.66rem;
  font-weight: 800;
  color: #54736f;
}

.home-monitor-timeline {
  display: grid;
  grid-template-columns: repeat(24, minmax(0, 1fr));
  gap: 2px;
  margin-top: 0.95rem;
}

.home-monitor-timeline-slot {
  height: 0.45rem;
  border-radius: 999px;
  background: rgba(203, 213, 225, 0.56);
}

.home-monitor-timeline-slot.is-operational {
  background: linear-gradient(90deg, #14b8a6, #34d399);
}

.home-monitor-timeline-slot.is-degraded {
  background: linear-gradient(90deg, #f59e0b, #fbbf24);
}

.home-monitor-timeline-slot.is-failed,
.home-monitor-timeline-slot.is-error {
  background: linear-gradient(90deg, #f97316, #ef4444);
}

.home-monitor-timeline-slot.is-empty {
  background: rgba(226, 232, 240, 0.82);
}

.home-monitor-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-top: 0.55rem;
  font-size: 0.68rem;
  color: #6b8b87;
  font-weight: 700;
}

.home-monitor-empty {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 1.4rem;
  border-radius: 1.5rem;
  border: 1px dashed rgba(20, 184, 166, 0.22);
  background: rgba(255, 255, 255, 0.68);
}

.home-monitor-empty-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 3rem;
  height: 3rem;
  border-radius: 1rem;
  color: #0f766e;
  background: rgba(20, 184, 166, 0.12);
}

.home-monitor-empty-title {
  color: #0f172a;
  font-size: 1rem;
  font-weight: 900;
}

.home-monitor-empty-description {
  margin-top: 0.35rem;
  color: #64748b;
  font-size: 0.88rem;
  line-height: 1.7;
}

.home-monitor-card-skeleton {
  pointer-events: none;
}

.home-monitor-metric-skeleton {
  height: 4.1rem;
  border-radius: 1rem;
  background: rgba(226, 232, 240, 0.44);
}

@media (max-width: 1023px) {
  .home-monitor-grid {
    grid-template-columns: 1fr;
  }
}
</style>
