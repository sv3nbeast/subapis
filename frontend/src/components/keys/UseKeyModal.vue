<template>
  <BaseDialog
    :show="show"
    :title="t('keys.useKeyModal.title')"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <!-- No Group Assigned Warning -->
      <div v-if="!platform" class="flex items-start gap-3 p-4 rounded-lg bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800">
        <svg class="w-5 h-5 text-yellow-500 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
        </svg>
        <div>
          <p class="text-sm font-medium text-yellow-800 dark:text-yellow-200">
            {{ t('keys.useKeyModal.noGroupTitle') }}
          </p>
          <p class="text-sm text-yellow-700 dark:text-yellow-300 mt-1">
            {{ t('keys.useKeyModal.noGroupDescription') }}
          </p>
        </div>
      </div>

      <!-- Platform-specific content -->
      <template v-else>
        <!-- Description -->
        <p class="text-sm text-gray-600 dark:text-gray-400">
          {{ platformDescription }}
        </p>

        <!-- Client Tabs -->
        <div v-if="clientTabs.length" class="border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex space-x-6" aria-label="Client">
            <button
              v-for="tab in clientTabs"
              :key="tab.id"
              @click="activeClientTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                activeClientTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <component :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <!-- OS/Shell Tabs -->
        <div v-if="showShellTabs" class="border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex space-x-4" aria-label="Tabs">
            <button
              v-for="tab in currentTabs"
              :key="tab.id"
              @click="activeTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                activeTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <component :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <!-- Code Blocks (Stacked for multi-file platforms) -->
        <div class="space-y-4">
          <div
            v-for="(file, index) in currentFiles"
            :key="index"
            class="relative"
          >
            <!-- File Hint (if exists) -->
            <p v-if="file.hint" class="text-xs text-amber-600 dark:text-amber-400 mb-1.5 flex items-center gap-1">
              <Icon name="exclamationCircle" size="sm" class="flex-shrink-0" />
              {{ file.hint }}
            </p>
            <div class="bg-gray-900 dark:bg-dark-900 rounded-xl overflow-hidden">
              <!-- Code Header -->
              <div class="flex items-center justify-between px-4 py-2 bg-gray-800 dark:bg-dark-800 border-b border-gray-700 dark:border-dark-700">
                <span class="text-xs text-gray-400 font-mono">{{ file.path }}</span>
                <button
                  @click="copyContent(file.content, index)"
                  class="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-lg transition-colors"
                  :class="copiedIndex === index
                    ? 'bg-green-500/20 text-green-400'
                    : 'bg-gray-700 hover:bg-gray-600 text-gray-300 hover:text-white'"
                >
                  <svg v-if="copiedIndex === index" class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  <svg v-else class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M15.666 3.888A2.25 2.25 0 0013.5 2.25h-3c-1.03 0-1.9.693-2.166 1.638m7.332 0c.055.194.084.4.084.612v0a.75.75 0 01-.75.75H9a.75.75 0 01-.75-.75v0c0-.212.03-.418.084-.612m7.332 0c.646.049 1.288.11 1.927.184 1.1.128 1.907 1.077 1.907 2.185V19.5a2.25 2.25 0 01-2.25 2.25H6.75A2.25 2.25 0 014.5 19.5V6.257c0-1.108.806-2.057 1.907-2.185a48.208 48.208 0 011.927-.184" />
                  </svg>
                  {{ copiedIndex === index ? t('keys.useKeyModal.copied') : t('keys.useKeyModal.copy') }}
                </button>
              </div>
              <!-- Code Content -->
              <pre class="p-4 text-sm font-mono text-gray-100 overflow-x-auto"><code v-if="file.highlighted" v-html="file.highlighted"></code><code v-else v-text="file.content"></code></pre>
            </div>
          </div>
        </div>

        <!-- Usage Note -->
        <div v-if="showPlatformNote" class="flex items-start gap-3 p-3 rounded-lg bg-blue-50 dark:bg-blue-900/20 border border-blue-100 dark:border-blue-800">
          <Icon name="infoCircle" size="md" class="text-blue-500 flex-shrink-0 mt-0.5" />
          <p class="text-sm text-blue-700 dark:text-blue-300">
            {{ platformNote }}
          </p>
        </div>
      </template>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button
          @click="emit('close')"
          class="btn btn-secondary"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, computed, h, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import type {
  APIKeyUsageClientTemplate,
  APIKeyUsageConfig,
  APIKeyUsageTemplateProfile,
  GroupPlatform
} from '@/types'

interface Props {
  show: boolean
  apiKey: string
  baseUrl: string
  platform: GroupPlatform | null
  groupId?: number | null
  groupName?: string
  claudeCodeOnly?: boolean
  allowMessagesDispatch?: boolean
  usageConfig?: Partial<APIKeyUsageConfig>
}

interface Emits {
  (e: 'close'): void
}

interface TabConfig {
  id: string
  label: string
  icon: Component
}

interface ClientTabConfig extends TabConfig {
  kind: string
  sortOrder: number
  custom?: APIKeyUsageClientTemplate
}

interface FileConfig {
  path: string
  content: string
  hint?: string  // Optional hint message for this file
  highlighted?: string
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const { copyToClipboard: clipboardCopy } = useClipboard()

const copiedIndex = ref<number | null>(null)
const activeTab = ref<string>('unix')
const activeClientTab = ref<string>('claude')
const DEFAULT_USAGE_CONFIG: APIKeyUsageConfig = {
  claude_code_default_model: 'claude-opus-4-7',
  claude_code_disable_nonessential_traffic: true,
  claude_code_attribution_header: 0,
  gemini_cli_default_model: 'gemini-2.0-flash',
  codex_model: 'gpt-5.5',
  codex_review_model: 'gpt-5.5',
  codex_reasoning_effort: 'xhigh',
  codex_disable_response_storage: true,
  codex_network_access: 'enabled',
  codex_goals_enabled: true,
  codex_websocket_enabled: true,
  codex_include_legacy_ws_feature: false,
  codex_extra_config: '',
  group_templates: [],
  template_profiles: []
}
const usageConfig = computed<APIKeyUsageConfig>(() => ({
  ...DEFAULT_USAGE_CONFIG,
  ...(props.usageConfig || {})
}))

// Reset tabs when platform changes
const defaultClientTab = computed(() => {
  switch (props.platform) {
    case 'openai':
      return 'codex'
    case 'gemini':
      return 'gemini'
    case 'antigravity':
      return 'claude'
    default:
      return 'claude'
  }
})

watch(() => [props.platform, props.groupId, props.claudeCodeOnly], () => {
  activeTab.value = 'unix'
  activeClientTab.value = defaultClientTab.value
}, { immediate: true })

// Reset shell tab when client changes
watch(activeClientTab, () => {
  activeTab.value = 'unix'
})

// Icon components
const AppleIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.81-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.38 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z' })
    ])
  }
}

const WindowsIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M3 12V6.75l6-1.32v6.48L3 12zm17-9v8.75l-10 .15V5.21L20 3zM3 13l6 .09v6.81l-6-1.15V13zm7 .25l10 .15V21l-10-1.91v-5.84z' })
    ])
  }
}

// Terminal icon for Claude Code
const TerminalIcon = {
  render() {
    return h('svg', {
      fill: 'none',
      stroke: 'currentColor',
      viewBox: '0 0 24 24',
      'stroke-width': '1.5',
      class: 'w-4 h-4'
    }, [
      h('path', {
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
        d: 'm6.75 7.5 3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0 0 21 17.25V6.75A2.25 2.25 0 0 0 18.75 4.5H5.25A2.25 2.25 0 0 0 3 6.75v10.5A2.25 2.25 0 0 0 5.25 20.25Z'
      })
    ])
  }
}

// Sparkle icon for Gemini
const SparkleIcon = {
  render() {
    return h('svg', {
      fill: 'none',
      stroke: 'currentColor',
      viewBox: '0 0 24 24',
      'stroke-width': '1.5',
      class: 'w-4 h-4'
    }, [
      h('path', {
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
        d: 'M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09ZM18.259 8.715 18 9.75l-.259-1.035a3.375 3.375 0 0 0-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 0 0 2.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 0 0 2.456 2.456L21.75 6l-1.035.259a3.375 3.375 0 0 0-2.456 2.456ZM16.894 20.567 16.5 21.75l-.394-1.183a2.25 2.25 0 0 0-1.423-1.423L13.5 18.75l1.183-.394a2.25 2.25 0 0 0 1.423-1.423l.394-1.183.394 1.183a2.25 2.25 0 0 0 1.423 1.423l1.183.394-1.183.394a2.25 2.25 0 0 0-1.423 1.423Z'
      })
    ])
  }
}

const builtInClientTabs = computed((): ClientTabConfig[] => {
  if (!props.platform) return []
  switch (props.platform) {
    case 'openai': {
      const tabs: ClientTabConfig[] = [
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: TerminalIcon, kind: 'codex', sortOrder: 10 }
      ]
      if (usageConfig.value.codex_websocket_enabled) {
        tabs.push({ id: 'codex-ws', label: t('keys.useKeyModal.cliTabs.codexCliWs'), icon: TerminalIcon, kind: 'codex', sortOrder: 20 })
      }
      if (props.allowMessagesDispatch) {
        tabs.push({ id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon, kind: 'claude_code', sortOrder: 30 })
      }
      tabs.push({ id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon, kind: 'opencode', sortOrder: 40 })
      return tabs
    }
    case 'gemini':
      return [
        { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: SparkleIcon, kind: 'gemini_cli', sortOrder: 10 },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon, kind: 'opencode', sortOrder: 20 }
      ]
    case 'antigravity':
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon, kind: 'claude_code', sortOrder: 10 },
        { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: SparkleIcon, kind: 'gemini_cli', sortOrder: 20 },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon, kind: 'opencode', sortOrder: 30 }
      ]
    default:
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon, kind: 'claude_code', sortOrder: 10 },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon, kind: 'opencode', sortOrder: 20 }
      ]
  }
})

function profileMatches(profile: APIKeyUsageTemplateProfile): boolean {
  if (!profile.enabled || !props.platform) return false
  const platforms = profile.match?.platforms || []
  const groupIds = profile.match?.group_ids || []
  const claudeCondition = profile.match?.claude_code_only || 'any'
  if (platforms.length && !platforms.some((value) => value.toLowerCase() === props.platform?.toLowerCase())) return false
  if (groupIds.length && (!props.groupId || !groupIds.includes(props.groupId))) return false
  if (claudeCondition === 'required' && !props.claudeCodeOnly) return false
  if (claudeCondition === 'forbidden' && props.claudeCodeOnly) return false
  return true
}

const clientTabs = computed((): ClientTabConfig[] => {
  const tabs = new Map<string, ClientTabConfig>()
  const groupTemplate = (usageConfig.value.group_templates || []).find((config) => (
    config.enabled && props.groupId && config.group_id === props.groupId
  ))

  if (groupTemplate) {
    groupTemplate.templates.forEach((template) => {
      if (!template.enabled) return
      tabs.set(template.id, {
        id: template.id,
        label: template.label,
        icon: template.kind === 'gemini_cli' ? SparkleIcon : TerminalIcon,
        kind: template.kind,
        sortOrder: template.sort_order,
        custom: template
      })
    })
  } else {
    builtInClientTabs.value.forEach((tab) => tabs.set(tab.id, tab))

    const profiles = (usageConfig.value.template_profiles || [])
      .map((profile, index) => ({ profile, index }))
      .filter(({ profile }) => profileMatches(profile))
      .sort((left, right) => left.profile.priority - right.profile.priority || left.index - right.index)

    profiles.forEach(({ profile }) => {
      if (profile.mode === 'replace') tabs.clear()
      profile.templates.forEach((template) => {
        if (!template.enabled) {
          tabs.delete(template.id)
          return
        }
        tabs.set(template.id, {
          id: template.id,
          label: template.label,
          icon: template.kind === 'gemini_cli' ? SparkleIcon : TerminalIcon,
          kind: template.kind,
          sortOrder: template.sort_order,
          custom: template
        })
      })
    })
  }

  const resolved = [...tabs.values()]
    .filter((tab) => !props.claudeCodeOnly || tab.kind === 'claude_code')
  return resolved.sort((left, right) => left.sortOrder - right.sortOrder)
})

const activeClient = computed(() => clientTabs.value.find((tab) => tab.id === activeClientTab.value))

watch(clientTabs, (tabs) => {
  if (!tabs.some((tab) => tab.id === activeClientTab.value)) {
    activeClientTab.value = tabs.find((tab) => tab.id === defaultClientTab.value)?.id || tabs[0]?.id || ''
  }
}, { immediate: true })

// Shell tabs (3 types for environment variable based configs)
const shellTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'cmd', label: 'Windows CMD', icon: WindowsIcon },
  { id: 'powershell', label: 'PowerShell', icon: WindowsIcon }
]

// OpenAI tabs (2 OS types)
const openaiTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'windows', label: 'Windows', icon: WindowsIcon }
]

const showShellTabs = computed(() => {
  if (activeClient.value?.custom) return activeClient.value.custom.variants.length > 1
  return activeClientTab.value !== 'opencode'
})

const currentTabs = computed(() => {
  if (!showShellTabs.value) return []
  if (activeClient.value?.custom) {
    return activeClient.value.custom.variants.map((variant) => ({
      id: variant.id,
      label: variant.label,
      icon: TerminalIcon
    }))
  }
  if (activeClientTab.value === 'codex' || activeClientTab.value === 'codex-ws') {
    return openaiTabs
  }
  return shellTabs
})

watch(activeClient, (client) => {
  const variants = client?.custom?.variants
  if (variants?.length && !variants.some((variant) => variant.id === activeTab.value)) {
    activeTab.value = variants[0].id
  }
})

function customTemplateVariables(): Record<string, string> {
  const configuredBase = (props.baseUrl || window.location.origin).replace(/\/+$/, '')
  const baseRoot = configuredBase.replace(/\/v1(?:beta)?\/?$/, '').replace(/\/+$/, '')
  return {
    api_key: props.apiKey,
    base_url: configuredBase,
    base_url_v1: `${baseRoot}/v1`,
    base_url_v1beta: `${baseRoot}/v1beta`,
    group_id: props.groupId ? String(props.groupId) : '',
    group_name: props.groupName || '',
    platform: props.platform || '',
    claude_code_default_model: usageConfig.value.claude_code_default_model,
    gemini_cli_default_model: usageConfig.value.gemini_cli_default_model,
    codex_model: usageConfig.value.codex_model,
    codex_review_model: usageConfig.value.codex_review_model,
    codex_reasoning_effort: usageConfig.value.codex_reasoning_effort,
    codex_network_access: usageConfig.value.codex_network_access
  }
}

function renderCustomTemplateValue(value: string): string {
  const variables = customTemplateVariables()
  return value.replace(/\{\{([a-z0-9_]+)\}\}/gi, (placeholder, name: string) => (
    Object.prototype.hasOwnProperty.call(variables, name) ? variables[name] : placeholder
  ))
}

const platformDescription = computed(() => {
  if (activeClient.value?.custom?.description) return renderCustomTemplateValue(activeClient.value.custom.description)
  switch (props.platform) {
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.description')
      }
      return t('keys.useKeyModal.openai.description')
    case 'gemini':
      return t('keys.useKeyModal.gemini.description')
    case 'antigravity':
      return t('keys.useKeyModal.antigravity.description')
    default:
      return t('keys.useKeyModal.description')
  }
})

const platformNote = computed(() => {
  if (activeClient.value?.custom?.note) return renderCustomTemplateValue(activeClient.value.custom.note)
  switch (props.platform) {
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.note')
      }
      return activeTab.value === 'windows'
        ? t('keys.useKeyModal.openai.noteWindows')
        : t('keys.useKeyModal.openai.note')
    case 'gemini':
      return t('keys.useKeyModal.gemini.note')
    case 'antigravity':
      return activeClientTab.value === 'claude'
        ? t('keys.useKeyModal.antigravity.claudeNote')
        : t('keys.useKeyModal.antigravity.geminiNote')
    default:
      return t('keys.useKeyModal.note')
  }
})

const showPlatformNote = computed(() => activeClient.value?.custom
  ? Boolean(activeClient.value.custom.note)
  : activeClientTab.value !== 'opencode')

const escapeHtml = (value: string) => value
  .replace(/&/g, '&amp;')
  .replace(/</g, '&lt;')
  .replace(/>/g, '&gt;')
  .replace(/"/g, '&quot;')
  .replace(/'/g, '&#39;')

const wrapToken = (className: string, value: string) =>
  `<span class="${className}">${escapeHtml(value)}</span>`

const keyword = (value: string) => wrapToken('text-emerald-300', value)
const variable = (value: string) => wrapToken('text-sky-200', value)
const operator = (value: string) => wrapToken('text-slate-400', value)
const string = (value: string) => wrapToken('text-amber-200', value)
const comment = (value: string) => wrapToken('text-slate-500', value)

// Syntax highlighting helpers
// Generate file configs based on platform and active tab
const currentFiles = computed((): FileConfig[] => {
  const baseUrl = props.baseUrl || window.location.origin
  const apiKey = props.apiKey
  const baseRoot = baseUrl.replace(/\/v1\/?$/, '').replace(/\/+$/, '')
  const ensureV1 = (value: string) => {
    const trimmed = value.replace(/\/+$/, '')
    return trimmed.endsWith('/v1') ? trimmed : `${trimmed}/v1`
  }
  const apiBase = ensureV1(baseRoot)
  const antigravityBase = ensureV1(`${baseRoot}/antigravity`)
  const antigravityGeminiBase = (() => {
    const trimmed = `${baseRoot}/antigravity`.replace(/\/+$/, '')
    return trimmed.endsWith('/v1beta') ? trimmed : `${trimmed}/v1beta`
  })()
  const geminiBase = (() => {
    const trimmed = baseRoot.replace(/\/+$/, '')
    return trimmed.endsWith('/v1beta') ? trimmed : `${trimmed}/v1beta`
  })()

  if (activeClient.value?.custom) {
    const variants = activeClient.value.custom.variants
    const variant = variants.find((item) => item.id === activeTab.value) || variants[0]
    if (!variant) return []
    return variant.files.map((file) => ({
      path: renderCustomTemplateValue(file.path),
      content: renderCustomTemplateValue(file.content),
      hint: file.hint ? renderCustomTemplateValue(file.hint) : undefined
    }))
  }

  if (activeClientTab.value === 'opencode') {
    switch (props.platform) {
      case 'anthropic':
        return [generateOpenCodeConfig('anthropic', apiBase, apiKey)]
      case 'openai':
        return [generateOpenCodeConfig('openai', apiBase, apiKey)]
      case 'gemini':
        return [generateOpenCodeConfig('gemini', geminiBase, apiKey)]
      case 'grok':
        return [generateOpenCodeConfig('grok', apiBase, apiKey)]
      case 'antigravity':
        return [
          generateOpenCodeConfig('antigravity-claude', antigravityBase, apiKey, 'opencode.json (Claude)'),
          generateOpenCodeConfig('antigravity-gemini', antigravityGeminiBase, apiKey, 'opencode.json (Gemini)')
        ]
      default:
        return [generateOpenCodeConfig('openai', apiBase, apiKey)]
    }
  }

  switch (props.platform) {
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return generateAnthropicFiles(baseUrl, apiKey)
      }
      if (activeClientTab.value === 'codex-ws') {
        return generateOpenAIWsFiles(baseUrl, apiKey)
      }
      return generateOpenAIFiles(baseUrl, apiKey)
    case 'gemini':
      return [generateGeminiCliContent(baseUrl, apiKey)]
    case 'antigravity':
      if (activeClientTab.value === 'gemini') {
        return [generateGeminiCliContent(`${baseUrl}/antigravity`, apiKey)]
      }
      return generateAnthropicFiles(`${baseUrl}/antigravity`, apiKey)
    default:
      return generateAnthropicFiles(baseUrl, apiKey)
  }
})

function generateAnthropicFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const config = usageConfig.value
  const defaultModel = config.claude_code_default_model
  const disableTraffic = config.claude_code_disable_nonessential_traffic
  let path: string
  let content: string

  switch (activeTab.value) {
    case 'unix':
      path = 'Terminal'
      content = `export ANTHROPIC_BASE_URL="${baseUrl}"
export ANTHROPIC_AUTH_TOKEN="${apiKey}"
${disableTraffic ? 'export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1\n' : ''}export CLAUDE_CODE_ATTRIBUTION_HEADER=${config.claude_code_attribution_header}`
      break
    case 'cmd':
      path = 'Command Prompt'
      content = `set ANTHROPIC_BASE_URL=${baseUrl}
set ANTHROPIC_AUTH_TOKEN=${apiKey}
${disableTraffic ? 'set CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1\n' : ''}set CLAUDE_CODE_ATTRIBUTION_HEADER=${config.claude_code_attribution_header}`
      break
    case 'powershell':
      path = 'PowerShell'
      content = `$env:ANTHROPIC_BASE_URL="${baseUrl}"
$env:ANTHROPIC_AUTH_TOKEN="${apiKey}"
${disableTraffic ? '$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1\n' : ''}$env:CLAUDE_CODE_ATTRIBUTION_HEADER=${config.claude_code_attribution_header}`
      break
    default:
      path = 'Terminal'
      content = ''
  }

  const vscodeSettingsPath = activeTab.value === 'unix'
    ? '~/.claude/settings.json'
    : '%userprofile%\\.claude\\settings.json'

  const vscodeContent = `{
  "env": {
    "ANTHROPIC_BASE_URL": "${baseUrl}",
    "ANTHROPIC_AUTH_TOKEN": "${apiKey}",
${disableTraffic ? '    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",\n' : ''}    "CLAUDE_CODE_ATTRIBUTION_HEADER": "${config.claude_code_attribution_header}"
  }
  //, "model": "${defaultModel}" // 修改这里的模型名可指定使用模型，默认${defaultModel}
}`

  return [
    { path, content },
    {
      path: vscodeSettingsPath,
      content: vscodeContent,
      hint: t('keys.useKeyModal.claudeCode.settingsHint', {
        model: defaultModel
      })
    }
  ]
}

function generateGeminiCliContent(baseUrl: string, apiKey: string): FileConfig {
  const model = usageConfig.value.gemini_cli_default_model
  const modelComment = t('keys.useKeyModal.gemini.modelComment')
  let path: string
  let content: string
  let highlighted: string

  switch (activeTab.value) {
    case 'unix':
      path = 'Terminal'
      content = `export GOOGLE_GEMINI_BASE_URL="${baseUrl}"
export GEMINI_API_KEY="${apiKey}"
export GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('export')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('export')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('export')} ${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    case 'cmd':
      path = 'Command Prompt'
      content = `set GOOGLE_GEMINI_BASE_URL=${baseUrl}
set GEMINI_API_KEY=${apiKey}
set GEMINI_MODEL=${model}`
      highlighted = `${keyword('set')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(baseUrl)}
${keyword('set')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(apiKey)}
${keyword('set')} ${variable('GEMINI_MODEL')}${operator('=')}${string(model)}
${comment(`REM ${modelComment}`)}`
      break
    case 'powershell':
      path = 'PowerShell'
      content = `$env:GOOGLE_GEMINI_BASE_URL="${baseUrl}"
$env:GEMINI_API_KEY="${apiKey}"
$env:GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('$env:')}${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('$env:')}${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('$env:')}${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    default:
      path = 'Terminal'
      content = ''
      highlighted = ''
  }

  return { path, content, highlighted }
}

function generateOpenAIFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.codex' : '~/.codex'

  const configContent = generateCodexConfig(baseUrl, false)

  // auth.json content
  const authContent = `{
  "OPENAI_API_KEY": "${apiKey}"
}`

  return [
    {
      path: `${configDir}/config.toml`,
      content: configContent,
      hint: t('keys.useKeyModal.openai.configTomlHint')
    },
    {
      path: `${configDir}/auth.json`,
      content: authContent
    }
  ]
}

function generateOpenAIWsFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.codex' : '~/.codex'

  const configContent = generateCodexConfig(baseUrl, true)

  // auth.json content
  const authContent = `{
  "OPENAI_API_KEY": "${apiKey}"
}`

  return [
    {
      path: `${configDir}/config.toml`,
      content: configContent,
      hint: t('keys.useKeyModal.openai.configTomlHint')
    },
    {
      path: `${configDir}/auth.json`,
      content: authContent
    }
  ]
}

function generateCodexConfig(baseUrl: string, websocket: boolean): string {
  const config = usageConfig.value
  const topLevel = [
    'model_provider = "OpenAI"',
    `model = ${JSON.stringify(config.codex_model)}`,
    `review_model = ${JSON.stringify(config.codex_review_model || config.codex_model)}`,
    `model_reasoning_effort = ${JSON.stringify(config.codex_reasoning_effort)}`,
    `disable_response_storage = ${config.codex_disable_response_storage}`,
    `network_access = ${JSON.stringify(config.codex_network_access)}`,
    'windows_wsl_setup_acknowledged = true'
  ]
  const extraConfig = config.codex_extra_config.trim()
  if (extraConfig) topLevel.push('', extraConfig)

  const provider = [
    '[model_providers.OpenAI]',
    'name = "OpenAI"',
    `base_url = ${JSON.stringify(baseUrl)}`,
    'wire_api = "responses"',
    ...(websocket ? ['supports_websockets = true'] : []),
    'requires_openai_auth = true'
  ]

  const features: string[] = []
  if (websocket && config.codex_include_legacy_ws_feature) {
    features.push('responses_websockets_v2 = true')
  }
  if (config.codex_goals_enabled) features.push('goals = true')

  return [
    ...topLevel,
    '',
    ...provider,
    ...(features.length ? ['', '[features]', ...features] : [])
  ].join('\n')
}

function generateOpenCodeConfig(platform: string, baseUrl: string, apiKey: string, pathLabel?: string): FileConfig {
  const provider: Record<string, any> = {
    [platform]: {
      options: {
        baseURL: baseUrl,
        apiKey
      }
    }
  }
  const openaiModels = {
    'gpt-5.2': {
      name: 'GPT-5.2',
      limit: {
        context: 400000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.6-sol': {
      name: 'GPT-5.6 Sol',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.6-terra': {
      name: 'GPT-5.6 Terra',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.6-luna': {
      name: 'GPT-5.6 Luna',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.5': {
      name: 'GPT-5.5',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.4': {
      name: 'GPT-5.4',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.4-mini': {
      name: 'GPT-5.4 Mini',
      limit: {
        context: 400000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.3-codex-spark': {
      name: 'GPT-5.3 Codex Spark',
      limit: {
        context: 128000,
        output: 32000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'codex-mini-latest': {
      name: 'Codex Mini',
      limit: {
        context: 200000,
        output: 100000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {}
      }
    }
  }
  const grokModels = {
    'grok-4.5': {
      name: 'Grok 4.5'
    },
    'grok-4.3': {
      name: 'Grok 4.3'
    },
    'grok-build-0.1': {
      name: 'Grok Build 0.1'
    },
    'grok-composer-2.5-fast': {
      name: 'Grok Composer 2.5 Fast'
    },
    'grok-4.20-0309-reasoning': {
      name: 'Grok 4.20 Reasoning',
      reasoning: true
    },
    'grok-4.20-0309-non-reasoning': {
      name: 'Grok 4.20 Non Reasoning'
    },
    'grok-4.20-multi-agent-0309': {
      name: 'Grok 4.20 Multi Agent'
    }
  }
  const geminiModels = {
    'gemini-2.0-flash': {
      name: 'Gemini 2.0 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-pro': {
      name: 'Gemini 2.5 Pro',
      limit: {
        context: 2097152,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.5-flash': {
      name: 'Gemini 3.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-flash-preview': {
      name: 'Gemini 3 Flash Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-pro-preview': {
      name: 'Gemini 3 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-preview': {
      name: 'Gemini 3.1 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }

  const antigravityGeminiModels = {
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'disable'
        }
      }
    },
    'gemini-2.5-flash-lite': {
      name: 'Gemini 2.5 Flash Lite',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-thinking': {
      name: 'Gemini 2.5 Flash (Thinking)',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3-flash': {
      name: 'Gemini 3 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-low': {
      name: 'Gemini 3.1 Pro Low',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-high': {
      name: 'Gemini 3.1 Pro High',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-image': {
      name: 'Gemini 2.5 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-flash-image': {
      name: 'Gemini 3.1 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }
  const claudeModel = (
    name: string,
    context: number,
    output: number,
    thinking?: { type: 'adaptive' | 'enabled'; budgetTokens?: number }
  ) => ({
    name,
    limit: { context, output },
    modalities: {
      input: ['text', 'image', 'pdf'],
      output: ['text']
    },
    ...(thinking ? { options: { thinking } } : {})
  })
  const claudeModels = {
    'claude-fable-5': claudeModel('Claude Fable 5', 1048576, 128000, { type: 'adaptive' }),
    'claude-sonnet-5': claudeModel('Claude Sonnet 5', 1000000, 128000),
    'claude-opus-4-8': claudeModel('Claude Opus 4.8', 200000, 128000),
    'claude-opus-4-8-thinking': claudeModel('Claude Opus 4.8 (Thinking)', 200000, 128000, { type: 'enabled', budgetTokens: 24576 }),
    'claude-opus-4-7': claudeModel('Claude Opus 4.7', 200000, 128000),
    'claude-opus-4-6': claudeModel('Claude Opus 4.6', 200000, 128000),
    'claude-opus-4-6-thinking': claudeModel('Claude Opus 4.6 (Thinking)', 200000, 128000, { type: 'enabled', budgetTokens: 24576 }),
    'claude-sonnet-4-6': claudeModel('Claude Sonnet 4.6', 200000, 64000),
    'claude-sonnet-4-6-thinking': claudeModel('Claude Sonnet 4.6 (Thinking)', 200000, 64000, { type: 'enabled', budgetTokens: 24576 }),
    'claude-opus-4-5-20251101': claudeModel('Claude Opus 4.5', 200000, 64000),
    'claude-opus-4-5-thinking': claudeModel('Claude Opus 4.5 (Thinking)', 200000, 64000, { type: 'enabled', budgetTokens: 24576 }),
    'claude-sonnet-4-5': claudeModel('Claude Sonnet 4.5', 200000, 64000),
    'claude-sonnet-4-5-thinking': claudeModel('Claude Sonnet 4.5 (Thinking)', 200000, 64000, { type: 'enabled', budgetTokens: 24576 }),
    'claude-sonnet-4-5-20250929': claudeModel('Claude Sonnet 4.5 (Versioned)', 200000, 64000),
    'claude-haiku-4-5': claudeModel('Claude Haiku 4.5', 200000, 64000),
    'claude-haiku-4-5-20251001': claudeModel('Claude Haiku 4.5 (Versioned)', 200000, 64000)
  }

  if (platform === 'gemini') {
    provider[platform].npm = '@ai-sdk/google'
    provider[platform].models = geminiModels
  } else if (platform === 'anthropic') {
    provider[platform].npm = '@ai-sdk/anthropic'
    provider[platform].models = claudeModels
  } else if (platform === 'grok') {
    provider[platform].npm = '@ai-sdk/openai-compatible'
    provider[platform].name = 'Grok'
    provider[platform].models = grokModels
  } else if (platform === 'antigravity-claude') {
    provider[platform].npm = '@ai-sdk/anthropic'
    provider[platform].name = 'Antigravity (Claude)'
    provider[platform].models = claudeModels
  } else if (platform === 'antigravity-gemini') {
    provider[platform].npm = '@ai-sdk/google'
    provider[platform].name = 'Antigravity (Gemini)'
    provider[platform].models = antigravityGeminiModels
  } else if (platform === 'openai') {
    provider[platform].models = openaiModels
  }

  const agent =
    platform === 'openai'
      ? {
          build: {
            options: {
              store: false
            }
          },
          plan: {
            options: {
              store: false
            }
          }
        }
      : undefined

  const content = JSON.stringify(
    {
      provider,
      ...(agent ? { agent } : {}),
      $schema: 'https://opencode.ai/config.json'
    },
    null,
    2
  )

  return {
    path: pathLabel ?? 'opencode.json',
    content,
    hint: t('keys.useKeyModal.opencode.hint')
  }
}

const copyContent = async (content: string, index: number) => {
  const success = await clipboardCopy(content, t('keys.copied'))
  if (success) {
    copiedIndex.value = index
    setTimeout(() => {
      copiedIndex.value = null
    }, 2000)
  }
}
</script>
