import { apiClient } from './client'

export interface WebChatPricingInterval {
  min_tokens: number
  max_tokens: number | null
  tier_label?: string
  input_price?: number | null
  output_price?: number | null
  cache_write_price?: number | null
  cache_write_5m_price?: number | null
  cache_write_1h_price?: number | null
  cache_read_price?: number | null
  per_request_price?: number | null
}

export interface WebChatModelPricing {
  billing_mode: string
  input_price?: number | null
  output_price?: number | null
  cache_write_price?: number | null
  cache_write_5m_price?: number | null
  cache_write_1h_price?: number | null
  cache_read_price?: number | null
  image_output_price?: number | null
  per_request_price?: number | null
  intervals?: WebChatPricingInterval[]
}

export interface WebChatModelOption {
  name: string
  pricing?: WebChatModelPricing | null
}

export interface WebChatGroupOption {
  id: number
  name: string
  platform: string
  subscription_type: string
  rate_multiplier: number
  models: WebChatModelOption[]
}

export interface WebChatOptions {
  enabled: boolean
  groups: WebChatGroupOption[]
  default_group_id?: number
  default_model?: string
  projects_enabled: boolean
  templates_enabled: boolean
  history_enabled: boolean
}

export interface WebChatSession {
  id: number
  user_id: number
  group_id: number
  group_name?: string
  platform?: string
  model: string
  title: string
  pinned_at?: string | null
  system_prompt: string
  temperature?: number | null
  max_output_tokens: number
  project_id?: number | null
  project_name?: string
  default_template_id?: number | null
  active_leaf_message_id?: number | null
  created_at: string
  updated_at: string
}

export interface WebChatMessage {
  id: number
  session_id: number
  user_id: number
  role: 'user' | 'assistant'
  content: string
  status: 'streaming' | 'completed' | 'error' | 'partial'
  error_message?: string
  request_id?: string
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_creation_tokens: number
  logical_id: number
  parent_message_id?: number | null
  version_index: number
  version_count: number
  version_reason: 'original' | 'regenerate' | 'edit'
  template_id?: number | null
  created_at: string
  updated_at: string
}

export interface WebChatStreamMeta {
  session_id: number
  message_id: number
  group_id: number
  model: string
}

export interface WebChatStreamHandlers {
  signal?: AbortSignal
  onMeta?: (meta: WebChatStreamMeta) => void
  onDelta?: (text: string) => void
  onDone?: (result: WebChatStreamDone) => void
  onError?: (message: string, persisted?: WebChatMessage) => void
}

export interface WebChatUsage {
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_creation_tokens: number
}

export interface WebChatStreamDone {
  message_id: number
  message?: WebChatMessage
  usage?: WebChatUsage
  request_id?: string
}

export interface WebChatSessionPatch {
  title?: string
  pinned?: boolean
  system_prompt?: string
  temperature?: number | null
  max_output_tokens?: number
  project_id?: number | null
  default_template_id?: number | null
}

export interface WebChatProject {
  id: number
  user_id: number
  name: string
  description: string
  color: string
  sort_order: number
  default_group_id?: number | null
  default_model?: string
  default_template_id?: number | null
  session_count: number
  created_at: string
  updated_at: string
}

export type WebChatProjectInput = Omit<WebChatProject, 'id' | 'user_id' | 'session_count' | 'created_at' | 'updated_at'>

export interface WebChatTemplateVariable {
  name: string
  label: string
  required: boolean
  default_value: string
  type: 'singleline' | 'multiline'
}

export interface WebChatTemplate {
  id: number
  scope: 'system' | 'personal'
  user_id?: number | null
  source_template_id?: number | null
  name: string
  category: string
  description: string
  body: string
  variables: WebChatTemplateVariable[]
  language: string
  enabled: boolean
  sort_order: number
  created_at: string
  updated_at: string
}

export type WebChatTemplateInput = Omit<WebChatTemplate, 'id' | 'scope' | 'user_id' | 'source_template_id' | 'created_at' | 'updated_at'>

export class WebChatStreamError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'WebChatStreamError'
  }
}

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1'

export async function getOptions(): Promise<WebChatOptions> {
  const { data } = await apiClient.get<WebChatOptions>('/web-chat/options')
  return data
}

export async function listSessions(query = ''): Promise<WebChatSession[]> {
  const { data } = await apiClient.get<WebChatSession[]>('/web-chat/sessions', { params: query ? { q: query } : undefined })
  return data
}

export async function patchSession(sessionID: number, payload: WebChatSessionPatch): Promise<WebChatSession> {
  const { data } = await apiClient.patch<WebChatSession>(`/web-chat/sessions/${sessionID}`, payload)
  return data
}

export async function createSession(payload: { group_id?: number; model?: string; project_id?: number | null; default_template_id?: number | null }): Promise<WebChatSession> {
  const { data } = await apiClient.post<WebChatSession>('/web-chat/sessions', payload)
  return data
}

export async function listMessages(sessionID: number): Promise<WebChatMessage[]> {
  const { data } = await apiClient.get<WebChatMessage[]>(`/web-chat/sessions/${sessionID}/messages`)
  return data
}

export async function deleteSession(sessionID: number): Promise<void> {
  await apiClient.delete(`/web-chat/sessions/${sessionID}`)
}

export async function streamMessage(
  sessionID: number,
  payload: { content: string; group_id?: number | null; model?: string; template_id?: number | null },
  handlers: WebChatStreamHandlers = {},
): Promise<void> {
	return streamRequest(`/web-chat/sessions/${sessionID}/messages`, payload, handlers)
}

export async function regenerateMessage(sessionID: number, messageID: number, handlers: WebChatStreamHandlers = {}): Promise<void> {
  return streamRequest(`/web-chat/sessions/${sessionID}/messages/${messageID}/regenerate`, {}, handlers)
}

export async function reviseMessage(sessionID: number, messageID: number, content: string, handlers: WebChatStreamHandlers = {}): Promise<void> {
  return streamRequest(`/web-chat/sessions/${sessionID}/messages/${messageID}/revise`, { content }, handlers)
}

export async function listProjects(): Promise<WebChatProject[]> { const { data } = await apiClient.get<WebChatProject[]>('/web-chat/projects'); return data }
export async function createProject(payload: WebChatProjectInput): Promise<WebChatProject> { const { data } = await apiClient.post<WebChatProject>('/web-chat/projects', payload); return data }
export async function patchProject(id: number, payload: WebChatProjectInput): Promise<WebChatProject> { const { data } = await apiClient.patch<WebChatProject>(`/web-chat/projects/${id}`, payload); return data }
export async function deleteProject(id: number): Promise<void> { await apiClient.delete(`/web-chat/projects/${id}`) }
export async function listTemplates(): Promise<WebChatTemplate[]> { const { data } = await apiClient.get<WebChatTemplate[]>('/web-chat/templates'); return data }
export async function createTemplate(payload: WebChatTemplateInput): Promise<WebChatTemplate> { const { data } = await apiClient.post<WebChatTemplate>('/web-chat/templates', payload); return data }
export async function patchTemplate(id: number, payload: WebChatTemplateInput): Promise<WebChatTemplate> { const { data } = await apiClient.patch<WebChatTemplate>(`/web-chat/templates/${id}`, payload); return data }
export async function deleteTemplate(id: number): Promise<void> { await apiClient.delete(`/web-chat/templates/${id}`) }
export async function copyTemplate(id: number): Promise<WebChatTemplate> { const { data } = await apiClient.post<WebChatTemplate>(`/web-chat/templates/${id}/copy`); return data }
export async function listMessageVersions(sessionID: number, messageID: number): Promise<WebChatMessage[]> { const { data } = await apiClient.get<WebChatMessage[]>(`/web-chat/sessions/${sessionID}/messages/${messageID}/versions`); return data }
export async function activateMessageVersion(sessionID: number, messageID: number): Promise<WebChatMessage[]> { const { data } = await apiClient.post<WebChatMessage[]>(`/web-chat/sessions/${sessionID}/messages/${messageID}/activate`); return data }
export async function adminListTemplates(): Promise<WebChatTemplate[]> { const { data } = await apiClient.get<WebChatTemplate[]>('/admin/settings/web-chat-templates'); return data }
export async function adminCreateTemplate(payload: WebChatTemplateInput): Promise<WebChatTemplate> { const { data } = await apiClient.post<WebChatTemplate>('/admin/settings/web-chat-templates', payload); return data }
export async function adminPatchTemplate(id: number, payload: WebChatTemplateInput): Promise<WebChatTemplate> { const { data } = await apiClient.patch<WebChatTemplate>(`/admin/settings/web-chat-templates/${id}`, payload); return data }
export async function adminDeleteTemplate(id: number): Promise<void> { await apiClient.delete(`/admin/settings/web-chat-templates/${id}`) }

async function streamRequest(path: string, payload: unknown, handlers: WebChatStreamHandlers): Promise<void> {
  const token = localStorage.getItem('auth_token')
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'text/event-stream',
  }
  if (token) headers.Authorization = `Bearer ${token}`

  const response = await fetch(`${API_BASE_URL}${path}`, {
    method: 'POST',
    headers,
    body: JSON.stringify(payload),
    credentials: 'include',
    signal: handlers.signal,
  })

  if (!response.ok || !response.body) {
    const text = await response.text().catch(() => '')
    throw new Error(text || `HTTP ${response.status}`)
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let streamError: string | null = null

  while (true) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const chunks = buffer.split(/\r?\n\r?\n/)
    buffer = chunks.pop() || ''
    for (const chunk of chunks) {
      const errorMessage = dispatchSSEChunk(chunk, handlers)
      if (errorMessage) streamError = errorMessage
    }
  }
  buffer += decoder.decode()
  if (buffer.trim()) {
    const errorMessage = dispatchSSEChunk(buffer, handlers)
    if (errorMessage) streamError = errorMessage
  }
  if (streamError) {
    throw new WebChatStreamError(streamError)
  }
}

function dispatchSSEChunk(chunk: string, handlers: WebChatStreamHandlers): string | null {
  let event = 'message'
  const dataLines: string[] = []
  for (const line of chunk.split(/\r?\n/)) {
    if (line.startsWith('event:')) {
      event = line.slice(6).trim()
    } else if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trimStart())
    }
  }
  if (dataLines.length === 0) return null
  const dataText = dataLines.join('\n')
  let payload: any
  try {
    payload = JSON.parse(dataText)
  } catch {
    payload = dataText
  }

  if (event === 'meta') {
    handlers.onMeta?.(payload as WebChatStreamMeta)
  } else if (event === 'delta') {
    handlers.onDelta?.(typeof payload === 'string' ? payload : payload?.text || '')
  } else if (event === 'done') {
    handlers.onDone?.(payload as WebChatStreamDone)
  } else if (event === 'error') {
    const message = typeof payload === 'string' ? payload : payload?.message || 'Stream error'
    handlers.onError?.(message, payload?.persisted)
    return message
  }
  return null
}

export const webChatAPI = {
  getOptions,
  listSessions,
  createSession,
  patchSession,
  listMessages,
  deleteSession,
  streamMessage,
  regenerateMessage,
  reviseMessage,
  listProjects, createProject, patchProject, deleteProject,
  listTemplates, createTemplate, patchTemplate, deleteTemplate, copyTemplate,
  listMessageVersions, activateMessageVersion,
  adminListTemplates, adminCreateTemplate, adminPatchTemplate, adminDeleteTemplate,
}

export default webChatAPI
