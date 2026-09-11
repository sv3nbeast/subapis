<template>
  <section class="task-feed wc-feed" :aria-label="t('webAgent.tasks')">
    <div v-if="!tasks.length" class="task-empty wc-empty"><span class="wc-empty-ic"><Icon name="layers" size="lg" /></span><h2>{{ t('webAgent.empty') }}</h2><p>{{ t('webAgent.emptyHint') }}</p></div>
    <article v-for="task in tasks" :key="task.id" class="task-thread wc-task">
      <p class="task-request wc-task-req-p"><span class="wc-task-req-b">{{ task.prompt }}</span></p>
      <div class="task-response wc-task-res">
        <span class="wc-av ai" aria-hidden="true">S</span>
        <div class="min-w-0">
          <div class="task-heading wc-meta">
            <b>SubAPIs</b>
            <span class="wc-task-pill" :data-status="task.status">{{ t(`webAgent.${task.status}`) }}</span>
            <span>{{ task.model }}</span>
          </div>
          <p v-if="task.error_code" class="task-error wc-msg-error" role="status">{{ failure(task.error_code) }}</p>
          <ol v-if="steps(task).length" class="wc-steps">
            <li v-for="step in steps(task)" :key="step.id">
              <span class="state" :class="step.state"><Icon v-if="step.state==='done'" name="check" size="xs" /><Icon v-else-if="step.state==='failed'" name="x" size="xs" /></span>
              {{ stepName(step.name) }}
              <small v-if="step.state==='running'">{{ t('webAgent.running') }}</small>
            </li>
          </ol>
          <div class="wc-task-arts">
            <button v-for="artifact in taskArtifacts(task)" :key="artifact.id" type="button" class="wc-art" @click="emit('open', artifact.id)">
              <span class="wc-art-ic"><Icon name="presentation" size="md" /></span>
              <span class="min-w-0"><span class="wc-art-n trunc">{{ artifact.title }}</span><span class="wc-art-d trunc">{{ artifact.filename }} · v{{ artifact.version }}</span></span>
              <span class="wc-thumbs" aria-hidden="true"><i v-for="n in 4" :key="n" /></span>
              <span class="wc-art-o">{{ t('webAgent.open') }}<Icon name="chevronRight" size="xs" /></span>
            </button>
          </div>
          <div class="wc-task-acts">
            <button v-if="task.status === 'succeeded' && artifactID(task)" type="button" class="wc-btn" @click="emit('open', artifactID(task)!)"><Icon name="eye" size="sm" />{{ t('webAgent.open') }}</button>
            <button v-if="!isTaskTerminal(task.status)" type="button" class="wc-btn" :disabled="task.status === 'cancel_requested'" @click="emit('cancel', task.id)"><Icon name="x" size="sm" />{{ t('webAgent.cancel') }}</button>
            <button type="button" class="wc-btn wc-btn-ghost" @click="emit('details', task.id)"><Icon name="info" size="sm" />{{ t('webAgent.details') }}</button>
          </div>
          <WebChatSources :sources="generation(task)?.sources || []" />
          <details class="wc-details" @toggle="onDetails($event, task.id)">
            <summary>{{ t('webAgent.technicalDetails') }}</summary>
            <code v-if="generation(task)?.request_id">{{ generation(task)?.request_id }}</code>
            <code v-if="generation(task)?.client_request_id">{{ generation(task)?.client_request_id }}</code>
            <small v-if="generation(task)?.usage">↑ {{ formatTokens(generation(task)?.usage?.input_tokens || 0) }} · ↓ {{ formatTokens(generation(task)?.usage?.output_tokens || 0) }}</small>
          </details>
        </div>
      </div>
    </article>
    <button v-if="hasMore" type="button" class="load-more wc-load-more" :disabled="loading" @click="emit('older')">{{ t('webAgent.more') }}</button>
  </section>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import WebChatSources from './WebChatSources.vue'
import type { WebChatSource } from '@/api/webChat'
import { isTaskTerminal, type WebAgentArtifact, type WebAgentTask, type WebAgentTaskEvent } from '@/api/webAgent'
import { formatTokens } from '@/utils/webChatTokens'

const props = defineProps<{ tasks: WebAgentTask[]; events: Record<number, WebAgentTaskEvent[]>; hasMore: boolean; loading: boolean; artifacts?: WebAgentArtifact[] }>()
const emit = defineEmits<{ open: [number]; cancel: [number]; details: [number]; older: [] }>()
const { t, te } = useI18n()
type Result = { artifact_id?: number; generation?: { request_id?: string; client_request_id?: string; sources?: WebChatSource[]; usage?: { input_tokens: number; output_tokens: number } } }
const result = (task: WebAgentTask): Result => task.result && typeof task.result === 'object' ? task.result as Result : {}
function artifactID(task: WebAgentTask) { const id = result(task).artifact_id; return Number.isSafeInteger(id) && Number(id) > 0 ? Number(id) : undefined }
function taskArtifacts(task: WebAgentTask): WebAgentArtifact[] { const id = artifactID(task); if (!id) return []; const known = props.artifacts?.find(file => file.id === id); return known ? [known] : [] }
const generation = (task: WebAgentTask) => result(task).generation
function failure(code: string) { if (te(`webAgent.errors.${code}`)) return t(`webAgent.errors.${code}`); if (/^model_http_\d{3}$/.test(code)) return t('webAgent.gatewayFailed', { status: code.slice(-3) }); return t('webAgent.taskFailed') }
function stepName(name: string) { return t(({ '生成内容': 'webAgent.plan', '生成文件和预览': 'webAgent.render', '保存成果': 'webAgent.store' } as Record<string, string>)[name] || name) }
function steps(task: WebAgentTask) {
  const list: { id: number; name: string; state: string }[] = []
  for (const event of props.events[task.id] || []) {
    if (event.type === 'step.started') list.push({ id: event.id, name: String(event.data.name || ''), state: 'running' })
    if (event.type === 'step.completed') { const step = [...list].reverse().find(s => s.name === event.data.name && s.state === 'running'); if (step) step.state = 'done' }
  }
  if (isTaskTerminal(task.status) && task.status !== 'succeeded') list.forEach(s => { if (s.state === 'running') s.state = 'failed' })
  return list
}
function onDetails(event: Event, id: number) { if ((event.target as HTMLDetailsElement).open) emit('details', id) }
</script>
<style scoped>
.wc-task-req-p { display: flex; justify-content: flex-end; }
.wc-task-req-b { max-width: 88%; background: var(--wc-user-bg); border: 1px solid var(--wc-line-x); border-radius: 14px; padding: 10px 14px; font-size: 14px; line-height: 1.7; box-shadow: var(--wc-hl); white-space: pre-wrap; overflow-wrap: anywhere; }
.wc-task-pill { height: 22px; border-radius: 7px; padding: 0 8px; display: inline-flex; align-items: center; gap: 5px; font-size: 11.5px; border: 1px solid var(--wc-acc-l); background: var(--wc-acc-s); color: var(--wc-acc-d); font-family: -apple-system, BlinkMacSystemFont, "PingFang SC", system-ui, sans-serif; font-weight: 600; }
.wc-task-pill[data-status="failed"], .wc-task-pill[data-status="interrupted"], .wc-task-pill[data-status="cancelled"] { border-color: rgba(220, 38, 38, .3); background: rgba(220, 38, 38, .1); color: var(--wc-danger); }
.wc-feed { flex: 1; min-height: 0; }
.wc-empty { min-height: 15rem; }
@media (max-width: 767px) { .wc-task-acts .wc-btn { min-height: 40px; } }
</style>
