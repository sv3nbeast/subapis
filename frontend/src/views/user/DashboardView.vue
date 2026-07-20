<template>
  <AppLayout v-slot="{ uiVersion }">
    <div class="user-dashboard-page space-y-6">
      <header class="ui-v2-dashboard-header">
        <div>
          <p>{{ t('nav.personalWorkspace') }}</p>
          <h1>{{ t('dashboard.greetingTitle', { greeting, name: displayName }) }}</h1>
          <span>{{ dashboardSubtitle }}</span>
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

      <div v-if="uiVersion === 'v2'" class="dashboard-command-bar">
        <div class="dashboard-view-segmented" role="tablist" :aria-label="t('dashboard.workspaceView')">
          <button
            id="dashboard-overview-tab"
            type="button"
            role="tab"
            data-dashboard-view="overview"
            :class="{ 'is-active': activeDashboardView === 'overview' }"
            :aria-selected="activeDashboardView === 'overview'"
            aria-controls="dashboard-overview-panel"
            @click="selectDashboardView('overview')"
          >
            <Icon name="grid" size="sm" :stroke-width="1.8" />
            {{ t('dashboard.overviewView') }}
          </button>
          <button
            id="dashboard-analysis-tab"
            type="button"
            role="tab"
            data-dashboard-view="analysis"
            :class="{ 'is-active': activeDashboardView === 'analysis' }"
            :aria-selected="activeDashboardView === 'analysis'"
            aria-controls="dashboard-analysis-panel"
            @click="selectDashboardView('analysis')"
          >
            <Icon name="chart" size="sm" :stroke-width="1.8" />
            {{ t('dashboard.analysisView') }}
          </button>
        </div>
        <button
          type="button"
          class="dashboard-refresh-button"
          :title="t('common.refresh')"
          :aria-label="t('common.refresh')"
          :disabled="refreshing"
          @click="refreshAll"
        >
          <Icon name="refresh" size="sm" :stroke-width="1.8" />
        </button>
      </div>

      <div v-if="loadingStats" class="dashboard-loading-state"><LoadingSpinner /></div>

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
          <section
            v-if="activeDashboardView === 'overview'"
            id="dashboard-overview-panel"
            class="dashboard-workspace-panel"
            role="tabpanel"
            aria-labelledby="dashboard-overview-tab"
          >
            <UserDashboardStats
              :stats="stats"
              :balance="user?.balance || 0"
              :is-simple="authStore.isSimpleMode"
              :platform-quotas="platformQuotas"
              mode="primary"
            />

            <div class="dashboard-v2-primary-grid">
              <DashboardRequestTrend
                :trend="overviewTrend"
                :loading="loadingOverviewTrend"
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
          </section>

          <section
            v-else
            id="dashboard-analysis-panel"
            class="dashboard-workspace-panel dashboard-analysis-panel"
            role="tabpanel"
            aria-labelledby="dashboard-analysis-tab"
          >
            <header class="dashboard-analysis-heading">
              <div>
                <h2>{{ t('dashboard.moreMetrics') }}</h2>
                <p>{{ t('dashboard.moreMetricsDescription') }}</p>
              </div>
            </header>

            <UserDashboardStats
              :stats="stats"
              :balance="user?.balance || 0"
              :is-simple="authStore.isSimpleMode"
              :platform-quotas="platformQuotas"
              mode="secondary"
            />

            <UserDashboardCharts
              v-model:startDate="analysisStartDate"
              v-model:endDate="analysisEndDate"
              v-model:granularity="analysisGranularity"
              :loading="loadingAnalytics"
              :trend="analyticsTrend"
              :models="modelStats"
              @dateRangeChange="loadAnalytics"
              @granularityChange="loadAnalytics"
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
            v-model:startDate="analysisStartDate"
            v-model:endDate="analysisEndDate"
            v-model:granularity="analysisGranularity"
            :loading="loadingAnalytics"
            :trend="analyticsTrend"
            :models="modelStats"
            @dateRangeChange="loadAnalytics"
            @granularityChange="loadAnalytics"
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
import { computed, defineAsyncComponent, defineComponent, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import { userAPI } from '@/api/user'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import DashboardRequestTrend from '@/components/user/dashboard/DashboardRequestTrend.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'
import ServiceStatusOverview from '@/components/status/ServiceStatusOverview.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import { useUiVersion } from '@/composables/useUiVersion'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem } from '@/types'

const DashboardChartsLoading = defineComponent({
  name: 'DashboardChartsLoading',
  setup: () => () => h('div', { class: 'dashboard-analysis-loading' }, [h(LoadingSpinner)]),
})
const UserDashboardCharts = defineAsyncComponent({
  loader: () => import('@/components/user/dashboard/UserDashboardCharts.vue').then((module) => module.default),
  loadingComponent: DashboardChartsLoading,
  delay: 0,
})

type DashboardViewMode = 'overview' | 'analysis'

const authStore = useAuthStore()
const { t } = useI18n()
const user = computed(() => authStore.user)
const { uiVersion: initialUiVersion } = useUiVersion(computed(() => user.value?.id))
const activeDashboardView = ref<DashboardViewMode>('overview')
const displayName = computed(() => user.value?.username || user.value?.email?.split('@')[0] || t('nav.personalWorkspace'))
const greeting = computed(() => {
  const hour = new Date().getHours()
  if (hour < 12) return t('dashboard.greetingMorning')
  if (hour < 18) return t('dashboard.greetingAfternoon')
  return t('dashboard.greetingEvening')
})
const dashboardSubtitle = computed(() => activeDashboardView.value === 'overview'
  ? t('dashboard.overviewSubtitle')
  : t('dashboard.analysisSubtitle'))

const stats = ref<UserStatsType | null>(null)
const loadingStats = ref(true)
const loadingUsage = ref(false)
const loadingOverviewTrend = ref(false)
const loadingAnalytics = ref(false)
const overviewTrend = ref<TrendDataPoint[]>([])
const analyticsTrend = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[]>([])
const analyticsLoaded = ref(false)

const formatLD = (d: Date) => d.toISOString().split('T')[0]
const today = () => new Date()
const daysAgo = (days: number) => new Date(Date.now() - days * 86400000)
const trendPeriod = ref<7 | 30 | 90>(30)
const analysisDays = initialUiVersion.value === 'v2' ? 30 : 7
const analysisStartDate = ref(formatLD(daysAgo(analysisDays - 1)))
const analysisEndDate = ref(formatLD(today()))
const analysisGranularity = ref('day')

const refreshing = computed(() => loadingStats.value || loadingUsage.value || loadingOverviewTrend.value || loadingAnalytics.value)

const loadStats = async (refreshUser = false) => {
  loadingStats.value = true
  try {
    if (refreshUser) {
      await authStore.refreshUser()
    }
    stats.value = await usageAPI.getDashboardStats()
  } catch (error) {
    console.error('Failed to load dashboard stats:', error)
  } finally {
    loadingStats.value = false
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

const loadOverviewTrend = async () => {
  if (loadingOverviewTrend.value) return
  loadingOverviewTrend.value = true
  const endDate = formatLD(today())
  const startDate = formatLD(daysAgo(trendPeriod.value - 1))
  try {
    const res = await usageAPI.getDashboardTrend({
      start_date: startDate,
      end_date: endDate,
      granularity: 'day',
    })
    overviewTrend.value = res.trend || []
  } catch (error) {
    console.error('Failed to load overview trend:', error)
  } finally {
    loadingOverviewTrend.value = false
  }
}

const loadAnalytics = async () => {
  if (loadingAnalytics.value) return
  loadingAnalytics.value = true
  try {
    const [trendResponse, modelsResponse] = await Promise.all([
      usageAPI.getDashboardTrend({
        start_date: analysisStartDate.value,
        end_date: analysisEndDate.value,
        granularity: analysisGranularity.value as 'day' | 'hour',
      }),
      usageAPI.getDashboardModels({
        start_date: analysisStartDate.value,
        end_date: analysisEndDate.value,
      }),
    ])
    analyticsTrend.value = trendResponse.trend || []
    modelStats.value = modelsResponse.models || []
    analyticsLoaded.value = true
  } catch (error) {
    console.error('Failed to load analytics:', error)
  } finally {
    loadingAnalytics.value = false
  }
}

const loadRecent = async () => {
  if (loadingUsage.value) return
  loadingUsage.value = true
  const endDate = formatLD(today())
  const startDate = formatLD(daysAgo(6))
  try {
    const res = await usageAPI.query({
      start_date: startDate,
      end_date: endDate,
      page: 1,
      page_size: 5,
      sort_by: 'created_at',
      sort_order: 'desc',
    })
    recentUsage.value = res.items
  } catch (error) {
    console.error('Failed to load recent usage:', error)
  } finally {
    loadingUsage.value = false
  }
}

const selectTrendPeriod = (days: 7 | 30 | 90) => {
  if (trendPeriod.value === days) return
  trendPeriod.value = days
  void loadOverviewTrend()
}

const selectDashboardView = (view: DashboardViewMode) => {
  activeDashboardView.value = view
  if (view === 'analysis' && !analyticsLoaded.value) {
    void loadAnalytics()
  }
}

const refreshAll = () => {
  void loadStats(true)
  void loadPlatformQuotas()
  void loadRecent()
  if (initialUiVersion.value === 'v2' && activeDashboardView.value === 'overview') {
    void loadOverviewTrend()
  } else {
    void loadAnalytics()
  }
}

onMounted(() => {
  void loadStats()
  void loadPlatformQuotas()
  void loadRecent()
  if (initialUiVersion.value === 'v2') {
    void loadOverviewTrend()
  } else {
    void loadAnalytics()
  }
})
</script>
