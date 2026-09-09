<template>
  <aside class="artifact-pane" :aria-label="t('webAgent.files')">
    <header><div><h2>{{ artifact.title }}</h2><small>{{ artifact.filename }} · v{{ artifact.version }}</small></div><button :aria-label="t('webAgent.close')" @click="emit('close')">×</button></header>
    <div class="artifact-toolbar">
      <div class="artifact-tabs" :aria-label="t('webAgent.files')"><button v-for="item in tabs" :key="item" :aria-pressed="tab === item" @click="tab = item">{{ t(`webAgent.${item}`) }}</button></div>
      <button class="download" :disabled="busy" @click="download">{{ t('webAgent.download') }}</button>
    </div>
    <p v-if="error" class="artifact-error" role="alert">{{ error }}</p>
    <div class="artifact-body">
      <WebAgentPdfPreview v-if="tab === 'preview'" :key="artifact.id" :artifact-id="artifact.id" />
      <div v-else-if="tab === 'files'" class="file-list"><button v-for="file in files" :key="file.id" :aria-pressed="file.id === artifact.id" @click="emit('select', file.id)"><strong>{{ file.filename }}</strong><small>v{{ file.version }} · {{ bytes(file.size_bytes) }}</small></button></div>
      <div v-else class="file-list"><button v-for="version in versions" :key="version.id" :aria-pressed="version.id === artifact.id" @click="emit('select', version.id)"><strong>v{{ version.version }} · {{ version.title }}</strong><small>{{ new Date(version.created_at).toLocaleString() }}</small></button><button v-if="nextBefore" :disabled="busy" @click="loadVersions(true)">{{ t('webAgent.more') }}</button></div>
    </div>
    <footer><button :disabled="!canRevise || busy" @click="emit('revise', artifact)">{{ t('webAgent.revise') }}</button><button class="delete" :disabled="busy" @click="confirmDelete = true">{{ t('webAgent.delete') }}</button></footer>
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
.artifact-pane{width:45%;min-width:22rem;border-left:1px solid var(--wa-line);display:flex;flex-direction:column;min-height:0;background:var(--wa-bg)}.artifact-pane>header{display:flex;gap:1rem;align-items:center;padding:1rem 1.25rem}.artifact-pane>header>div{min-width:0;flex:1}.artifact-pane h2{font-size:1rem;font-weight:650;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.artifact-pane small{font-size:.75rem;color:var(--wa-muted);overflow-wrap:anywhere}.artifact-pane>header>button{font-size:1.25rem;width:2rem;height:2rem}.artifact-toolbar{display:flex;align-items:center;justify-content:space-between;gap:.5rem;padding:0 1.25rem;border-bottom:1px solid var(--wa-line);flex-wrap:wrap}.artifact-tabs{display:flex;gap:1rem}.artifact-tabs button{font-size:.875rem;padding:.6rem 0;border-bottom:2px solid transparent}.artifact-tabs [aria-pressed=true]{border-color:var(--wa-accent);font-weight:600}.download{font-size:.8125rem;padding:.35rem .6rem;background:var(--wa-text);color:var(--wa-bg);border-radius:5px;min-height:2rem}.artifact-body{flex:1;min-height:0;overflow:auto;padding:1rem 1.25rem;background:var(--wa-soft)}.file-list{display:flex;flex-direction:column;gap:.5rem}.file-list button{display:flex;flex-direction:column;gap:.2rem;text-align:left;border:1px solid var(--wa-line);border-radius:5px;padding:.75rem;background:var(--wa-bg);font-size:.875rem;overflow-wrap:anywhere}.file-list strong{font-weight:550}.file-list [aria-pressed=true]{border-color:var(--wa-accent)}.artifact-pane>footer{display:flex;gap:1.25rem;justify-content:space-between;padding:.75rem 1.25rem;border-top:1px solid var(--wa-line);font-size:.875rem}.artifact-pane>footer button{min-height:2rem}.delete{color:var(--wa-muted)}.artifact-error{color:#b91c1c;font-size:.875rem;padding:.5rem 1.25rem}.confirm-delete{background:#b91c1c;color:white;padding:.4rem .75rem;border-radius:5px;margin-left:1rem}@media(max-width:1100px){.artifact-pane{width:48%;min-width:20rem}}@media(max-width:767px){.artifact-pane{position:fixed;inset:3.5rem 0 0;z-index:65;width:100%;min-width:0;border:0}.artifact-pane button{min-height:44px}.artifact-body{padding:.75rem}.artifact-pane>footer{padding-bottom:max(.75rem,env(safe-area-inset-bottom))}}
</style>
