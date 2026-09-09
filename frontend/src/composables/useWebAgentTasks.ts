import { onBeforeUnmount, ref, watch, type Ref } from 'vue'
import * as api from '@/api/webAgent'
import { extractApiErrorMessage } from '@/utils/apiError'

type Intent = { session: number; input: api.WebAgentCreateRequest; signature: string }
export function useWebAgentTasks(session: Ref<number | null>, userID: Ref<number | undefined>, available: Ref<boolean> = ref(true)) {
  const tasks = ref<api.WebAgentTask[]>([]), artifacts = ref<api.WebAgentArtifact[]>([])
  const events = ref<Record<number, api.WebAgentTaskEvent[]>>({})
  const loading = ref(false), creating = ref(false), error = ref(''), pending = ref<Intent | null>(null)
  const nextBefore = ref(0)
  let epoch = 0, stopped = false, controller = new AbortController(), timer: ReturnType<typeof setTimeout> | undefined
  let refreshing = false
  let loadedOlder = false
  const deletedArtifacts = new Set<number>()
  const intentKey = (id: number) => `web-agent-intent:${userID.value}:${id}`
  const mergeTasks = (incoming: api.WebAgentTask[]) => {
    const byID = new Map(tasks.value.map(t => [t.id, t]))
    incoming.forEach(t => byID.set(t.id, t))
    tasks.value = [...byID.values()].sort((a, b) => b.id - a.id)
  }
  async function refresh() {
    const id = session.value, version = epoch
    if (!id || !available.value || refreshing || stopped) return
    refreshing = true
    try {
      const [list, files] = await Promise.all([api.listTasks({ session_id: id }, controller.signal), api.listArtifacts({ session_id: id }, controller.signal)])
      if (version !== epoch) return
      const known = new Set(tasks.value.map(t => t.id))
      if (list.items.length === 50 && known.size && !list.items.some(t => known.has(t.id))) loadedOlder = false
      mergeTasks(list.items); artifacts.value = files.items.filter(a => !deletedArtifacts.has(a.id))
      if (!loadedOlder) nextBefore.value = list.next_before
      await Promise.all(list.items.filter(t => !api.isTaskTerminal(t.status) || !['task.succeeded', 'task.failed', 'task.cancelled', 'task.interrupted'].includes(events.value[t.id]?.at(-1)?.type || '')).slice(0, 10).map(t => loadEvents(t.id, version)))
      if (version === epoch) error.value = ''
    } catch (e) { if (version === epoch && !controller.signal.aborted) error.value = extractApiErrorMessage(e) }
    finally { if (version === epoch) { refreshing = false; loading.value = false } }
  }
  async function loadEvents(id: number, version = epoch) {
    const old = events.value[id] || [], cursor = old.at(-1)?.id || 0
    let list
    try { list = await api.getTaskEvents(id, cursor, controller.signal) }
    catch (e) { if (version !== epoch || controller.signal.aborted) return; throw e }
    if (version !== epoch) return
    const merged = new Map((events.value[id] || []).map(e => [e.id, e])); list.items.forEach(e => merged.set(e.id, e))
    events.value[id] = [...merged.values()].sort((a, b) => a.id - b.id)
  }
  async function older() {
    const id = session.value, version = epoch, before = nextBefore.value
    if (!id || !before || loading.value) return
    loading.value = true
    try {
      const list = await api.listTasks({ session_id: id, before }, controller.signal)
      if (version === epoch) { mergeTasks(list.items); nextBefore.value = list.next_before; loadedOlder = true }
    } catch (e) { if (version === epoch) error.value = extractApiErrorMessage(e) }
    finally { if (version === epoch) loading.value = false }
  }
  async function sendIntent(intent: Intent) {
    if (creating.value) return null
    creating.value = true; error.value = ''
    const version = epoch, key = intentKey(intent.session)
    try {
      const task = await api.createTask(intent.session, intent.input)
      try {
        const stored = sessionStorage.getItem(key)
        if (stored && JSON.parse(stored).input.idempotency_key === intent.input.idempotency_key) sessionStorage.removeItem(key)
      } catch { /* A local cleanup failure cannot turn an accepted task into a failed POST. */ }
      if (version === epoch && session.value === intent.session) { pending.value = null; mergeTasks([task]); void refresh() }
      return task
    } catch (e) { if (version === epoch) error.value = extractApiErrorMessage(e); throw e }
    finally { creating.value = false }
  }
  async function create(input: Omit<api.WebAgentCreateRequest, 'idempotency_key'>) {
    if (!session.value || !userID.value || creating.value) return null
    const signature = JSON.stringify(input), id = session.value
    let intent = pending.value
    if (!intent || intent.session !== id || intent.signature !== signature) intent = { session: id, signature, input: { ...input, idempotency_key: crypto.randomUUID() } }
    // This is a local submission intent, not an authoritative task record.
    // Persist before POST so a reload can retry the exact same operation key.
    sessionStorage.setItem(intentKey(id), JSON.stringify(intent)); pending.value = intent
    return sendIntent(intent)
  }
  async function retryPending() { return pending.value ? sendIntent(pending.value) : null }
  async function cancel(id: number) {
    const version = epoch
    const task = await api.cancelTask(id)
    if (version === epoch) { mergeTasks([task]); await refresh() }
  }
  async function removeArtifact(id: number) {
    await api.deleteArtifact(id)
    markArtifactDeleted(id)
  }
  function markArtifactDeleted(id: number) {
    deletedArtifacts.add(id)
    artifacts.value = artifacts.value.filter(a => a.id !== id)
  }
  const schedule = () => {
    if (stopped) return
    timer = setTimeout(async () => {
      if (stopped) return
      if (document.visibilityState !== 'hidden') await refresh()
      schedule()
    }, tasks.value.some(t => !api.isTaskTerminal(t.status)) ? 1800 : 8000)
  }
  watch([session, userID, available], () => {
    epoch++; controller.abort(); controller = new AbortController(); refreshing = false
    tasks.value = []; artifacts.value = []; events.value = {}; nextBefore.value = 0; loadedOlder = false; deletedArtifacts.clear(); pending.value = null; error.value = ''
    if (session.value && available.value) {
      try {
        const raw = sessionStorage.getItem(intentKey(session.value))
        if (raw) {
          const intent = JSON.parse(raw) as Intent
          if (intent.session === session.value && typeof intent.signature === 'string' && typeof intent.input?.prompt === 'string' && typeof intent.input?.idempotency_key === 'string' && ['slides', 'document', 'spreadsheet'].includes(intent.input.kind)) pending.value = intent
        }
      } catch { /* Ignore corrupted local draft state. */ }
      loading.value = true; void refresh()
    } else loading.value = false
  }, { immediate: true })
  schedule()
  onBeforeUnmount(() => { stopped = true; epoch++; controller.abort(); clearTimeout(timer) })
  return { tasks, artifacts, events, loading, creating, error, pending, nextBefore, refresh, older, loadEvents, create, retryPending, cancel, removeArtifact, markArtifactDeleted }
}
