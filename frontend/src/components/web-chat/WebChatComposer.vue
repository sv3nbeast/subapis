<template>
  <form class="composer" @submit.prevent="submit" @dragover.prevent @drop.prevent="drop">
    <div v-if="documents.length || failedAttachments.length || attachmentState" class="attachment-strip">
      <span v-for="doc in documents" :key="doc.id">
        {{ doc.original_name }}
        <button type="button" @click="emit('remove-document', doc.id)">×</button>
      </span>
      <span v-for="failed in failedAttachments" :key="failed.key" class="attachment-failed" :title="failed.error">
        {{ failed.file.name }} · {{ failed.error || t('webChat.documentFailed') }}
        <button type="button" @click="emit('retry-attachment', failed.key)">{{ t('webChat.retry') }}</button>
        <button type="button" @click="emit('remove-failed-attachment', failed.key)">×</button>
      </span>
      <span v-if="attachmentState">{{ attachmentState }}</span>
    </div>
    <textarea
      :value="modelValue"
      rows="2"
      :aria-label="t('workspace.inputLabel')"
      :placeholder="t('webChat.placeholder')"
      :disabled="disabled"
      class="composer-input"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
      @keydown="handleKeydown"
    />
    <div class="composer-bottom">
      <span>
        <label v-if="filesEnabled" class="template-trigger">
          <input class="sr-only" type="file" :disabled="disabled || sending" multiple accept=".pdf,.docx,.xlsx,.txt,.md,.csv" @change="pick" />
          <Icon name="upload" size="xs" /> {{ t('webChat.attach') }}
        </label>
        <button v-if="templatesEnabled" type="button" :disabled="disabled || sending" class="template-trigger" @click="emit('open-template')">
          <Icon name="sparkles" size="xs" /> {{ templateName || t('webChat.templates') }}
        </button>
        <small v-if="modelValue.length > 18000" class="char-count">{{ modelValue.length.toLocaleString() }} / 20,000</small>
      </span>
      <button v-if="sending" type="button" class="btn-stop" :aria-label="t('webChat.stop')" :title="t('webChat.stop')" @click="emit('stop')"><span class="stop-square" /></button>
      <button v-else class="btn-send" :disabled="!canSend || disabled || !modelValue.trim()" :aria-label="t('webChat.send')" :title="t('webChat.send')"><Icon name="arrowUp" size="sm" /></button>
    </div>
  </form>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { WebChatDocument } from '@/api/webChat'
import type { WebChatFailedAttachment } from '@/composables/useWebChatDocuments'

const props = defineProps<{
  modelValue: string
  disabled: boolean
  canSend: boolean
  sending: boolean
  filesEnabled: boolean
  templatesEnabled: boolean
  templateName: string
  documents: WebChatDocument[]
  failedAttachments: WebChatFailedAttachment[]
  attachmentState: string
}>()
const emit = defineEmits<{
  'update:modelValue': [string]
  submit: []
  stop: []
  'open-template': []
  files: [File[]]
  'remove-document': [number]
  'retry-attachment': [string]
  'remove-failed-attachment': [string]
}>()
const { t } = useI18n()

function pick(event: Event) {
  const input = event.target as HTMLInputElement
  emit('files', Array.from(input.files || []))
  input.value = ''
}
function submit() {
  if (!props.disabled && !props.sending && props.canSend && props.modelValue.trim()) emit('submit')
}
function handleKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.shiftKey || event.altKey || event.ctrlKey || event.metaKey || event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  submit()
}
function drop(event: DragEvent) {
  if (props.filesEnabled && !props.disabled && !props.sending) emit('files', Array.from(event.dataTransfer?.files || []))
}
</script>

<style scoped>
.composer{border:1px solid var(--wa-line,#e4e4e7);border-radius:6px;padding:.65rem .8rem;background:var(--wa-bg,#fff)}
.composer:focus-within{border-color:#a1a1aa}.composer-input{width:100%;resize:none;border:0;background:transparent;color:var(--wa-text,#18181b);min-height:3rem;padding:.3rem .15rem;font-size:1rem;line-height:1.6;outline:none}
.composer-bottom{display:flex;align-items:center;justify-content:space-between;gap:.5rem;color:var(--wa-muted,#71717a);font-size:.875rem;margin-top:.3rem}.composer-bottom>span{display:flex;flex-wrap:wrap;align-items:center;gap:.65rem}
.btn-send,.btn-stop{display:grid;place-items:center;flex-shrink:0;width:2rem;height:2rem;border-radius:6px;color:var(--wa-bg,#fff);background:var(--wa-text,#18181b)}.btn-send:disabled{opacity:.35;cursor:not-allowed}.stop-square{width:.6rem;height:.6rem;background:currentColor;border-radius:1px}
.template-trigger{display:inline-flex;align-items:center;gap:.35rem;position:relative;cursor:pointer;font-size:.875rem;min-height:2rem}.template-trigger:focus-within{outline:2px solid var(--wa-accent,#2563eb);outline-offset:3px}.char-count{font-size:.75rem}
.attachment-strip{display:flex;flex-wrap:wrap;gap:.4rem;margin-bottom:.5rem}.attachment-strip>span{display:inline-flex;align-items:center;gap:.4rem;border:1px solid var(--wa-line,#e4e4e7);border-radius:5px;padding:.25rem .45rem;font-size:.8125rem;overflow-wrap:anywhere}.attachment-strip .attachment-failed{color:#b91c1c;background:#fef2f2}.attachment-failed button:first-of-type{text-decoration:underline}
@media(max-width:767px){.btn-send,.btn-stop{width:44px;height:44px;background:transparent;position:relative;isolation:isolate}.btn-send:before,.btn-stop:before{content:"";position:absolute;inset:6px;z-index:-1;border-radius:6px;background:var(--wa-text,#18181b)}.template-trigger{min-height:44px}.attachment-strip button{min-width:32px;min-height:32px}}
</style>
