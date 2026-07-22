<template>
  <div ref="rootRef" class="home-v2">
    <section class="home-v2-screen home-v2-hero" aria-labelledby="home-v2-title">
      <div class="home-v2-shell home-v2-hero-grid">
        <div class="home-v2-hero-copy home-v2-reveal">
          <p class="home-v2-section-index">{{ siteName }} · 01 / {{ t('home.experience.overview.section') }}</p>
          <h1 id="home-v2-title" class="home-v2-title">
            <span>{{ t('home.hero.titleLine1') }}</span>
            <span>{{ t('home.hero.titleLine2') }} <strong>{{ t('home.hero.titleHighlight') }}</strong></span>
          </h1>
          <p class="home-v2-lede">{{ t('home.hero.description') }}</p>

          <div class="home-v2-endpoint" role="group" :aria-label="t('home.hero.baseUrlLabel')">
            <div class="home-v2-endpoint-label">
              <span><i aria-hidden="true"></i>{{ t('home.hero.baseUrlLabel') }}</span>
              <strong>{{ t('home.experience.overview.ready') }}</strong>
            </div>
            <div class="home-v2-endpoint-line">
              <code>{{ apiBaseUrl }}</code>
              <span class="home-v2-endpoint-paths" aria-hidden="true">
                <span
                  v-for="(path, index) in endpointPaths"
                  :key="path"
                  :style="{ animationDelay: `${index * 2.6}s` }"
                >
                  {{ path }}
                </span>
              </span>
              <button type="button" :title="t('common.copy')" :aria-label="t('common.copy')" @click="$emit('copy-base-url')">
                <Icon name="copy" size="sm" />
              </button>
            </div>
          </div>

          <div class="home-v2-actions">
            <RouterLink :to="isAuthenticated ? dashboardPath : '/login'" class="home-v2-button home-v2-button-primary">
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
              <Icon name="arrowRight" size="sm" />
            </RouterLink>
            <RouterLink to="/docs" class="home-v2-button home-v2-button-secondary">
              <Icon name="book" size="sm" />
              {{ t('home.guide') }}
            </RouterLink>
          </div>

          <dl class="home-v2-proof" :aria-label="t('home.experience.overview.proof')">
            <div>
              <dt>{{ t('home.hero.stats.models') }}</dt>
              <dd>{{ modelCountLabel }}</dd>
            </div>
            <div>
              <dt>{{ t('home.experience.overview.interfaces') }}</dt>
              <dd>{{ endpointPaths.length }}</dd>
            </div>
            <div>
              <dt>{{ t('home.experience.overview.monitors') }}</dt>
              <dd>{{ publicMonitorItems.length || '—' }}</dd>
            </div>
          </dl>
        </div>

        <aside class="home-v2-routing home-v2-reveal" :aria-label="t('home.experience.overview.routingTitle')">
          <header>
            <div>
              <span class="home-v2-live-dot" aria-hidden="true"></span>
              <p>{{ t('home.experience.overview.routingLabel') }}</p>
            </div>
            <span>{{ t('home.experience.overview.live') }}</span>
          </header>
          <div class="home-v2-routing-title">
            <h2>{{ t('home.experience.overview.routingTitle') }}</h2>
            <p>{{ t('home.experience.overview.routingDescription') }}</p>
          </div>
          <div class="home-v2-routing-list">
            <article
              v-for="(feature, index) in heroFeatures"
              :key="feature.title"
              :class="`home-v2-routing-row home-v2-routing-row-${index + 1}`"
            >
              <span><Icon :name="feature.icon" size="sm" /></span>
              <div>
                <h3>{{ feature.title }}</h3>
                <p>{{ feature.description }}</p>
              </div>
              <i aria-hidden="true"></i>
            </article>
          </div>
          <footer>
            <span>{{ t('home.experience.overview.policy') }}</span>
            <div>
              <span
                v-for="channel in coreChannels"
                :key="channel.name"
                class="home-v2-provider-logo"
                :class="channel.markClass"
                role="img"
                :aria-label="channel.name"
                :title="channel.name"
              >
                <PlatformIcon :platform="channel.platform" size="lg" />
              </span>
            </div>
          </footer>
        </aside>
      </div>
    </section>

    <section class="home-v2-screen home-v2-capabilities" aria-labelledby="home-v2-capabilities-title">
      <div class="home-v2-shell home-v2-split">
        <header class="home-v2-section-heading home-v2-reveal">
          <p class="home-v2-section-index">02 / {{ t('home.experience.capabilities.section') }}</p>
          <h2 id="home-v2-capabilities-title">{{ t('home.value.title') }}</h2>
          <p>{{ t('home.value.description') }}</p>
          <div class="home-v2-heading-rule" aria-hidden="true"><span></span></div>
        </header>

        <div class="home-v2-capability-ledger home-v2-reveal">
          <article v-for="(capability, index) in valueCards" :key="capability.title">
            <span class="home-v2-row-number">0{{ index + 1 }}</span>
            <span class="home-v2-row-icon"><Icon :name="capability.icon" size="md" /></span>
            <div>
              <h3>{{ capability.title }}</h3>
              <p>{{ capability.description }}</p>
            </div>
            <Icon name="chevronRight" size="sm" class="home-v2-row-arrow" />
          </article>
        </div>
      </div>
    </section>

    <section class="home-v2-screen home-v2-workflow" aria-labelledby="home-v2-workflow-title">
      <div class="home-v2-shell">
        <header class="home-v2-wide-heading home-v2-reveal">
          <div>
            <p class="home-v2-section-index">03 / {{ t('home.experience.workflow.section') }}</p>
            <h2 id="home-v2-workflow-title">{{ t('home.workflow.title') }}</h2>
          </div>
          <p>{{ t('home.experience.workflow.description') }}</p>
        </header>

        <ol class="home-v2-workflow-rail home-v2-reveal">
          <li v-for="step in workflowSteps" :key="step.step">
            <span>{{ step.step }}</span>
            <div class="home-v2-workflow-icon"><Icon :name="step.icon" size="md" /></div>
            <h3>{{ step.title }}</h3>
            <p>{{ step.description }}</p>
          </li>
        </ol>

        <div class="home-v2-config home-v2-reveal" :aria-label="t('home.experience.workflow.config')">
          <div class="home-v2-config-address">
            <span>{{ t('home.experience.workflow.config') }}</span>
            <code>{{ apiBaseUrl }}</code>
          </div>
          <div class="home-v2-config-protocols">
            <span v-for="path in endpointPaths.slice(0, 3)" :key="path">{{ path }}</span>
          </div>
          <RouterLink to="/docs">
            {{ t('home.guide') }}
            <Icon name="arrowRight" size="sm" />
          </RouterLink>
        </div>
      </div>
    </section>

    <section class="home-v2-screen home-v2-models" aria-labelledby="home-v2-models-title">
      <div class="home-v2-shell home-v2-models-grid">
        <header class="home-v2-model-summary home-v2-reveal">
          <p class="home-v2-section-index">04 / {{ t('home.experience.models.section') }}</p>
          <p class="home-v2-model-number">
            <strong>{{ modelCountLabel }}</strong>
            <span>{{ t('home.hero.stats.models') }}</span>
          </p>
          <h2 id="home-v2-models-title">{{ t('home.channels.title') }}</h2>
          <p>{{ t('home.channels.description') }}</p>
          <RouterLink v-if="publicModelMarketEnabled" to="/models" class="home-v2-text-link">
            {{ t('modelMarket.viewModelsAndPricing') }}
            <Icon name="arrowRight" size="sm" />
          </RouterLink>
        </header>

        <div class="home-v2-channel-ledger home-v2-reveal">
          <div class="home-v2-ledger-head">
            <span>{{ t('home.experience.models.provider') }}</span>
            <span>{{ t('home.experience.models.capability') }}</span>
            <span>{{ t('home.experience.models.state') }}</span>
          </div>
          <article v-for="channel in supportedChannels" :key="channel.name">
            <div class="home-v2-channel-name">
              <i :class="channel.markClass">{{ channel.shortName }}</i>
              <strong>{{ channel.name }}</strong>
            </div>
            <p>{{ channel.description }}</p>
            <span class="home-v2-channel-state"><i aria-hidden="true"></i>{{ channel.status }}</span>
          </article>
        </div>
      </div>
    </section>

    <section class="home-v2-screen home-v2-status" aria-labelledby="home-v2-status-title">
      <div class="home-v2-shell home-v2-status-grid">
        <header class="home-v2-status-copy home-v2-reveal">
          <p class="home-v2-section-index">05 / {{ t('home.experience.status.section') }}</p>
          <span class="home-v2-status-signal" :class="{ 'is-warning': hasMonitorIssue }">
            <i aria-hidden="true"></i>
            {{ monitorSummary }}
          </span>
          <h2 id="home-v2-status-title">{{ t('home.statusPreview.title') }}</h2>
          <p>{{ t('home.statusPreview.description') }}</p>
          <div class="home-v2-actions">
            <RouterLink to="/monitor" class="home-v2-button home-v2-button-light">
              <Icon name="chart" size="sm" />
              {{ t('home.statusPreview.button') }}
            </RouterLink>
            <RouterLink to="/status" class="home-v2-text-link home-v2-text-link-light">
              {{ t('nav.serviceStatus') }}
              <Icon name="arrowRight" size="sm" />
            </RouterLink>
          </div>
        </header>

        <div class="home-v2-monitor home-v2-reveal">
          <div class="home-v2-monitor-head">
            <span>{{ t('home.experience.status.liveMonitor') }}</span>
            <span>{{ t('home.experience.status.availability') }}</span>
            <span>{{ t('home.experience.status.latency') }}</span>
          </div>
          <div v-if="publicMonitorLoading" class="home-v2-monitor-empty">
            <span class="home-v2-monitor-spinner" aria-hidden="true"></span>
            {{ t('common.loading') }}
          </div>
          <div v-else-if="visibleMonitors.length === 0" class="home-v2-monitor-empty">
            {{ t('home.experience.status.empty') }}
          </div>
          <article v-for="item in visibleMonitors" v-else :key="item.id">
            <div class="home-v2-monitor-name">
              <span :class="`is-${item.provider}`">{{ providerLabel(item.provider) }}</span>
              <div>
                <strong>{{ item.name }}</strong>
                <small>{{ item.primary_model }}</small>
              </div>
            </div>
            <div class="home-v2-monitor-timeline" aria-hidden="true">
              <i v-for="(point, index) in monitorPoints(item)" :key="`${item.id}-${index}`" :class="statusClass(point.status)"></i>
            </div>
            <strong>{{ formatPercent(item.availability_7d) }}</strong>
            <span>{{ latencyLabel(item.primary_latency_ms) }}</span>
          </article>
        </div>
      </div>
    </section>

    <section class="home-v2-screen home-v2-final" aria-labelledby="home-v2-final-title">
      <div class="home-v2-shell home-v2-final-layout">
        <header class="home-v2-final-heading home-v2-reveal">
          <div>
            <p class="home-v2-section-index">06 / {{ t('home.experience.resources.section') }}</p>
            <h2 id="home-v2-final-title">{{ t('home.cta.title') }}</h2>
            <p>{{ t('home.cta.description') }}</p>
          </div>
          <div class="home-v2-actions">
            <RouterLink :to="isAuthenticated ? dashboardPath : '/login'" class="home-v2-button home-v2-button-primary">
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.cta.button') }}
              <Icon name="arrowRight" size="sm" />
            </RouterLink>
            <RouterLink to="/docs" class="home-v2-button home-v2-button-secondary">
              <Icon name="book" size="sm" />
              {{ t('home.cta.guideButton') }}
            </RouterLink>
          </div>
        </header>

        <nav class="home-v2-resources home-v2-reveal" :aria-label="t('nav.mainNavigation')">
          <RouterLink to="/docs">
            <Icon name="book" size="md" />
            <span><strong>{{ t('home.guide') }}</strong><small>{{ t('home.experience.resources.docs') }}</small></span>
            <Icon name="chevronRight" size="sm" />
          </RouterLink>
          <RouterLink v-if="publicModelMarketEnabled" to="/models">
            <Icon name="cube" size="md" />
            <span><strong>{{ t('modelMarket.navLabel') }}</strong><small>{{ t('home.experience.resources.models') }}</small></span>
            <Icon name="chevronRight" size="sm" />
          </RouterLink>
          <RouterLink to="/monitor">
            <Icon name="chart" size="md" />
            <span><strong>{{ t('nav.modelStatus') }}</strong><small>{{ t('home.experience.resources.monitor') }}</small></span>
            <Icon name="chevronRight" size="sm" />
          </RouterLink>
          <RouterLink to="/key-usage">
            <Icon name="key" size="md" />
            <span><strong>{{ t('keyUsage.title') }}</strong><small>{{ t('home.experience.resources.usage') }}</small></span>
            <Icon name="chevronRight" size="sm" />
          </RouterLink>
          <RouterLink to="/status">
            <Icon name="server" size="md" />
            <span><strong>{{ t('nav.serviceStatus') }}</strong><small>{{ t('home.experience.resources.service') }}</small></span>
            <Icon name="chevronRight" size="sm" />
          </RouterLink>
        </nav>

        <footer class="home-v2-footer home-v2-reveal">
          <p>&copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}</p>
          <div>
            <RouterLink v-for="link in legalLinks" :key="link.to" :to="link.to">{{ link.label }}</RouterLink>
          </div>
        </footer>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import type { PublicMonitorTimelinePoint, PublicMonitorView } from '@/api/publicChannelMonitor'
import type { MonitorStatus } from '@/api/admin/channelMonitor'
import type { GroupPlatform } from '@/types'

type HomeIconName = InstanceType<typeof Icon>['$props']['name']

interface HomeCard {
  icon: HomeIconName
  title: string
  description: string
}

interface HomeWorkflowStep extends HomeCard {
  step: string
}

interface HomeChannel {
  name: string
  platform: GroupPlatform
  shortName: string
  description: string
  status: string
  markClass: string
  isCustom?: boolean
}

interface HomeLegalLink {
  to: string
  label: string
}

const props = defineProps<{
  siteName: string
  apiBaseUrl: string
  endpointPaths: readonly string[]
  isAuthenticated: boolean
  dashboardPath: string
  publicModelMarketEnabled: boolean
  modelCount: number | null
  heroFeatures: HomeCard[]
  valueCards: HomeCard[]
  workflowSteps: HomeWorkflowStep[]
  supportedChannels: HomeChannel[]
  publicMonitorItems: PublicMonitorView[]
  publicMonitorLoading: boolean
  legalLinks: HomeLegalLink[]
  currentYear: number
}>()

defineEmits<{
  'copy-base-url': []
}>()

const { t } = useI18n()
const { providerLabel, formatPercent } = useChannelMonitorFormat()
const rootRef = ref<HTMLElement | null>(null)
let revealObserver: IntersectionObserver | null = null

const modelCountLabel = computed(() => props.modelCount === null ? '—' : String(props.modelCount))
const coreChannels = computed(() => props.supportedChannels.filter((channel) => !channel.isCustom).slice(0, 4))
const visibleMonitors = computed(() => props.publicMonitorItems.slice(0, 4))
const hasMonitorIssue = computed(() => props.publicMonitorItems.some((item) => item.primary_status !== 'operational'))
const monitorSummary = computed(() => {
  if (props.publicMonitorLoading) return t('home.experience.status.loading')
  if (props.publicMonitorItems.length === 0) return t('home.experience.status.empty')
  return hasMonitorIssue.value
    ? t('home.experience.status.attention')
    : t('home.experience.status.operational')
})

function latencyLabel(latency: number | null): string {
  return latency === null ? '—' : `${Math.round(latency)} ms`
}

function monitorPoints(item: PublicMonitorView): PublicMonitorTimelinePoint[] {
  return (item.timeline || []).slice(-18)
}

function statusClass(status: MonitorStatus): string {
  return `is-${status}`
}

function revealAll(): void {
  rootRef.value?.querySelectorAll<HTMLElement>('.home-v2-screen').forEach((screen) => {
    screen.classList.add('is-visible')
  })
}

onMounted(() => {
  void nextTick(() => {
    const root = rootRef.value
    if (!root) return

    const screens = Array.from(root.querySelectorAll<HTMLElement>('.home-v2-screen'))
    screens[0]?.classList.add('is-visible')

    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches || typeof IntersectionObserver === 'undefined') {
      revealAll()
      return
    }

    revealObserver = new IntersectionObserver((entries) => {
      entries.forEach((entry) => {
        if (!entry.isIntersecting) return
        entry.target.classList.add('is-visible')
        revealObserver?.unobserve(entry.target)
      })
    }, {
      root: root.parentElement,
      threshold: 0.28,
    })

    screens.slice(1).forEach((screen) => revealObserver?.observe(screen))
  })
})

onBeforeUnmount(() => {
  revealObserver?.disconnect()
  revealObserver = null
})
</script>

<style scoped>
.home-v2 {
  --v2-text: #17191d;
  --v2-secondary: #626a74;
  --v2-tertiary: #8b929a;
  --v2-line: rgba(24, 32, 38, 0.12);
  --v2-line-strong: rgba(24, 32, 38, 0.2);
  --v2-surface: #ffffff;
  --v2-surface-muted: #f2f5f5;
  --v2-accent: #0f8a78;
  --v2-accent-hover: #0b7466;
  --v2-accent-soft: #e3f3ef;
  width: 100%;
  color: var(--v2-text);
  font-family: ui-sans-serif, -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", sans-serif;
  font-optical-sizing: auto;
  letter-spacing: 0;
}

.home-v2-screen {
  position: relative;
  display: flex;
  min-height: 100dvh;
  align-items: center;
  padding: 104px 24px 48px;
  scroll-snap-align: start;
  scroll-snap-stop: always;
}

.home-v2-shell {
  width: min(100%, 1180px);
  margin: 0 auto;
}

.home-v2-reveal {
  opacity: 0;
  transform: translateY(12px);
  transition: opacity 220ms ease-out, transform 220ms cubic-bezier(0.22, 1, 0.36, 1);
}

.home-v2-screen.is-visible .home-v2-reveal {
  opacity: 1;
  transform: translateY(0);
}

.home-v2-section-index {
  margin: 0 0 16px;
  color: var(--v2-accent);
  font-size: 12px;
  font-weight: 700;
  line-height: 1.4;
  letter-spacing: 0;
}

.home-v2 h1,
.home-v2 h2,
.home-v2 h3,
.home-v2 p,
.home-v2 dl,
.home-v2 dd,
.home-v2 ol {
  margin-top: 0;
}

.home-v2 h2 {
  margin-bottom: 18px;
  color: var(--v2-text);
  font-size: 40px;
  font-weight: 700;
  line-height: 1.12;
  letter-spacing: 0;
}

.home-v2-hero {
  background: linear-gradient(118deg, #e8f7f3 0%, #f9fbfc 48%, #ebf2f7 100%);
}

.home-v2-hero-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.08fr) minmax(390px, 0.92fr);
  align-items: center;
  gap: 68px;
}

.home-v2-title {
  position: relative;
  width: fit-content;
  margin: 0;
  overflow: hidden;
  color: var(--v2-text);
  font-size: 56px;
  font-weight: 700;
  line-height: 1.06;
  letter-spacing: 0;
}

.home-v2-title > span {
  display: block;
}

.home-v2-title strong {
  color: var(--v2-accent);
  font-weight: 700;
}

.home-v2-title::after {
  position: absolute;
  inset: -10% -35%;
  background: linear-gradient(105deg, transparent 34%, rgba(255, 255, 255, 0.78) 48%, transparent 62%);
  content: '';
  pointer-events: none;
  transform: translateX(-70%);
  animation: home-v2-title-sweep 7s cubic-bezier(0.22, 1, 0.36, 1) infinite;
}

.home-v2-lede {
  max-width: 610px;
  margin-bottom: 0;
  margin-top: 22px !important;
  color: var(--v2-secondary);
  font-size: 16px;
  line-height: 1.75;
}

.home-v2-endpoint {
  max-width: 620px;
  margin-top: 28px;
  padding: 12px;
  background: rgba(255, 255, 255, 0.88);
  border: 1px solid rgba(38, 71, 76, 0.14);
  border-radius: 8px;
  box-shadow: 0 12px 30px rgba(38, 71, 76, 0.07);
}

.home-v2-endpoint-label,
.home-v2-endpoint-label > span,
.home-v2-endpoint-line,
.home-v2-actions,
.home-v2-button,
.home-v2-text-link {
  display: flex;
  align-items: center;
}

.home-v2-endpoint-label {
  min-height: 24px;
  justify-content: space-between;
  padding: 0 4px 8px;
  color: var(--v2-secondary);
  font-size: 12px;
  font-weight: 600;
}

.home-v2-endpoint-label > span {
  gap: 8px;
}

.home-v2-endpoint-label i,
.home-v2-live-dot,
.home-v2-channel-state i,
.home-v2-status-signal i {
  width: 7px;
  height: 7px;
  flex: 0 0 auto;
  background: #16826c;
  border-radius: 50%;
  box-shadow: 0 0 0 4px rgba(22, 130, 108, 0.11);
}

.home-v2-endpoint-label strong {
  color: var(--v2-accent);
  font-size: 11px;
  font-weight: 700;
}

.home-v2-endpoint-line {
  min-height: 48px;
  gap: 12px;
  padding: 5px 5px 5px 12px;
  background: #edf2f3;
  border: 1px solid rgba(38, 71, 76, 0.11);
  border-radius: 7px;
}

.home-v2-endpoint-line code {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  color: #17222a;
  font-size: 14px;
  font-weight: 650;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-v2-endpoint-paths {
  display: inline-grid;
  max-width: 46%;
  min-width: 0;
  flex: 0 1 auto;
  place-items: center end;
  overflow: hidden;
  color: var(--v2-accent);
  font-size: 13px;
  font-weight: 700;
  line-height: 1.2;
}

.home-v2-endpoint-paths > span {
  grid-area: 1 / 1;
  max-width: 100%;
  overflow: hidden;
  opacity: 0;
  text-overflow: ellipsis;
  transform: translateY(8px);
  white-space: nowrap;
  animation: home-v2-path-cycle 13s infinite;
}

.home-v2-endpoint-line button {
  display: grid;
  width: 36px;
  height: 36px;
  flex: 0 0 36px;
  place-items: center;
  background: #d9e4e5;
  border-radius: 7px;
  color: #465d61;
  transition: background-color 140ms ease-out, color 140ms ease-out, transform 90ms ease-out;
}

.home-v2-endpoint-line button:hover {
  background: #cbdadc;
  color: #18383a;
}

.home-v2-endpoint-line button:active,
.home-v2-button:active,
.home-v2-text-link:active,
.home-v2-resources a:active {
  transform: scale(0.97);
}

.home-v2-actions {
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 22px;
}

.home-v2-button {
  min-height: 40px;
  justify-content: center;
  gap: 8px;
  padding: 0 16px;
  border: 1px solid transparent;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 650;
  text-decoration: none;
  transition: background-color 150ms ease-out, border-color 150ms ease-out, color 150ms ease-out, transform 90ms ease-out;
}

.home-v2-button-primary {
  background: var(--v2-accent);
  color: #fff;
  box-shadow: 0 10px 22px rgba(15, 138, 120, 0.2);
}

.home-v2-button-primary:hover {
  background: var(--v2-accent-hover);
}

.home-v2-button-secondary {
  background: rgba(255, 255, 255, 0.8);
  border-color: var(--v2-line);
  color: var(--v2-text);
}

.home-v2-button-secondary:hover {
  background: #fff;
  border-color: var(--v2-line-strong);
}

.home-v2-proof {
  display: grid;
  max-width: 620px;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin-bottom: 0;
  margin-top: 22px;
  border-top: 1px solid var(--v2-line);
}

.home-v2-proof > div {
  display: grid;
  min-height: 58px;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  padding-right: 16px;
  border-right: 1px solid var(--v2-line);
}

.home-v2-proof > div + div {
  padding-left: 16px;
}

.home-v2-proof > div:last-child {
  border-right: 0;
}

.home-v2-proof dt {
  color: var(--v2-secondary);
  font-size: 11px;
  font-weight: 600;
}

.home-v2-proof dd {
  margin-bottom: 0;
  color: var(--v2-text);
  font-size: 18px;
  font-weight: 700;
}

.home-v2-routing {
  overflow: hidden;
  background: rgba(246, 250, 249, 0.95);
  border: 1px solid rgba(22, 130, 108, 0.15);
  border-radius: 8px;
  box-shadow: 0 20px 46px rgba(38, 71, 76, 0.12);
}

.home-v2-routing > header,
.home-v2-routing > header > div,
.home-v2-routing > footer,
.home-v2-routing > footer > div,
.home-v2-routing-row {
  display: flex;
  align-items: center;
}

.home-v2-routing > header {
  min-height: 46px;
  justify-content: space-between;
  padding: 0 16px;
  border-bottom: 1px solid var(--v2-line);
}

.home-v2-routing > header > div {
  gap: 10px;
}

.home-v2-routing > header p,
.home-v2-routing > header > span {
  margin-bottom: 0;
  color: var(--v2-secondary);
  font-size: 11px;
  font-weight: 700;
}

.home-v2-routing > header > span {
  color: var(--v2-accent);
}

.home-v2-routing-title {
  padding: 20px 18px 14px;
}

.home-v2-routing-title h2 {
  margin-bottom: 8px;
  font-size: 22px;
}

.home-v2-routing-title p {
  margin-bottom: 0;
  color: var(--v2-secondary);
  font-size: 13px;
  line-height: 1.6;
}

.home-v2-routing-list {
  padding: 0 18px;
}

.home-v2-routing-row {
  position: relative;
  min-height: 78px;
  gap: 12px;
  border-top: 1px solid var(--v2-line);
  animation: home-v2-row-float 7.2s ease-in-out infinite;
  will-change: transform;
}

.home-v2-routing-row-2 {
  margin-left: 13px;
  animation-delay: -2.4s;
}

.home-v2-routing-row-3 {
  margin-left: 4px;
  animation-delay: -4.8s;
}

.home-v2-routing-row > span {
  display: grid;
  width: 34px;
  height: 34px;
  flex: 0 0 34px;
  place-items: center;
  background: #e6f1f4;
  border: 1px solid rgba(57, 116, 214, 0.13);
  border-radius: 8px;
  color: #3974d6;
}

.home-v2-routing-row > div {
  min-width: 0;
  flex: 1;
}

.home-v2-routing-row h3 {
  margin-bottom: 5px;
  color: var(--v2-text);
  font-size: 14px;
  font-weight: 700;
}

.home-v2-routing-row p {
  margin-bottom: 0;
  overflow: hidden;
  color: var(--v2-secondary);
  font-size: 11px;
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-v2-routing-row > i {
  width: 7px;
  height: 7px;
  flex: 0 0 auto;
  background: #16826c;
  border-radius: 50%;
  box-shadow: 0 0 0 4px rgba(22, 130, 108, 0.1);
}

.home-v2-routing > footer {
  min-height: 52px;
  justify-content: space-between;
  padding: 0 18px;
  background: rgba(233, 241, 240, 0.68);
  border-top: 1px solid var(--v2-line);
}

.home-v2-routing > footer > span {
  color: var(--v2-secondary);
  font-size: 11px;
  font-weight: 650;
}

.home-v2-routing > footer > div {
  gap: 6px;
}

.home-v2-provider-logo,
.home-v2-channel-name i {
  display: grid;
  width: 26px;
  height: 26px;
  place-items: center;
  border-radius: 6px;
  color: #fff;
  font-size: 10px;
  font-style: normal;
  font-weight: 750;
}

.home-v2-provider-logo {
  transition: transform 100ms ease-out;
}

.home-v2-provider-logo:active {
  transform: scale(0.94);
}

.home-provider-claude { background: #b85c38; }
.home-provider-gpt { background: #16826c; }
.home-provider-gemini { background: #3974d6; }
.home-provider-antigravity { background: #7251c8; }
.home-provider-custom { background: #5f6368; }

.home-v2-capabilities {
  background: #fff;
}

.home-v2-split {
  display: grid;
  grid-template-columns: minmax(340px, 0.82fr) minmax(0, 1.18fr);
  align-items: center;
  gap: 76px;
}

.home-v2-section-heading {
  max-width: 470px;
}

.home-v2-section-heading > p:not(.home-v2-section-index),
.home-v2-wide-heading > p,
.home-v2-model-summary > p:not(.home-v2-section-index):not(.home-v2-model-number),
.home-v2-status-copy > p:not(.home-v2-section-index),
.home-v2-final-heading p {
  margin-bottom: 0;
  color: var(--v2-secondary);
  font-size: 16px;
  line-height: 1.75;
}

.home-v2-heading-rule {
  height: 1px;
  margin-top: 30px;
  background: var(--v2-line);
}

.home-v2-heading-rule span {
  display: block;
  width: 72px;
  height: 2px;
  background: var(--v2-accent);
}

.home-v2-capability-ledger {
  border-top: 1px solid var(--v2-line-strong);
}

.home-v2-capability-ledger article {
  display: grid;
  min-height: 94px;
  grid-template-columns: 32px 38px minmax(0, 1fr) 18px;
  align-items: center;
  gap: 14px;
  border-bottom: 1px solid var(--v2-line);
  transition: padding-left 150ms ease-out, background-color 150ms ease-out;
}

.home-v2-capability-ledger article:hover {
  padding-left: 8px;
  background: #f8faf9;
}

.home-v2-row-number {
  color: var(--v2-tertiary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  font-weight: 650;
}

.home-v2-row-icon,
.home-v2-workflow-icon {
  display: grid;
  width: 38px;
  height: 38px;
  place-items: center;
  background: var(--v2-accent-soft);
  border-radius: 8px;
  color: var(--v2-accent);
}

.home-v2-capability-ledger h3 {
  margin-bottom: 5px;
  color: var(--v2-text);
  font-size: 16px;
  font-weight: 700;
}

.home-v2-capability-ledger p {
  margin-bottom: 0;
  color: var(--v2-secondary);
  font-size: 13px;
  line-height: 1.55;
}

.home-v2-row-arrow {
  color: var(--v2-tertiary);
}

.home-v2-workflow {
  background: #f3f5f7;
}

.home-v2-wide-heading {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(300px, 0.62fr);
  align-items: end;
  gap: 64px;
}

.home-v2-wide-heading h2 {
  margin-bottom: 0;
  text-wrap: balance;
}

.home-v2 .home-v2-workflow-rail {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin-bottom: 0;
  margin-top: 48px;
  padding: 0;
  border-top: 1px solid var(--v2-line-strong);
  list-style: none;
}

.home-v2-workflow-rail li {
  position: relative;
  min-height: 230px;
  padding: 28px 28px 24px 0;
  border-right: 1px solid var(--v2-line);
}

.home-v2-workflow-rail li + li {
  padding-left: 28px;
}

.home-v2-workflow-rail li:last-child {
  border-right: 0;
}

.home-v2-workflow-rail li > span {
  position: absolute;
  top: -10px;
  left: 0;
  padding-right: 10px;
  background: #f3f5f7;
  color: var(--v2-accent);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  font-weight: 700;
}

.home-v2-workflow-rail li + li > span {
  left: 28px;
}

.home-v2-workflow-icon {
  margin-top: 12px;
}

.home-v2-workflow-rail h3 {
  margin-bottom: 10px;
  margin-top: 24px;
  font-size: 18px;
  font-weight: 700;
}

.home-v2-workflow-rail p {
  margin-bottom: 0;
  color: var(--v2-secondary);
  font-size: 13px;
  line-height: 1.65;
}

.home-v2-config {
  display: grid;
  min-height: 76px;
  grid-template-columns: minmax(260px, 0.8fr) minmax(0, 1.2fr) auto;
  align-items: center;
  gap: 24px;
  margin-top: 18px;
  padding: 12px 14px 12px 18px;
  overflow: hidden;
  background: #17191d;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  color: #f5f5f7;
  box-shadow: 0 16px 34px rgba(15, 23, 32, 0.14);
}

.home-v2-config-address span {
  display: block;
  margin-bottom: 6px;
  color: #858b93;
  font-size: 10px;
  font-weight: 700;
}

.home-v2-config-address code {
  display: block;
  overflow: hidden;
  font-size: 13px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-v2-config-protocols {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: 6px;
}

.home-v2-config-protocols span {
  max-width: 100%;
  padding: 5px 7px;
  overflow: hidden;
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 6px;
  color: #b4bac2;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-v2-config > a {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  gap: 7px;
  padding: 0 12px;
  border-radius: 7px;
  color: #6ed2bd;
  font-size: 12px;
  font-weight: 700;
  text-decoration: none;
}

.home-v2-config > a:hover {
  background: rgba(255, 255, 255, 0.07);
}

.home-v2-models {
  background: #ebf5f2;
}

.home-v2-models-grid {
  display: grid;
  grid-template-columns: minmax(260px, 0.62fr) minmax(0, 1.38fr);
  align-items: center;
  gap: 80px;
}

.home-v2-model-number {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 8px;
  color: var(--v2-accent);
  font-size: 72px;
  font-weight: 700;
  line-height: 1;
}

.home-v2-model-number strong {
  font: inherit;
}

.home-v2-model-number span {
  color: var(--v2-secondary);
  font-size: 12px;
  font-weight: 650;
}

.home-v2-model-summary h2 {
  font-size: 36px;
}

.home-v2-text-link {
  width: fit-content;
  min-height: 38px;
  gap: 7px;
  margin-top: 22px;
  padding: 0 8px;
  border-radius: 7px;
  color: var(--v2-accent);
  font-size: 13px;
  font-weight: 700;
  text-decoration: none;
  transition: background-color 140ms ease-out, transform 90ms ease-out;
}

.home-v2-text-link:hover {
  background: rgba(15, 138, 120, 0.08);
}

.home-v2-channel-ledger {
  background: rgba(255, 255, 255, 0.58);
  border: 1px solid rgba(22, 130, 108, 0.13);
  border-radius: 8px;
  box-shadow: 0 16px 36px rgba(38, 71, 76, 0.075);
}

.home-v2-ledger-head,
.home-v2-channel-ledger article {
  display: grid;
  grid-template-columns: minmax(150px, 0.72fr) minmax(0, 1.28fr) 110px;
  align-items: center;
  gap: 16px;
  padding: 0 18px;
}

.home-v2-ledger-head {
  min-height: 38px;
  color: var(--v2-tertiary);
  font-size: 10px;
  font-weight: 700;
}

.home-v2-channel-ledger article {
  min-height: 70px;
  border-top: 1px solid var(--v2-line);
}

.home-v2-channel-name {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.home-v2-channel-name strong {
  overflow: hidden;
  font-size: 14px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-v2-channel-ledger article > p {
  margin-bottom: 0;
  overflow: hidden;
  color: var(--v2-secondary);
  font-size: 12px;
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-v2-channel-state {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: #16826c;
  font-size: 11px;
  font-weight: 700;
}

.home-v2-status {
  --v2-text: #f4f6f8;
  --v2-secondary: #aeb7c2;
  --v2-tertiary: #79838e;
  --v2-line: rgba(255, 255, 255, 0.1);
  --v2-line-strong: rgba(255, 255, 255, 0.17);
  --v2-accent: #69d3bd;
  background: #171a1e;
  color: var(--v2-text);
}

.home-v2-status-grid {
  display: grid;
  grid-template-columns: minmax(280px, 0.68fr) minmax(0, 1.32fr);
  align-items: center;
  gap: 76px;
}

.home-v2-status-copy h2 {
  margin-top: 24px;
  color: var(--v2-text);
}

.home-v2-status-signal {
  display: inline-flex;
  min-height: 30px;
  align-items: center;
  gap: 9px;
  padding: 0 10px;
  background: rgba(72, 201, 130, 0.09);
  border: 1px solid rgba(72, 201, 130, 0.18);
  border-radius: 7px;
  color: #74d99e;
  font-size: 11px;
  font-weight: 700;
}

.home-v2-status-signal.is-warning {
  background: rgba(255, 173, 66, 0.1);
  border-color: rgba(255, 173, 66, 0.2);
  color: #ffb85c;
}

.home-v2-status-signal.is-warning i {
  background: #ffad42;
  box-shadow: 0 0 0 4px rgba(255, 173, 66, 0.12);
}

.home-v2-button-light {
  background: #f4f6f8;
  color: #171a1e;
}

.home-v2-button-light:hover {
  background: #fff;
}

.home-v2-text-link-light {
  color: #69d3bd;
}

.home-v2-text-link-light:hover {
  background: rgba(105, 211, 189, 0.09);
}

.home-v2-monitor {
  overflow: hidden;
  background: #20242a;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  box-shadow: 0 22px 48px rgba(0, 0, 0, 0.28);
}

.home-v2-monitor-head,
.home-v2-monitor article {
  display: grid;
  grid-template-columns: minmax(170px, 0.9fr) minmax(120px, 1.1fr) 90px 70px;
  align-items: center;
  gap: 16px;
  padding: 0 18px;
}

.home-v2-monitor-head {
  min-height: 42px;
  color: #77818d;
  font-size: 10px;
  font-weight: 700;
}

.home-v2-monitor-head span:nth-child(2) {
  grid-column: 3;
}

.home-v2-monitor-head span:nth-child(3) {
  grid-column: 4;
}

.home-v2-monitor article {
  min-height: 82px;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
}

.home-v2-monitor-name {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.home-v2-monitor-name > span {
  display: grid;
  width: 30px;
  height: 30px;
  flex: 0 0 30px;
  place-items: center;
  border-radius: 7px;
  color: #fff;
  font-size: 0;
}

.home-v2-monitor-name > span::first-letter {
  font-size: 11px;
}

.home-v2-monitor-name > span.is-openai { background: #16826c; }
.home-v2-monitor-name > span.is-anthropic { background: #b85c38; }
.home-v2-monitor-name > span.is-gemini { background: #3974d6; }
.home-v2-monitor-name > span.is-grok { background: #5f6368; }

.home-v2-monitor-name > div {
  min-width: 0;
}

.home-v2-monitor-name strong,
.home-v2-monitor-name small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-v2-monitor-name strong {
  color: #f4f6f8;
  font-size: 13px;
  font-weight: 700;
}

.home-v2-monitor-name small {
  margin-top: 4px;
  color: #858e99;
  font-size: 10px;
}

.home-v2-monitor-timeline {
  display: grid;
  grid-template-columns: repeat(18, minmax(2px, 1fr));
  gap: 3px;
}

.home-v2-monitor-timeline i {
  height: 18px;
  background: rgba(255, 255, 255, 0.08);
  border-radius: 2px;
}

.home-v2-monitor-timeline i.is-operational { background: #48c982; }
.home-v2-monitor-timeline i.is-degraded { background: #ffad42; }
.home-v2-monitor-timeline i.is-failed,
.home-v2-monitor-timeline i.is-error { background: #ef5a5a; }

.home-v2-monitor article > strong {
  color: #8fdaa9;
  font-size: 12px;
  font-weight: 700;
}

.home-v2-monitor article > span {
  color: #b2bac4;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
}

.home-v2-monitor-empty {
  display: flex;
  min-height: 230px;
  align-items: center;
  justify-content: center;
  gap: 10px;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
  color: #858e99;
  font-size: 13px;
}

.home-v2-monitor-spinner {
  width: 15px;
  height: 15px;
  border: 2px solid rgba(255, 255, 255, 0.15);
  border-top-color: #69d3bd;
  border-radius: 50%;
  animation: home-v2-spin 0.8s linear infinite;
}

.home-v2-final {
  background: #e9f0f3;
}

.home-v2-final-layout {
  display: grid;
  align-content: center;
  gap: 42px;
}

.home-v2-final-heading {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  gap: 48px;
}

.home-v2-final-heading > div:first-child {
  max-width: 680px;
}

.home-v2-final-heading h2 {
  margin-bottom: 14px;
  font-size: 44px;
}

.home-v2-final-heading .home-v2-actions {
  justify-content: flex-end;
  margin-top: 0;
}

.home-v2-resources {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  border-top: 1px solid var(--v2-line-strong);
}

.home-v2-resources a {
  display: grid;
  min-height: 94px;
  grid-template-columns: 34px minmax(0, 1fr) 18px;
  align-items: center;
  gap: 14px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--v2-line);
  color: var(--v2-text);
  text-decoration: none;
  transition: background-color 150ms ease-out, transform 90ms ease-out;
}

.home-v2-resources a:nth-child(odd) {
  border-right: 1px solid var(--v2-line);
}

.home-v2-resources a:last-child:nth-child(odd) {
  grid-column: 1 / -1;
  border-right: 0;
}

.home-v2-resources a:hover {
  background: rgba(255, 255, 255, 0.48);
}

.home-v2-resources a > svg:first-child {
  color: var(--v2-accent);
}

.home-v2-resources a > svg:last-child {
  color: var(--v2-tertiary);
}

.home-v2-resources strong,
.home-v2-resources small {
  display: block;
}

.home-v2-resources strong {
  font-size: 14px;
  font-weight: 700;
}

.home-v2-resources small {
  margin-top: 5px;
  color: var(--v2-secondary);
  font-size: 11px;
  line-height: 1.5;
}

.home-v2-footer {
  display: flex;
  min-height: 52px;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  border-top: 1px solid var(--v2-line);
}

.home-v2-footer p {
  margin-bottom: 0;
  color: var(--v2-secondary);
  font-size: 11px;
}

.home-v2-footer > div {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 14px;
}

.home-v2-footer a {
  color: var(--v2-secondary);
  font-size: 11px;
  text-decoration: none;
}

.home-v2-footer a:hover {
  color: var(--v2-text);
}

@keyframes home-v2-title-sweep {
  0%, 24% { transform: translateX(-70%); }
  72%, 100% { transform: translateX(70%); }
}

@keyframes home-v2-path-cycle {
  0%, 16% { opacity: 1; transform: translateY(0); }
  20%, 96% { opacity: 0; transform: translateY(-8px); }
  100% { opacity: 0; transform: translateY(8px); }
}

@keyframes home-v2-row-float {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-5px); }
}

@keyframes home-v2-spin {
  to { transform: rotate(360deg); }
}

:global(.home-dark .home-v2) {
  --v2-text: #f4f6f8;
  --v2-secondary: #aeb7c2;
  --v2-tertiary: #7f8994;
  --v2-line: rgba(255, 255, 255, 0.1);
  --v2-line-strong: rgba(255, 255, 255, 0.16);
  --v2-surface: #20242a;
  --v2-surface-muted: #262b32;
  --v2-accent: #69d3bd;
  --v2-accent-hover: #78ddc7;
  --v2-accent-soft: rgba(105, 211, 189, 0.12);
}

:global(.home-dark .home-v2-hero),
:global(.home-dark .home-v2-capabilities),
:global(.home-dark .home-v2-workflow),
:global(.home-dark .home-v2-models),
:global(.home-dark .home-v2-final) {
  background: #15181c;
}

:global(.home-dark .home-v2-capabilities),
:global(.home-dark .home-v2-models) {
  background: #191d22;
}

:global(.home-dark .home-v2-endpoint),
:global(.home-dark .home-v2-routing),
:global(.home-dark .home-v2-channel-ledger) {
  background: #20242a;
  border-color: rgba(255, 255, 255, 0.1);
}

:global(.home-dark .home-v2-endpoint-line),
:global(.home-dark .home-v2-routing > footer) {
  background: #262b32;
}

:global(.home-dark .home-v2-endpoint-line code),
:global(.home-dark .home-v2-capability-ledger h3),
:global(.home-dark .home-v2-workflow-rail h3),
:global(.home-dark .home-v2-channel-name strong) {
  color: #f4f6f8;
}

:global(.home-dark .home-v2-button-secondary) {
  background: #23282e;
  border-color: rgba(255, 255, 255, 0.12);
  color: #f4f6f8;
}

:global(.home-dark .home-v2-workflow-rail li > span) {
  background: #15181c;
}

@media (max-width: 1023px) {
  .home-v2-screen {
    align-items: flex-start;
    padding: 96px 20px 44px;
  }

  .home-v2-hero-grid,
  .home-v2-split,
  .home-v2-models-grid,
  .home-v2-status-grid {
    grid-template-columns: 1fr;
    gap: 42px;
  }

  .home-v2-hero-grid {
    align-items: start;
  }

  .home-v2-title {
    font-size: 48px;
  }

  .home-v2-routing {
    width: min(100%, 720px);
  }

  .home-v2-section-heading,
  .home-v2-model-summary,
  .home-v2-status-copy {
    max-width: 680px;
  }

  .home-v2-workflow-rail li {
    min-height: 210px;
  }

  .home-v2-model-number {
    font-size: 58px;
  }
}

@media (max-width: 767px) {
  .home-v2-screen {
    padding: 84px 16px 34px;
  }

  .home-v2 h2,
  .home-v2-final-heading h2 {
    font-size: 32px;
  }

  .home-v2-title {
    font-size: 36px;
    line-height: 1.1;
  }

  .home-v2-lede,
  .home-v2-section-heading > p:not(.home-v2-section-index),
  .home-v2-wide-heading > p,
  .home-v2-model-summary > p:not(.home-v2-section-index):not(.home-v2-model-number),
  .home-v2-status-copy > p:not(.home-v2-section-index),
  .home-v2-final-heading p {
    font-size: 14px;
  }

  .home-v2-endpoint-line {
    align-items: flex-start;
    flex-wrap: wrap;
    padding: 10px;
  }

  .home-v2-endpoint-line code,
  .home-v2-endpoint-paths {
    width: calc(100% - 48px);
    max-width: none;
  }

  .home-v2-endpoint-paths {
    order: 3;
    place-items: center start;
  }

  .home-v2-endpoint-line button {
    margin-left: auto;
  }

  .home-v2-proof {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .home-v2-proof > div,
  .home-v2-proof > div + div {
    min-height: 58px;
    grid-template-columns: 1fr;
    align-content: center;
    gap: 4px;
    padding: 0 10px;
    border-right: 1px solid var(--v2-line);
    border-bottom: 0;
  }

  .home-v2-proof > div:first-child {
    padding-left: 0;
  }

  .home-v2-proof > div:last-child {
    padding-right: 0;
    border-right: 0;
  }

  .home-v2-proof dd {
    grid-row: 1;
    font-size: 16px;
  }

  .home-v2-proof dt {
    grid-row: 2;
    font-size: 10px;
  }

  .home-v2-routing-row-2,
  .home-v2-routing-row-3 {
    margin-left: 0;
  }

  .home-v2-routing-title p,
  .home-v2-routing-row p {
    display: none;
  }

  .home-v2-routing-row {
    min-height: 64px;
  }

  .home-v2-wide-heading,
  .home-v2-final-heading {
    grid-template-columns: 1fr;
    align-items: start;
    gap: 22px;
  }

  .home-v2 .home-v2-workflow-rail {
    grid-template-columns: 1fr;
    margin-top: 38px;
    border-top: 0;
    border-left: 1px solid var(--v2-line-strong);
  }

  .home-v2-workflow-rail li,
  .home-v2-workflow-rail li + li {
    min-height: 0;
    padding: 16px 0 24px 24px;
    border-right: 0;
    border-bottom: 1px solid var(--v2-line);
  }

  .home-v2-workflow-rail li > span,
  .home-v2-workflow-rail li + li > span {
    top: 22px;
    left: -11px;
    width: 22px;
    padding: 3px 0;
    background: #f3f5f7;
    text-align: center;
  }

  .home-v2-config {
    grid-template-columns: 1fr;
    gap: 14px;
  }

  .home-v2-ledger-head {
    display: none;
  }

  .home-v2-channel-ledger article {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 8px 14px;
    padding: 14px;
  }

  .home-v2-channel-ledger article > p {
    grid-column: 1 / -1;
    white-space: normal;
  }

  .home-v2-monitor-head {
    display: none;
  }

  .home-v2-monitor article {
    grid-template-columns: minmax(0, 1fr) auto auto;
    gap: 10px;
    padding: 14px;
  }

  .home-v2-monitor-timeline {
    grid-column: 1 / -1;
    grid-row: 2;
  }

  .home-v2-final-heading .home-v2-actions {
    justify-content: flex-start;
  }

  .home-v2-resources {
    grid-template-columns: 1fr;
  }

  .home-v2-resources a:nth-child(odd) {
    border-right: 0;
  }

  .home-v2-footer {
    align-items: flex-start;
    flex-direction: column;
    padding-top: 16px;
  }

  .home-v2-footer > div {
    justify-content: flex-start;
  }
}

@media (max-height: 760px) and (min-width: 1024px) {
  .home-v2-screen {
    padding-top: 82px;
    padding-bottom: 28px;
  }

  .home-v2-title {
    font-size: 48px;
  }

  .home-v2-lede {
    margin-top: 14px !important;
  }

  .home-v2-endpoint {
    margin-top: 18px;
  }

  .home-v2-proof {
    margin-top: 14px;
  }

  .home-v2 .home-v2-workflow-rail {
    margin-top: 34px;
  }

  .home-v2-workflow-rail li {
    min-height: 190px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .home-v2-reveal {
    opacity: 1;
    transform: none;
    transition: none;
  }

  .home-v2-title::after,
  .home-v2-endpoint-paths > span,
  .home-v2-routing-row,
  .home-v2-monitor-spinner {
    animation: none;
  }

  .home-v2-endpoint-paths > span:first-child {
    opacity: 1;
    transform: none;
  }
}

@media (prefers-reduced-transparency: reduce) {
  .home-v2-endpoint,
  .home-v2-routing,
  .home-v2-channel-ledger {
    background: var(--v2-surface);
    backdrop-filter: none;
  }
}
</style>
