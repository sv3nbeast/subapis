<template>
  <section class="task-feed" :aria-label="t('webAgent.tasks')">
    <div v-if="!tasks.length" class="task-empty"><h2>{{ t('webAgent.empty') }}</h2><p>{{ t('webAgent.emptyHint') }}</p></div>
    <article v-for="task in tasks" :key="task.id" class="task-thread">
      <p class="task-request">{{ task.prompt }}</p>
      <div class="task-response">
        <div class="task-heading"><strong>SubAPIs</strong><span :data-status="task.status">{{ t(`webAgent.${task.status}`) }}</span><small>{{ task.model }}</small></div>
        <p v-if="task.error_code" class="task-error" role="status">{{ failure(task.error_code) }}</p>
        <details :open="!isTaskTerminal(task.status)" @toggle="onDetails($event, task.id)">
          <summary>{{ t('webAgent.details') }}</summary>
          <ol><li v-for="step in steps(task)" :key="step.id"><span :class="step.state">{{ step.state === 'done' ? '✓' : step.state === 'failed' ? '×' : '·' }}</span>{{ stepName(step.name) }}</li></ol>
          <code v-if="generation(task)?.request_id">{{ generation(task)?.request_id }}</code>
          <code v-if="generation(task)?.client_request_id">{{ generation(task)?.client_request_id }}</code>
          <small v-if="generation(task)?.usage">↑ {{ generation(task)?.usage?.input_tokens }} · ↓ {{ generation(task)?.usage?.output_tokens }}</small>
        </details>
        <WebChatSources :sources="generation(task)?.sources || []" />
        <div class="task-actions">
          <button v-if="task.status === 'succeeded' && artifactID(task)" class="artifact-link" @click="emit('open', artifactID(task)!)">{{ t('webAgent.open') }} <span>↗</span></button>
          <button v-if="!isTaskTerminal(task.status)" :disabled="task.status === 'cancel_requested'" @click="emit('cancel', task.id)">{{ t('webAgent.cancel') }}</button>
        </div>
      </div>
    </article>
    <button v-if="hasMore" class="load-more" :disabled="loading" @click="emit('older')">{{ t('webAgent.more') }}</button>
  </section>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { isTaskTerminal, type WebAgentTask, type WebAgentTaskEvent } from '@/api/webAgent'
import WebChatSources from './WebChatSources.vue'
import type { WebChatSource } from '@/api/webChat'
const props = defineProps<{ tasks: WebAgentTask[]; events: Record<number, WebAgentTaskEvent[]>; hasMore: boolean; loading: boolean }>()
const emit = defineEmits<{ open: [number]; cancel: [number]; details: [number]; older: [] }>()
const { t, te } = useI18n()
type Result = { artifact_id?: number; generation?: { request_id?: string; client_request_id?: string; sources?: WebChatSource[]; usage?: { input_tokens: number; output_tokens: number } } }
const result = (task: WebAgentTask): Result => task.result && typeof task.result === 'object' ? task.result as Result : {}
function artifactID(task: WebAgentTask) { const id = result(task).artifact_id; return Number.isSafeInteger(id) && Number(id) > 0 ? id : undefined }
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
.task-feed{overflow:auto;flex:1;min-height:0;padding:1.5rem max(1.25rem,calc((100% - 48rem)/2))}.task-empty{display:grid;place-content:center;min-height:15rem;text-align:center}.task-empty h2{font-size:1.25rem;font-weight:650}.task-empty p{color:var(--wa-muted);margin-top:.5rem}.task-thread{margin-bottom:2.5rem}.task-request{white-space:pre-wrap;overflow-wrap:anywhere;padding:.75rem 1rem;background:var(--wa-soft);border-radius:6px;margin-left:auto;max-width:88%;width:fit-content}.task-response{margin-top:1.25rem}.task-heading{display:flex;flex-wrap:wrap;gap:.7rem;align-items:center}.task-heading strong{font-weight:650}.task-heading span{font-size:.875rem;color:var(--wa-accent)}.task-heading small{font-size:.75rem;color:var(--wa-muted)}.task-heading [data-status=failed],.task-heading [data-status=interrupted],.task-error{color:#b91c1c}.task-error{margin:.75rem 0;font-size:.875rem}.task-response details{margin-top:1rem;font-size:.875rem}.task-response summary{cursor:pointer;color:var(--wa-muted)}.task-response ol{list-style:none;margin-top:.5rem}.task-response li{padding:.7rem 0;border-bottom:1px solid var(--wa-line);display:flex;gap:.6rem}.task-response li span{width:1rem;color:var(--wa-muted)}.task-response li .done{color:var(--wa-accent)}.task-response li .failed{color:#b91c1c}.task-response code,.task-response details>small{display:block;font-size:.75rem;overflow-wrap:anywhere;color:var(--wa-muted);margin-top:.5rem}.task-actions{display:flex;gap:1rem;margin-top:1rem;font-size:.875rem}.task-actions button{min-height:2rem}.artifact-link{border:1px solid var(--wa-line);padding:.4rem .75rem;border-radius:5px;display:flex;gap:1rem}.load-more{display:block;margin:auto;color:var(--wa-accent);font-size:.875rem}@media(max-width:767px){.task-feed{padding:1rem}.task-actions button,.load-more{min-height:44px}}
</style>
