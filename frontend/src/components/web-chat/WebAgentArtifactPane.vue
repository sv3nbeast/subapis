<template>
  <aside class="artifact-pane wc-pane" :aria-label="t('webAgent.files')">
    <header class="wc-pane-head">
      <div class="min-w-0 grow"><h2 class="trunc">{{ artifact.title }}</h2><small>{{ artifact.filename }} · v{{ artifact.version }}</small></div>
      <button class="wc-ib wc-pane-close" :aria-label="t('webAgent.close')" @click="emit('close')"><Icon name="x" size="sm" /></button>
    </header>
    <div class="wc-pane-seg">
      <div class="artifact-tabs wc-seg" :aria-label="t('webAgent.files')"><button v-for="item in tabs" :key="item" :aria-pressed="tab === item" @click="tab = item">{{ t(`webAgent.${item}`) }}</button></div>
      <button class="download wc-btn" :disabled="busy" @click="download"><Icon name="download" size="sm" />{{ t('webAgent.download') }}</button>
    </div>
    <p v-if="error" class="artifact-error wc-notice error" role="alert">{{ error }}</p>
    <div class="artifact-body wc-pane-body">
      <WebAgentPdfPreview v-if="tab === 'preview'" :key="artifact.id" :artifact-id="artifact.id" />
      <div v-else-if="tab === 'files'" class="file-list wc-file-list"><button v-for="file in files" :key="file.id" :class="{ on: file.id === artifact.id }" :aria-pressed="file.id === artifact.id" @click="emit('select', file.id)"><Icon name="document" size="sm" class="vv" /><strong>{{ file.filename }}</strong><small>v{{ file.version }} · {{ bytes(file.size_bytes) }}</small></button></div>
      <div v-else class="file-list wc-file-list"><button v-for="version in versions" :key="version.id" :class="{ on: version.id === artifact.id }" :aria-pressed="version.id === artifact.id" @click="emit('select', version.id)"><i class="vv">v{{ version.version }}</i><strong>{{ version.title }}</strong><small>{{ new Date(version.created_at).toLocaleString() }}</small></button><button v-if="nextBefore" class="wc-btn wc-btn-ghost" :disabled="busy" @click="loadVersions(true)">{{ t('webAgent.more') }}</button></div>
    </div>
    <footer class="wc-pane-foot">
      <button class="wc-btn wc-btn-p" :disabled="!canRevise || busy" @click="emit('revise', artifact)"><Icon name="sparkles" size="sm" />{{ t('webAgent.revise') }}</button>
      <button class="delete wc-btn wc-btn-ghost" :disabled="busy" @click="confirmDelete = true"><Icon name="trash" size="sm" /></button>
    </footer>
    <BaseDialog :show="confirmDelete" :title="t('webAgent.deleteTitle')" width="narrow" :z-index="80" @close="confirmDelete = false">
      <p>{{ t('webAgent.deleteHint') }}</p>
      <template #footer><button :disabled="busy" @click="confirmDelete = false">{{ t('common.cancel') }}</button><button class="confirm-delete" :disabled="busy" @click="remove">{{ t('webAgent.delete') }}</button></template>
    </BaseDialog>
  </aside>
</template>
<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import WebAgentPdfPreview from './WebAgentPdfPreview.vue'
import { getArtifactBlob, getArtifactVersions, deleteArtifact, type WebAgentArtifact } from '@/api/webAgent'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ artifact: WebAgentArtifact; files: WebAgentArtifact[]; canRevise: boolean }>()
const emit = defineEmits<{ close: []; select: [number]; revise: [WebAgentArtifact]; deleted: [number] }>()
const { t } = useI18n()
const tabs = ['preview', 'files', 'versions'] as const
const tab = ref<typeof tabs[number]>('preview'), busy = ref(false), confirmDelete = ref(false), error = ref(''), versions = ref<WebAgentArtifact[]>([]), nextBefore = ref(0)
let epoch = 0, controller = new AbortController()
const bytes = (n: number) => n < 1024 * 1024 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1024 / 1024).toFixed(1)} MB`
async function loadVersions(older = false) {
  const version = epoch, id = props.artifact.id
  try {
    const list = await getArtifactVersions(id, older ? nextBefore.value : 0, controller.signal)
    if (version === epoch) { versions.value = older ? [...versions.value, ...list.items] : list.items; nextBefore.value = list.next_before }
  } catch (e) { if (version === epoch && !controller.signal.aborted) error.value = extractApiErrorMessage(e) }
}
async function download() {
  if (busy.value) return
  busy.value = true; error.value = ''
  const file = props.artifact, version = epoch
  try {
    const blob = await getArtifactBlob(file.id, false, controller.signal)
    if (version !== epoch) return
    const url = URL.createObjectURL(blob), anchor = document.createElement('a')
    anchor.href = url; anchor.download = file.filename; anchor.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (e) { if (version === epoch) error.value = extractApiErrorMessage(e) }
  finally { if (version === epoch) busy.value = false }
}
async function remove() {
  if (busy.value) return
  const id = props.artifact.id, version = epoch
  busy.value = true; error.value = ''
  try { await deleteArtifact(id); emit('deleted', id); if (version === epoch) confirmDelete.value = false }
  catch (e) { if (version === epoch) error.value = extractApiErrorMessage(e) }
  finally { if (version === epoch) busy.value = false }
}
watch(() => props.artifact.id, () => { epoch++; controller.abort(); controller = new AbortController(); versions.value = []; nextBefore.value = 0; busy.value = false; confirmDelete.value = false; error.value = ''; void loadVersions() }, { immediate: true })
onBeforeUnmount(() => { epoch++; controller.abort() })
</script>
<style scoped>
.wc-pane-seg{display:flex;align-items:center;gap:8px;padding:10px 14px 0}
.wc-pane-seg .wc-seg{flex:1}
.wc-pane-seg .download{flex:none}
</style>
