<template>
  <form class="composer wc-cbox" @submit.prevent="submit" @dragover.prevent @drop.prevent="drop">
    <div v-if="templateName" class="wc-tplbar">
      <span class="tile"><Icon name="sparkles" size="xs" /></span>
      <span>{{ t('webChat.templateApplied') }}</span><b class="trunc">{{ templateName }}</b>
      <button type="button" class="x" :aria-label="t('webChat.clearTemplate')" :title="t('webChat.clearTemplate')" @click="emit('clear-template')"><Icon name="x" size="xs" /></button>
    </div>
    <div v-if="documents.length || failedAttachments.length || attachmentState" class="attachment-strip wc-attach-strip">
      <span v-for="doc in documents" :key="doc.id" class="wc-att">
        <i class="ext" :data-ext="extension(doc.extension || doc.original_name)">{{ extension(doc.extension || doc.original_name).toUpperCase() }}</i>
        <span class="trunc">{{ doc.original_name }}</span><small>{{ bytes(doc.size_bytes) }}</small>
        <button type="button" :aria-label="t('common.delete')" @click="emit('remove-document', doc.id)"><Icon name="x" size="xs" /></button>
      </span>
      <span v-for="failed in failedAttachments" :key="failed.key" class="wc-att failed attachment-failed" :title="failed.error">
        <i class="ext">!</i><span class="trunc">{{ failed.file.name }}</span><small>{{ failed.error || t('webChat.documentFailed') }}</small>
        <button type="button" class="retry" @click="emit('retry-attachment', failed.key)">{{ t('webChat.retry') }}</button>
        <button type="button" :aria-label="t('common.delete')" @click="emit('remove-failed-attachment', failed.key)"><Icon name="x" size="xs" /></button>
      </span>
      <span v-if="attachmentState" class="wc-att state">{{ attachmentState }}</span>
    </div>
    <textarea
      :value="modelValue"
      rows="2"
      :aria-label="t('workspace.inputLabel')"
      :placeholder="t('webChat.placeholder')"
      :disabled="disabled"
      class="composer-input wc-ctext"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
      @keydown="handleKeydown"
    />
    <div class="composer-bottom wc-cbar">
      <slot name="model" />
      <div v-if="allowModes && modes.length" class="task-modebar wc-seg wc-modes" :aria-label="t('webAgent.mode')">
        <button v-for="item in modes" :key="item" type="button" :aria-pressed="mode === item" :disabled="modeDisabled" @click="emit('update:mode', item)">
          <Icon :name="modeIcon[item]" size="xs" /><span class="label">{{ t(`webAgent.${item}`) }}</span>
        </button>
      </div>
      <label v-if="filesEnabled" class="template-trigger wc-ib" :title="t('webChat.attach')" :aria-label="t('webChat.attach')">
        <input class="wc-sr-only" type="file" :disabled="disabled || sending" multiple accept=".pdf,.docx,.xlsx,.txt,.md,.csv" @change="pick" />
        <Icon name="paperClip" size="sm" />
      </label>
      <button v-if="templatesEnabled" type="button" :disabled="disabled || sending" class="template-trigger wc-ib" :title="t('webChat.templates')" :aria-label="t('webChat.templates')" @click="emit('open-template')"><Icon name="sparkles" size="sm" /></button>
      <small v-if="modelValue.length > 18000" class="char-count wc-cnt">{{ modelValue.length.toLocaleString() }} / 20,000</small>
      <span v-else class="wc-cnt">{{ modelValue.length.toLocaleString() }} / 20,000</span>
      <button v-if="sending" type="button" class="btn-stop wc-send stop" :aria-label="t('webChat.stop')" :title="t('webChat.stop')" @click="emit('stop')"><Icon name="stop" size="sm" /></button>
      <button v-else class="btn-send wc-send" :disabled="!canSend || disabled || !modelValue.trim()" :aria-label="t('webChat.send')" :title="t('webChat.send')"><Icon name="arrowUp" size="sm" /></button>
    </div>
  </form>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { WebChatDocument } from '@/api/webChat'
import type { WebChatFailedAttachment } from '@/composables/useWebChatDocuments'

type TaskMode = 'chat' | 'slides' | 'spreadsheet' | 'document'

const props = withDefaults(defineProps<{
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
  modes?: readonly TaskMode[]
  mode?: TaskMode
  modeDisabled?: boolean
  allowModes?: boolean
}>(), { modes: () => [], mode: 'chat', modeDisabled: false, allowModes: false })
const emit = defineEmits<{
  'update:modelValue': [string]
  'update:mode': [TaskMode]
  submit: []
  stop: []
  'open-template': []
  'clear-template': []
  files: [File[]]
  'remove-document': [number]
  'retry-attachment': [string]
  'remove-failed-attachment': [string]
}>()
const { t } = useI18n()
const modeIcon: Record<TaskMode, 'chat' | 'presentation' | 'chartBar' | 'document'> = {
  chat: 'chat',
  slides: 'presentation',
  spreadsheet: 'chartBar',
  document: 'document',
}

function extension(nameOrExt: string): string {
  const raw = nameOrExt.includes('.') ? nameOrExt.slice(nameOrExt.lastIndexOf('.') + 1) : nameOrExt
  return raw.trim().toLowerCase()
}
function bytes(value: number): string {
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(1)} MB`
  return `${Math.max(1, Math.ceil(value / 1024))} KB`
}
function submit() {
  if (!props.disabled && !props.sending && props.canSend && props.modelValue.trim()) emit('submit')
}
function handleKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.shiftKey || event.altKey || event.ctrlKey || event.metaKey || event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  submit()
}
function pick(event: Event) {
  const input = event.target as HTMLInputElement
  emit('files', Array.from(input.files || []))
  input.value = ''
}
function drop(event: DragEvent) {
  if (props.filesEnabled && !props.disabled && !props.sending) emit('files', Array.from(event.dataTransfer?.files || []))
}
</script>
