<template>
  <section class="artifact-library" :aria-label="t('webAgent.library')">
    <header class="library-heading"><div><h2>{{ t('webAgent.library') }}</h2><p>{{ t('webAgent.libraryHint') }}</p></div><button :disabled="loading" @click="load(true)">{{ t('webAgent.refresh') }}</button></header>
    <div v-if="enabled" class="library-filters">
      <input v-model="query" type="search" :aria-label="t('webAgent.searchLoaded')" :placeholder="t('webAgent.searchLoaded')" />
      <select v-model="kind" :aria-label="t('webAgent.mode')"><option value="">{{ t('webAgent.allKinds') }}</option><option v-for="value in kinds" :key="value" :value="value">{{ t(`webAgent.${value}`) }}</option></select>
      <small>{{ t('webAgent.loadedVersions', { count: records.length }) }}</small>
    </div>
    <p v-if="error" class="library-error" role="alert">{{ error }}</p>
    <p v-if="!enabled" class="library-empty">{{ t('webAgent.unavailable') }}</p>
    <p v-else-if="loading && !records.length" class="library-empty" role="status">{{ t('common.loading') }}</p>
    <div v-else-if="visible.length" class="library-grid">
      <article v-for="file in visible" :key="file.lineage_id" class="library-file">
        <span class="file-kind">{{ t(`webAgent.${file.kind}`) }}</span>
        <button class="file-title" @click="emit('open', file.id)">{{ file.title }}</button>
        <p>{{ file.filename }}</p>
        <small>v{{ file.version }} · {{ bytes(file.size_bytes) }} · {{ date(file.created_at) }}</small>
        <button class="file-session" @click="emit('conversation', file)">{{ sessionName(file.session_id) }} ↗</button>
        <button class="file-open" @click="emit('open', file.id)">{{ t('webAgent.open') }}</button>
      </article>
    </div>
    <p v-else class="library-empty">{{ query || kind ? t('webAgent.noLoadedMatch') : t('webAgent.noFiles') }}</p>
    <button v-if="enabled && nextBefore" class="load-more" :disabled="loading" @click="load(false)">{{ t('webAgent.moreFiles') }}</button>
    <slot name="references" />
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { listArtifacts, type WebAgentArtifact, type WebAgentTaskKind } from '@/api/webAgent'
import type { WebChatSession } from '@/api/webChat'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ enabled: boolean; userId?: number; sessions: WebChatSession[] }>()
const emit = defineEmits<{ open: [number]; conversation: [WebAgentArtifact]; loaded: [WebAgentArtifact[]] }>()
const { t, locale } = useI18n()
const records = ref<WebAgentArtifact[]>([]), query = ref(''), kind = ref<WebAgentTaskKind | ''>(''), nextBefore = ref(0), loading = ref(false), error = ref('')
const kinds: WebAgentTaskKind[] = ['slides', 'spreadsheet', 'document']
let epoch = 0, controller = new AbortController()
const removed = new Set<number>()
const visible = computed(() => {
  const latest = new Map<string, WebAgentArtifact>()
  for (const file of records.value) {
    const old = latest.get(file.lineage_id)
    if (!old || file.version > old.version) latest.set(file.lineage_id, file)
  }
  const search = query.value.trim().toLocaleLowerCase(locale.value)
  return [...latest.values()].filter(file => (!kind.value || file.kind === kind.value) && (!search || [file.title, file.filename, sessionName(file.session_id)].some(s => s.toLocaleLowerCase(locale.value).includes(search)))).sort((a, b) => b.id - a.id)
})
function sessionName(id: number) { return props.sessions.find(s => s.id === id)?.title || t('webAgent.sessionFallback', { id }) }
function bytes(n: number) { return n < 1024 * 1024 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1024 / 1024).toFixed(1)} MB` }
function date(value: string) { return new Date(value).toLocaleDateString(locale.value) }
async function load(reset = false) {
  if (!props.enabled || !props.userId || (loading.value && !reset)) return
  if (reset) { epoch++; controller.abort(); controller = new AbortController() }
  const version = epoch, cursor = reset ? 0 : nextBefore.value
  loading.value = true; error.value = ''
  try {
    const list = await listArtifacts(cursor ? { before: cursor } : {}, controller.signal)
    if (version !== epoch) return
    const merged = new Map((reset ? [] : records.value).map(file => [file.id, file]))
    list.items.filter(file => !removed.has(file.id)).forEach(file => merged.set(file.id, file))
    records.value = [...merged.values()].sort((a, b) => b.id - a.id)
    nextBefore.value = list.next_before
    emit('loaded', records.value)
  } catch (e) { if (version === epoch && !controller.signal.aborted) error.value = extractApiErrorMessage(e) }
  finally { if (version === epoch) loading.value = false }
}
function remove(id: number) { removed.add(id); records.value = records.value.filter(file => file.id !== id); emit('loaded', records.value) }
watch(() => [props.enabled, props.userId], () => {
  epoch++; controller.abort(); controller = new AbortController(); removed.clear(); records.value = []; nextBefore.value = 0; error.value = ''; loading.value = false; query.value = ''; kind.value = ''; emit('loaded', [])
  void load(true)
}, { immediate: true })
onBeforeUnmount(() => { epoch++; controller.abort() })
defineExpose({ refresh: () => load(true), remove })
</script>
<style scoped>
.artifact-library{overflow:auto;flex:1;min-height:0;padding:1.5rem}.library-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:1rem}.library-heading h2{font-size:1.125rem;font-weight:650}.library-heading p{font-size:.875rem;color:var(--wa-muted);margin-top:.25rem}.library-heading>button,.load-more{font-size:.875rem;min-height:2rem;padding:.25rem .65rem;border:1px solid var(--wa-line);border-radius:5px}.library-filters{display:flex;gap:.6rem;align-items:center;flex-wrap:wrap;margin:1.25rem 0}.library-filters input,.library-filters select{font-size:.875rem;background:var(--wa-bg);border:1px solid var(--wa-line);border-radius:5px;padding:.4rem .6rem;min-width:0}.library-filters input{width:min(100%,20rem)}.library-filters small{font-size:.75rem;color:var(--wa-muted)}.library-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,15rem),1fr));gap:1rem}.library-file{border:1px solid var(--wa-line);border-radius:7px;padding:1rem;display:flex;flex-direction:column;align-items:flex-start;gap:.45rem;min-width:0}.file-kind{font-size:.75rem;color:var(--wa-accent)}.file-title{font-size:1rem;font-weight:600;text-align:left;overflow-wrap:anywhere}.library-file>p{font-size:.875rem;overflow-wrap:anywhere}.library-file>small{font-size:.75rem;color:var(--wa-muted)}.file-session{font-size:.8125rem;text-align:left;color:var(--wa-muted);overflow-wrap:anywhere}.file-open{font-size:.875rem;color:var(--wa-accent);margin-top:.5rem;min-height:2rem}.library-error{color:#b91c1c;font-size:.875rem;margin:1rem 0}.library-empty{padding:3rem 0;text-align:center;color:var(--wa-muted)}.load-more{display:block;margin:1.25rem auto}.library-file button:active,.library-heading>button:active{background:var(--wa-soft)}@media(max-width:767px){.artifact-library{padding:1rem}.artifact-library button,.library-filters select,.library-filters input{min-height:44px}}
</style>
