import { apiClient } from './client'

export type WebAgentTaskStatus = 'queued' | 'running' | 'cancel_requested' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted'
export type WebAgentTaskKind = 'slides' | 'spreadsheet' | 'document'
export interface WebAgentTask {
  id: number
  session_id: number
  group_id: number | null
  model: string
  kind: WebAgentTaskKind
  prompt: string
  document_ids: number[]
  source_artifact_id?: number
  status: WebAgentTaskStatus
  result?: unknown
  error_code?: string
  step_count: number
  deadline_at: string
  created_at: string
  updated_at: string
  finished_at?: string
}
export interface WebAgentTaskEvent {
  id: number
  task_id: number
  type: string
  data: Record<string, unknown>
  created_at: string
}
export interface WebAgentCreateRequest {
  kind: WebAgentTaskKind
  prompt: string
  document_ids?: number[]
  source_artifact_id?: number
  // Create once per user action and reuse on transport retry. Never generate
  // a replacement key automatically after an ambiguous response.
  idempotency_key: string
}
export async function createTask(sessionID: number, input: WebAgentCreateRequest) {
  const { data } = await apiClient.post<WebAgentTask>(`/web-chat/sessions/${sessionID}/tasks`, input)
  return data
}
export async function getTask(id: number, signal?: AbortSignal) {
  const { data } = await apiClient.get<WebAgentTask>(`/web-chat/tasks/${id}`, { signal })
  return data
}
export async function listTasks(params: { session_id?: number; before?: number } = {}, signal?: AbortSignal) {
  const { data } = await apiClient.get<{ items: WebAgentTask[]; next_before: number }>('/web-chat/tasks', { params, signal })
  return data
}
export async function getTaskEvents(id: number, after = 0, signal?: AbortSignal) {
  const { data } = await apiClient.get<{ items: WebAgentTaskEvent[]; next_after: number }>(`/web-chat/tasks/${id}/events`, { params: { after }, signal })
  return data
}
export async function cancelTask(id: number) {
  const { data } = await apiClient.post<WebAgentTask>(`/web-chat/tasks/${id}/cancel`)
  return data
}
export function isTaskTerminal(status: WebAgentTaskStatus) {
  return ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(status)
}

export interface WebAgentArtifact {
  id: number
  task_id: number
  session_id: number
  lineage_id: string
  version: number
  parent_id?: number
  kind: WebAgentTaskKind
  title: string
  filename: string
  mime: string
  size_bytes: number
  sha256: string
  created_at: string
}
export async function listArtifacts(params: { session_id?: number; before?: number } = {}, signal?: AbortSignal) {
  const { data } = await apiClient.get<{ items: WebAgentArtifact[]; next_before: number }>('/web-chat/artifacts', { params, signal })
  return data
}
export async function getArtifact(id: number, signal?: AbortSignal) {
  const { data } = await apiClient.get<WebAgentArtifact>(`/web-chat/artifacts/${id}`, { signal })
  return data
}
export async function getArtifactVersions(id: number, before = 0, signal?: AbortSignal) {
  const { data } = await apiClient.get<{ items: WebAgentArtifact[]; next_before: number }>(`/web-chat/artifacts/${id}/versions`, { params: { before }, signal })
  return data
}
// Use the authenticated client, never put bearer tokens into a preview URL.
// The view owns creating/revoking object URLs so switching tasks can release them.
export async function getArtifactBlob(id: number, preview = false, signal?: AbortSignal): Promise<Blob> {
  const { data } = await apiClient.get<Blob>(`/web-chat/artifacts/${id}/${preview ? 'preview' : 'download'}`, { responseType: 'blob', signal })
  return data
}
