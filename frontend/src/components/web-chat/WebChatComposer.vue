<template>
  <form class="composer" @submit.prevent="emit('submit')" @dragover.prevent @drop.prevent="drop">
    <div v-if="documents.length || failedAttachments.length || attachmentState" class="attachment-strip">
      <span v-for="doc in documents" :key="doc.id">
        {{ doc.original_name }}
        <button type="button" @click="emit('remove-document', doc.id)">×</button>
      </span>
      <span v-for="failed in failedAttachments" :key="failed.key" class="attachment-failed" :title="failed.error">
        {{ failed.file.name }} · {{ t('webChat.documentFailed') }}
        <button type="button" @click="emit('retry-attachment', failed.key)">{{ t('webChat.retry') }}</button>
        <button type="button" @click="emit('remove-failed-attachment', failed.key)">×</button>
      </span>
      <span v-if="attachmentState">{{ attachmentState }}</span>
    </div>
    <textarea
      :value="modelValue"
      rows="2"
      :placeholder="t('webChat.placeholder')"
      :disabled="disabled"
      class="composer-input"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
      @keydown.enter.exact.prevent="emit('submit')"
    />
    <div class="composer-bottom">
      <span>
        <label v-if="filesEnabled" class="template-trigger">
          <input class="hidden" type="file" multiple accept=".pdf,.docx,.txt,.md,.csv" @change="pick" />
          <Icon name="upload" size="xs" /> {{ t('webChat.attach') }}
        </label>
        <button v-if="templatesEnabled" type="button" class="template-trigger" @click="emit('open-template')">
          <Icon name="sparkles" size="xs" /> {{ templateName || t('webChat.templates') }}
        </button>
        {{ modelValue.length.toLocaleString() }} / 20,000 · {{ t('webChat.enterHint') }}
      </span>
      <button v-if="sending" type="button" class="btn-stop" @click="emit('stop')"><span class="stop-square" />{{ t('webChat.stop') }}</button>
      <button v-else class="btn-send" :disabled="!canSend"><Icon name="arrowUp" size="sm" />{{ t('webChat.send') }}</button>
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
function drop(event: DragEvent) {
  if (props.filesEnabled) emit('files', Array.from(event.dataTransfer?.files || []))
}
</script>

<style scoped>
.composer{border-top:1px solid rgba(148,163,184,.25);padding:.75rem 1rem}.composer-input{width:100%;resize:none;border:1px solid #d1d5db;border-radius:1rem;background:transparent;padding:.65rem .8rem;outline:none}.composer-input:focus{border-color:#14b8a6;box-shadow:0 0 0 3px rgba(20,184,166,.12)}.composer-bottom{display:flex;align-items:center;justify-content:space-between;margin-top:.45rem;color:#94a3b8;font-size:.68rem}.btn-send,.btn-stop{display:flex;align-items:center;gap:.35rem;border-radius:.7rem;padding:.5rem .75rem;color:white;font-weight:700}.btn-send{background:#0d9488}.btn-send:disabled{opacity:.45}.btn-stop{background:#dc2626}.stop-square{width:.55rem;height:.55rem;background:white;border-radius:.1rem}.template-trigger{display:inline-flex;align-items:center;gap:.25rem;margin-right:.5rem;border-radius:.5rem;background:rgba(20,184,166,.1);padding:.25rem .4rem;color:#0f766e;font-weight:700;cursor:pointer}.attachment-strip{display:flex;flex-wrap:wrap;gap:.35rem;margin-bottom:.45rem}.attachment-strip span{border-radius:999px;background:rgba(20,184,166,.1);padding:.25rem .5rem;font-size:.68rem;color:#0f766e}.attachment-strip .attachment-failed{background:rgba(239,68,68,.1);color:#b91c1c}.attachment-failed button:first-of-type{margin-left:.35rem;font-weight:800;text-decoration:underline}@media(max-width:640px){.composer-bottom>span{max-width:70%}}
</style>
