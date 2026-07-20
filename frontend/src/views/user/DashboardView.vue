<template>
  <AppLayout v-slot="{ uiVersion }">
    <div class="user-dashboard-page space-y-6">
      <header class="ui-v2-dashboard-header">
        <div>
          <p>{{ t('nav.personalWorkspace') }}</p>
          <h1>{{ t('dashboard.greetingTitle', { greeting, name: displayName }) }}</h1>
          <span>{{ t('dashboard.overviewSubtitle') }}</span>
        </div>
        <div class="ui-v2-dashboard-actions">
          <router-link to="/usage" class="ui-v2-dashboard-button ui-v2-dashboard-button-secondary">
            <Icon name="chart" size="sm" :stroke-width="1.8" />
            <span>{{ t('dashboard.viewUsage') }}</span>
          </router-link>
          <router-link to="/keys" class="ui-v2-dashboard-button ui-v2-dashboard-button-primary">
            <Icon name="plus" size="sm" :stroke-width="1.8" />
            <span>{{ t('dashboard.createApiKey') }}</span>
          </router-link>
        </div>
      </header>

      <div v-if="loading" class="dashboard-loading-state"><LoadingSpinner /></div>

      <div v-else-if="!stats" class="dashboard-load-error card">
        <Icon name="exclamationCircle" size="lg" :stroke-width="1.8" />
        <div>
          <strong>{{ t('dashboard.loadFailed') }}</strong>
          <span>{{ t('dashboard.loadFailedDescription') }}</span>
        </div>
        <button type="button" class="btn btn-secondary" @click="refreshAll">
          <Icon name="refresh" size="sm" />
          {{ t('common.refresh') }}
        </button>
      </div>

      <template v-else>
        <template v-if="uiVersion === 'v2'">
          <UserDashboardStats
            :stats="stats"
            :balance="user?.balance || 0"
            :is-simple="authStore.isSimpleMode"
            :platform-quotas="platformQuotas"
            mode="primary"
          />

          <div class="dashboard-v2-primary-grid">
            <DashboardRequestTrend
              :trend="trendData"
              :loading="loadingCharts"
              :period="trendPeriod"
              @period-change="selectTrendPeriod"
            />
            <section class="dashboard-service-card">
              <ServiceStatusOverview compact />
            </section>
          </div>

          <div class="dashboard-lower-grid grid grid-cols-1 gap-6 lg:grid-cols-3">
            <div class="lg:col-span-2"><UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" /></div>
            <div class="lg:col-span-1"><UserDashboardQuickActions /></div>
          </div>

          <section class="dashboard-v2-secondary">
            <header class="dashboard-v2-secondary-header">
              <h2>{{ t('dashboard.moreMetrics') }}</h2>
              <p>{{ t('dashboard.moreMetricsDescription') }}</p>
            </header>
            <UserDashboardStats
              :stats="stats"
              :balance="user?.balance || 0"
              :is-simple="authStore.isSimpleMode"
              :platform-quotas="platformQuotas"
              mode="secondary"
            />
            <UserDashboardCharts
              v-model:startDate="startDate"
              v-model:endDate="endDate"
              v-model:granularity="granularity"
              :loading="loadingCharts"
              :trend="trendData"
              :models="modelStats"
              @dateRangeChange="loadCharts"
              @granularityChange="loadCharts"
              @refresh="refreshAll"
            />
          </section>
        </template>

        <template v-else>
          <UserDashboardStats
            :stats="stats"
            :balance="user?.balance || 0"
            :is-simple="authStore.isSimpleMode"
            :platform-quotas="platformQuotas"
          />
          <ServiceStatusOverview compact />
          <UserDashboardCharts
            v-model:startDate="startDate"
            v-model:endDate="endDate"
            v-model:granularity="granularity"
            :loading="loadingCharts"
            :trend="trendData"
            :models="modelStats"
            @dateRangeChange="loadCharts"
            @granularityChange="loadCharts"
            @refresh="refreshAll"
          />
          <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
            <div class="lg:col-span-2"><UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" /></div>
            <div class="lg:col-span-1"><UserDashboardQuickActions /></div>
          </div>
        </template>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import { userAPI } from '@/api/user'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import DashboardRequestTrend from '@/components/user/dashboard/DashboardRequestTrend.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'
import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import ServiceStatusOverview from '@/components/status/ServiceStatusOverview.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import { useUiVersion } from '@/composables/useUiVersion'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem } from '@/types'

const authStore = useAuthStore()
const { t } = useI18n()
const user = computed(() => authStore.user)
const { uiVersion: initialUiVersion } = useUiVersion(computed(() => user.value?.id))
const displayName = computed(() => user.value?.username || user.value?.email?.split('@')[0] || t('nav.personalWorkspace'))
const greeting = computed(() => {
  const hour = new Date().getHours()
  if (hour < 12) return t('dashboard.greetingMorning')
  if (hour < 18) return t('dashboard.greetingAfternoon')
  return t('dashboard.greetingEvening')
})

const stats = ref<UserStatsType | null>(null)
const loading = ref(false)
const loadingUsage = ref(false)
const loadingCharts = ref(false)
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[]>([])

const formatLD = (d: Date) => d.toISOString().split('T')[0]
const trendPeriod = ref<7 | 30 | 90>(initialUiVersion.value === 'v2' ? 30 : 7)
const startDate = ref(formatLD(new Date(Date.now() - (trendPeriod.value - 1) * 86400000)))
const endDate = ref(formatLD(new Date()))
const granularity = ref('day')

const loadStats = async () => {
  loading.value = true
  try {
    await authStore.refreshUser()
    stats.value = await usageAPI.getDashboardStats()
  } catch (error) {
    console.error('Failed to load dashboard stats:', error)
  } finally {
    loading.value = false
  }
}

const loadPlatformQuotas = async () => {
  if (authStore.isSimpleMode) {
    platformQuotas.value = []
    return
  }
  try {
    const res = await userAPI.getMyPlatformQuotas()
    platformQuotas.value = res.platform_quotas || []
  } catch (error) {
    console.error('Failed to load platform quotas:', error)
    platformQuotas.value = []
  }
}

const loadCharts = async () => {
  loadingCharts.value = true
  try {
    const res = await Promise.all([
      usageAPI.getDashboardTrend({
        start_date: startDate.value,
        end_date: endDate.value,
        granularity: granularity.value as 'day' | 'hour',
      }),
      usageAPI.getDashboardModels({ start_date: startDate.value, end_date: endDate.value }),
    ])
    trendData.value = res[0].trend || []
    modelStats.value = res[1].models || []
  } catch (error) {
    console.error('Failed to load charts:', error)
  } finally {
    loadingCharts.value = false
  }
}

const loadRecent = async () => {
  loadingUsage.value = true
  try {
    const res = await usageAPI.getByDateRange(startDate.value, endDate.value)
    recentUsage.value = res.items.slice(0, 5)
  } catch (error) {
    console.error('Failed to load recent usage:', error)
  } finally {
    loadingUsage.value = false
  }
}

const selectTrendPeriod = (days: 7 | 30 | 90) => {
  trendPeriod.value = days
  endDate.value = formatLD(new Date())
  startDate.value = formatLD(new Date(Date.now() - (days - 1) * 86400000))
  granularity.value = 'day'
  void loadCharts()
}

const refreshAll = () => {
  void loadStats()
  void loadPlatformQuotas()
  void loadCharts()
  void loadRecent()
}

onMounted(refreshAll)
</script>
