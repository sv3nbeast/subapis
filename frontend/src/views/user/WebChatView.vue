<template>
  <AppLayout>
    <div
      ref="webChatShellRef"
      class="web-chat-shell"
      :class="{
        'is-resizing-session': resizingPanel === 'session',
        'is-resizing-context': resizingPanel === 'context',
      }"
      :style="webChatShellStyle"
    >
      <aside class="session-panel" :class="{ 'session-panel-open': sessionsOpen }">
        <div class="flex items-center justify-between gap-3">
          <div>
            <p class="text-xs font-bold uppercase tracking-[0.25em] text-primary-600 dark:text-primary-300">
              {{ t('webChat.eyebrow') }}
            </p>
            <h2 class="mt-1 text-lg font-black text-gray-950 dark:text-white">
              {{ t('webChat.sessions') }}
            </h2>
          </div>
          <button class="btn btn-secondary btn-sm lg:hidden" @click="sessionsOpen = false">
            <Icon name="x" size="sm" />
          </button>
        </div>

        <button
          class="mt-5 w-full rounded-2xl bg-primary-600 px-4 py-3 text-sm font-bold text-white shadow-lg shadow-primary-600/20 transition hover:-translate-y-0.5 hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="!canCreateSession || creatingSession || sending"
          @click="startNewSession"
        >
          <Icon name="plus" size="sm" class="mr-2 inline-block" />
          {{ creatingSession ? t('common.creating') : t('webChat.newChat') }}
        </button>

        <div class="mt-5 space-y-2 overflow-y-auto pr-1">
          <button
            v-for="session in sessions"
            :key="session.id"
            class="session-item"
            :class="{ 'session-item-active': session.id === activeSessionId }"
            @click="selectSession(session)"
          >
            <span class="truncate text-sm font-bold">{{ session.title || session.model }}</span>
            <span class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
              {{ session.group_name || groupName(session.group_id) }} · {{ session.model }}
            </span>
          </button>

          <div v-if="!sessions.length && !loading" class="rounded-2xl border border-dashed border-gray-200 p-5 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
            {{ t('webChat.noSessions') }}
          </div>
        </div>
      </aside>

      <div
        class="resize-handle resize-handle-session"
        role="separator"
        aria-orientation="vertical"
        :aria-label="t('webChat.resizeSessions')"
        @pointerdown="startPanelResize('session', $event)"
        @dblclick="resetPanelWidth('session')"
      />

      <main class="chat-panel">
        <header class="chat-header">
          <button class="btn btn-secondary btn-sm lg:hidden" @click="sessionsOpen = true">
            <Icon name="menu" size="sm" />
          </button>
          <div class="min-w-0 flex-1">
            <p class="text-xs font-semibold text-primary-600 dark:text-primary-300">
              {{ selectedGroup?.name || t('webChat.selectGroup') }}
            </p>
            <h1 class="truncate text-xl font-black text-gray-950 dark:text-white sm:text-2xl">
              {{ activeSession?.title || t('webChat.title') }}
            </h1>
          </div>
          <button
            v-if="activeSession"
            class="btn btn-secondary btn-sm text-red-600 dark:text-red-300"
            :disabled="sending"
            @click="removeCurrentSession"
          >
            <Icon name="trash" size="sm" />
            <span class="hidden sm:inline">{{ t('common.delete') }}</span>
          </button>
        </header>

        <section ref="messageListRef" class="message-list">
          <div v-if="!enabled" class="empty-state">
            <Icon name="lock" size="xl" class="text-primary-500" />
            <h2>{{ t('webChat.disabledTitle') }}</h2>
            <p>{{ t('webChat.disabledDescription') }}</p>
          </div>

          <div v-else-if="loading" class="empty-state">
            <Icon name="refresh" size="xl" class="animate-spin text-primary-500" />
            <h2>{{ t('common.loading') }}</h2>
          </div>

          <div v-else-if="!hasUsableModel" class="empty-state">
            <Icon name="inbox" size="xl" class="text-primary-500" />
            <h2>{{ t('webChat.noGroupsTitle') }}</h2>
            <p>{{ t('webChat.noGroupsDescription') }}</p>
          </div>

          <div v-else-if="!activeSession" class="empty-state">
            <Icon name="chat" size="xl" class="text-primary-500" />
            <h2>{{ t('webChat.emptyTitle') }}</h2>
            <p>{{ t('webChat.emptyDescription') }}</p>
          </div>

          <template v-else>
            <div
              v-for="message in messages"
              :key="message.id"
              class="message-row"
              :class="message.role === 'user' ? 'message-row-user' : 'message-row-assistant'"
            >
              <div class="message-bubble" :class="message.role === 'user' ? 'message-bubble-user' : 'message-bubble-assistant'">
                <p class="whitespace-pre-wrap break-words text-sm leading-7">{{ message.content }}</p>
                <p v-if="message.status === 'error' || message.status === 'partial'" class="mt-2 text-xs font-medium text-red-500">
                  {{ message.error_message || t('webChat.streamError') }}
                </p>
              </div>
            </div>

            <div v-if="sending && streamingText" class="message-row message-row-assistant">
              <div class="message-bubble message-bubble-assistant">
                <p class="whitespace-pre-wrap break-words text-sm leading-7">{{ streamingText }}</p>
              </div>
            </div>
          </template>
        </section>

        <form class="composer" @submit.prevent="send">
          <textarea
            v-model="draft"
            rows="3"
            :placeholder="t('webChat.placeholder')"
            :disabled="!canSend"
            class="composer-input"
            @keydown.enter.exact.prevent="send"
          />
          <div class="flex items-center justify-between gap-3">
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('webChat.enterHint') }}
            </p>
            <button class="btn btn-primary" :disabled="!canSend || !draft.trim()">
              <Icon :name="sending ? 'refresh' : 'arrowUp'" size="sm" :class="sending ? 'animate-spin' : ''" />
              {{ sending ? t('common.sending') : t('webChat.send') }}
            </button>
          </div>
        </form>
      </main>

      <div
        class="resize-handle resize-handle-context"
        role="separator"
        aria-orientation="vertical"
        :aria-label="t('webChat.resizeContext')"
        @pointerdown="startPanelResize('context', $event)"
        @dblclick="resetPanelWidth('context')"
      />

      <aside class="context-panel">
        <div class="context-card">
          <p class="context-label">{{ t('webChat.context') }}</p>
          <label class="input-label mt-4">{{ t('webChat.group') }}</label>
          <select v-model.number="selectedGroupId" class="input" :disabled="Boolean(activeSession) || sending">
            <option v-for="group in options.groups" :key="group.id" :value="group.id">
              {{ group.name }} · {{ platformLabel(group.platform) }}
            </option>
          </select>

          <label class="input-label mt-4">{{ t('webChat.model') }}</label>
          <select v-model="selectedModel" class="input" :disabled="Boolean(activeSession) || sending">
            <option v-for="model in selectedGroupModels" :key="model.name" :value="model.name">
              {{ model.name }}
            </option>
          </select>

          <div class="mt-4 rounded-2xl border border-primary-100 bg-primary-50/70 p-4 dark:border-primary-500/20 dark:bg-primary-500/10">
            <p class="text-xs font-bold uppercase tracking-[0.2em] text-primary-700 dark:text-primary-300">
              {{ t('webChat.priceHint') }}
            </p>
            <p class="mt-2 text-sm leading-6 text-gray-700 dark:text-gray-200">
              {{ pricingHint }}
            </p>
          </div>
        </div>

        <div class="context-card">
          <p class="context-label">{{ t('webChat.shortcuts') }}</p>
          <router-link to="/channel-status" class="context-link">
            <Icon name="server" size="sm" />
            {{ t('nav.modelStatus') }}
          </router-link>
          <router-link to="/usage" class="context-link">
            <Icon name="chart" size="sm" />
            {{ t('nav.usage') }}
          </router-link>
        </div>
      </aside>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import webChatAPI, {
  type WebChatMessage,
  type WebChatOptions,
  type WebChatSession,
} from '@/api/webChat'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformLabel } from '@/utils/platformColors'
import { formatScaled } from '@/utils/pricing'

const { t } = useI18n()

const loading = ref(true)
const creatingSession = ref(false)
const sending = ref(false)
const enabled = ref(false)
const sessionsOpen = ref(false)
const options = ref<WebChatOptions>({ enabled: false, groups: [] })
const sessions = ref<WebChatSession[]>([])
const messages = ref<WebChatMessage[]>([])
const activeSessionId = ref<number | null>(null)
const selectedGroupId = ref<number | null>(null)
const selectedModel = ref('')
const draft = ref('')
const streamingText = ref('')
const messageListRef = ref<HTMLElement | null>(null)
const webChatShellRef = ref<HTMLElement | null>(null)
const resizingPanel = ref<'session' | 'context' | null>(null)

const sessionPanelWidth = ref(248)
const contextPanelWidth = ref(272)

const sessionPanelDefaultWidth = 248
const contextPanelDefaultWidth = 272
const sessionPanelMinWidth = 208
const sessionPanelMaxWidth = 420
const contextPanelMinWidth = 232
const contextPanelMaxWidth = 440
const sessionPanelWidthStorageKey = 'subapis.webChat.sessionPanelWidth'
const contextPanelWidthStorageKey = 'subapis.webChat.contextPanelWidth'

const activeSession = computed(() => sessions.value.find((item) => item.id === activeSessionId.value) || null)
const selectedGroup = computed(() => options.value.groups.find((group) => group.id === selectedGroupId.value) || null)
const selectedGroupModels = computed(() => selectedGroup.value?.models || [])
const selectedModelOption = computed(() => selectedGroupModels.value.find((model) => model.name === selectedModel.value) || null)
const hasUsableModel = computed(() => options.value.groups.some((group) => group.models.length > 0))

const webChatShellStyle = computed(() => ({
  '--web-chat-session-width': `${sessionPanelWidth.value}px`,
  '--web-chat-context-width': `${contextPanelWidth.value}px`,
}))

const canCreateSession = computed(() => enabled.value && Boolean(selectedGroup.value && selectedModel.value))
const canSend = computed(() => enabled.value && Boolean(activeSession.value) && !sending.value)

const pricingHint = computed(() => {
  const pricing = selectedModelOption.value?.pricing
  if (!pricing) return t('webChat.noPricing')
  if (pricing.billing_mode === 'per_request') {
    return t('webChat.perRequestPrice', { price: formatScaled(pricing.per_request_price ?? null, 1) })
  }
  if (pricing.billing_mode === 'image') {
    return t('webChat.imagePrice', { price: formatScaled(pricing.image_output_price ?? pricing.per_request_price ?? null, 1) })
  }
  return t('webChat.tokenPrice', {
    input: formatScaled(pricing.input_price ?? null, 1_000_000),
    output: formatScaled(pricing.output_price ?? null, 1_000_000),
  })
})

watch(selectedGroupId, () => {
  if (activeSession.value) return
  const first = selectedGroupModels.value[0]
  selectedModel.value = first?.name || ''
})

onMounted(() => {
  restorePanelWidths()
  void loadInitial()
})

onBeforeUnmount(() => {
  stopPanelResize()
})

async function loadInitial() {
  loading.value = true
  try {
    const [opts, list] = await Promise.all([
      webChatAPI.getOptions(),
      webChatAPI.listSessions().catch(() => []),
    ])
    options.value = opts
    enabled.value = opts.enabled
    sessions.value = list
    selectedGroupId.value = opts.default_group_id ?? opts.groups[0]?.id ?? null
    selectedModel.value = opts.default_model || opts.groups[0]?.models?.[0]?.name || ''
    if (sessions.value[0]) {
      await selectSession(sessions.value[0])
    }
  } catch (err) {
    console.error(extractApiErrorMessage(err))
  } finally {
    loading.value = false
  }
}

async function startNewSession() {
  if (!canCreateSession.value || !selectedGroupId.value || !selectedModel.value) return
  creatingSession.value = true
  try {
    const session = await webChatAPI.createSession({
      group_id: selectedGroupId.value,
      model: selectedModel.value,
    })
    sessions.value = [session, ...sessions.value.filter((item) => item.id !== session.id)]
    await selectSession(session)
  } catch (err) {
    console.error(extractApiErrorMessage(err))
  } finally {
    creatingSession.value = false
  }
}

async function selectSession(session: WebChatSession) {
  activeSessionId.value = session.id
  selectedGroupId.value = session.group_id
  selectedModel.value = session.model
  sessionsOpen.value = false
  try {
    messages.value = await webChatAPI.listMessages(session.id)
    await scrollToBottom()
  } catch (err) {
    console.error(extractApiErrorMessage(err))
  }
}

async function removeCurrentSession() {
  if (!activeSession.value) return
  const id = activeSession.value.id
  await webChatAPI.deleteSession(id)
  sessions.value = sessions.value.filter((item) => item.id !== id)
  messages.value = []
  activeSessionId.value = null
  if (sessions.value[0]) {
    await selectSession(sessions.value[0])
  }
}

async function send() {
  const content = draft.value.trim()
  if (!canSend.value || !activeSession.value || !content) return
  const sessionID = activeSession.value.id
  const localUserMessage: WebChatMessage = {
    id: Date.now(),
    session_id: sessionID,
    user_id: 0,
    role: 'user',
    content,
    status: 'completed',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  }
  messages.value.push(localUserMessage)
  draft.value = ''
  sending.value = true
  streamingText.value = ''
  await scrollToBottom()

  try {
    let assistantID = Date.now() + 1
    await webChatAPI.streamMessage(sessionID, content, {
      onMeta(meta) {
        assistantID = meta.message_id
      },
      onDelta(text) {
        streamingText.value += text
        void scrollToBottom()
      },
    })
    messages.value.push({
      id: assistantID,
      session_id: sessionID,
      user_id: 0,
      role: 'assistant',
      content: streamingText.value,
      status: 'completed',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    })
    streamingText.value = ''
    sessions.value = await webChatAPI.listSessions()
  } catch (err) {
    messages.value.push({
      id: Date.now() + 2,
      session_id: sessionID,
      user_id: 0,
      role: 'assistant',
      content: streamingText.value,
      status: streamingText.value ? 'partial' : 'error',
      error_message: extractApiErrorMessage(err),
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    })
    streamingText.value = ''
  } finally {
    sending.value = false
    await scrollToBottom()
  }
}

function groupName(groupID: number): string {
  return options.value.groups.find((group) => group.id === groupID)?.name || `#${groupID}`
}

async function scrollToBottom() {
  await nextTick()
  const el = messageListRef.value
  if (el) el.scrollTop = el.scrollHeight
}

function startPanelResize(panel: 'session' | 'context', event: PointerEvent) {
  if (window.innerWidth < 1280) return
  event.preventDefault()
  resizingPanel.value = panel
  document.body.classList.add('web-chat-resizing')
  window.addEventListener('pointermove', handlePanelResize)
  window.addEventListener('pointerup', stopPanelResize, { once: true })
  handlePanelResize(event)
}

function handlePanelResize(event: PointerEvent) {
  const shell = webChatShellRef.value
  if (!shell || !resizingPanel.value) return
  const rect = shell.getBoundingClientRect()
  if (resizingPanel.value === 'session') {
    sessionPanelWidth.value = clamp(event.clientX - rect.left, sessionPanelMinWidth, sessionPanelMaxWidth)
    return
  }
  contextPanelWidth.value = clamp(rect.right - event.clientX, contextPanelMinWidth, contextPanelMaxWidth)
}

function stopPanelResize() {
  if (resizingPanel.value === 'session') {
    savePanelWidth(sessionPanelWidthStorageKey, sessionPanelWidth.value)
  } else if (resizingPanel.value === 'context') {
    savePanelWidth(contextPanelWidthStorageKey, contextPanelWidth.value)
  }
  resizingPanel.value = null
  document.body.classList.remove('web-chat-resizing')
  window.removeEventListener('pointermove', handlePanelResize)
}

function resetPanelWidth(panel: 'session' | 'context') {
  if (panel === 'session') {
    sessionPanelWidth.value = sessionPanelDefaultWidth
    savePanelWidth(sessionPanelWidthStorageKey, sessionPanelWidth.value)
    return
  }
  contextPanelWidth.value = contextPanelDefaultWidth
  savePanelWidth(contextPanelWidthStorageKey, contextPanelWidth.value)
}

function restorePanelWidths() {
  sessionPanelWidth.value = readPanelWidth(
    sessionPanelWidthStorageKey,
    sessionPanelDefaultWidth,
    sessionPanelMinWidth,
    sessionPanelMaxWidth,
  )
  contextPanelWidth.value = readPanelWidth(
    contextPanelWidthStorageKey,
    contextPanelDefaultWidth,
    contextPanelMinWidth,
    contextPanelMaxWidth,
  )
}

function readPanelWidth(key: string, fallback: number, min: number, max: number) {
  const value = Number(window.localStorage.getItem(key))
  if (!Number.isFinite(value) || value <= 0) return fallback
  return clamp(value, min, max)
}

function savePanelWidth(key: string, value: number) {
  window.localStorage.setItem(key, String(Math.round(value)))
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, Math.round(value)))
}
</script>

<style scoped>
.web-chat-shell {
  display: grid;
  grid-template-columns:
    var(--web-chat-session-width, 15.5rem)
    0.375rem
    minmax(0, 1fr)
    0.375rem
    var(--web-chat-context-width, 17rem);
  gap: 0.75rem;
  height: calc(100vh - 7.5rem);
  min-height: 42rem;
}

.session-panel,
.chat-panel,
.context-panel {
  min-height: 0;
}

.session-panel,
.context-card {
  border: 1px solid rgb(229 231 235);
  background: rgba(255, 255, 255, 0.88);
  box-shadow: 0 18px 50px rgba(15, 23, 42, 0.07);
}

.dark .session-panel,
.dark .context-card {
  border-color: rgba(51, 65, 85, 0.9);
  background: rgba(15, 23, 42, 0.84);
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.25);
}

.session-panel {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 1.5rem;
  padding: 1rem;
}

.resize-handle {
  position: relative;
  min-width: 0.375rem;
  cursor: col-resize;
  touch-action: none;
}

.resize-handle::before {
  position: absolute;
  inset: 1.25rem 50%;
  width: 2px;
  transform: translateX(-50%);
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.28);
  content: "";
  opacity: 0.55;
  transition:
    background 160ms ease,
    opacity 160ms ease,
    width 160ms ease;
}

.resize-handle:hover::before,
.web-chat-shell.is-resizing-session .resize-handle-session::before,
.web-chat-shell.is-resizing-context .resize-handle-context::before {
  width: 4px;
  background: rgba(20, 184, 166, 0.62);
  opacity: 1;
}

:global(body.web-chat-resizing) {
  cursor: col-resize;
  user-select: none;
}

.session-item {
  display: flex;
  width: 100%;
  flex-direction: column;
  border-radius: 1rem;
  border: 1px solid transparent;
  padding: 0.85rem;
  text-align: left;
  transition: all 160ms ease;
}

.session-item:hover,
.session-item-active {
  border-color: rgba(20, 184, 166, 0.28);
  background: rgba(20, 184, 166, 0.08);
}

.chat-panel {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 1.75rem;
  border: 1px solid rgb(229 231 235);
  background:
    radial-gradient(circle at 15% 10%, rgba(20, 184, 166, 0.12), transparent 30%),
    linear-gradient(180deg, rgba(255, 255, 255, 0.92), rgba(248, 250, 252, 0.94));
}

.dark .chat-panel {
  border-color: rgba(51, 65, 85, 0.9);
  background:
    radial-gradient(circle at 15% 10%, rgba(45, 212, 191, 0.14), transparent 30%),
    linear-gradient(180deg, rgba(15, 23, 42, 0.92), rgba(2, 6, 23, 0.94));
}

.chat-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  border-bottom: 1px solid rgba(148, 163, 184, 0.22);
  padding: 1rem 1.25rem;
}

.message-list {
  flex: 1;
  overflow-y: auto;
  padding: 1.25rem;
}

.message-row {
  display: flex;
  margin-bottom: 1rem;
}

.message-row-user {
  justify-content: flex-end;
}

.message-row-assistant {
  justify-content: flex-start;
}

.message-bubble {
  max-width: min(54rem, 90%);
  border-radius: 1.25rem;
  padding: 0.9rem 1rem;
}

.message-bubble-user {
  background: linear-gradient(135deg, #0f766e, #0891b2);
  color: white;
  box-shadow: 0 14px 30px rgba(14, 116, 144, 0.2);
}

.message-bubble-assistant {
  border: 1px solid rgba(148, 163, 184, 0.2);
  background: rgba(255, 255, 255, 0.9);
  color: rgb(17, 24, 39);
}

.dark .message-bubble-assistant {
  border-color: rgba(71, 85, 105, 0.75);
  background: rgba(15, 23, 42, 0.9);
  color: rgb(226, 232, 240);
}

.composer {
  border-top: 1px solid rgba(148, 163, 184, 0.22);
  padding: 1rem;
}

.composer-input {
  width: 100%;
  resize: none;
  border-radius: 1.25rem;
  border: 1px solid rgb(209 213 219);
  background: rgba(255, 255, 255, 0.94);
  padding: 0.9rem 1rem;
  color: rgb(17 24 39);
  outline: none;
}

.composer-input:focus {
  border-color: rgb(20 184 166);
  box-shadow: 0 0 0 3px rgba(20, 184, 166, 0.16);
}

.dark .composer-input {
  border-color: rgb(51 65 85);
  background: rgba(2, 6, 23, 0.72);
  color: rgb(226 232 240);
}

.context-panel {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.context-card {
  border-radius: 1.5rem;
  padding: 1rem;
}

.context-label {
  font-size: 0.75rem;
  font-weight: 800;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: rgb(13 148 136);
}

.context-link {
  margin-top: 0.75rem;
  display: flex;
  align-items: center;
  gap: 0.6rem;
  border-radius: 1rem;
  padding: 0.8rem;
  font-size: 0.875rem;
  font-weight: 700;
  color: rgb(55 65 81);
  transition: all 160ms ease;
}

.context-link:hover {
  background: rgba(20, 184, 166, 0.08);
  color: rgb(13 148 136);
}

.dark .context-link {
  color: rgb(203 213 225);
}

.empty-state {
  display: flex;
  min-height: 100%;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  text-align: center;
  color: rgb(100 116 139);
}

.empty-state h2 {
  font-size: 1.25rem;
  font-weight: 900;
  color: rgb(15 23 42);
}

.dark .empty-state h2 {
  color: white;
}

@media (max-width: 1279px) {
  .web-chat-shell {
    grid-template-columns: minmax(0, 1fr);
    height: auto;
    min-height: calc(100vh - 7rem);
  }

  .resize-handle {
    display: none;
  }

  .chat-panel {
    min-height: calc(100vh - 10rem);
  }

  .context-panel {
    grid-row: 2;
  }

  .session-panel {
    position: fixed;
    inset: 0 auto 0 0;
    z-index: 50;
    width: min(85vw, 22rem);
    border-radius: 0 1.5rem 1.5rem 0;
    transform: translateX(-105%);
    transition: transform 180ms ease;
  }

  .session-panel-open {
    transform: translateX(0);
  }
}
</style>
