import { apiClient } from './client'

export interface WebChatPricingInterval {
  min_tokens: number
  max_tokens: number | null
  tier_label?: string
  input_price?: number | null
  output_price?: number | null
  cache_write_price?: number | null
  cache_read_price?: number | null
  per_request_price?: number | null
}

export interface WebChatModelPricing {
  billing_mode: string
  input_price?: number | null
  output_price?: number | null
  cache_write_price?: number | null
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
}

export interface WebChatSession {
  id: number
  user_id: number
  group_id: number
  group_name?: string
  platform?: string
  model: string
  title: string
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
  onDone?: () => void
  onError?: (message: string) => void
}

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

export async function listSessions(): Promise<WebChatSession[]> {
  const { data } = await apiClient.get<WebChatSession[]>('/web-chat/sessions')
  return data
}

export async function createSession(payload: { group_id: number; model: string }): Promise<WebChatSession> {
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
  content: string,
  handlers: WebChatStreamHandlers = {},
): Promise<void> {
  const token = localStorage.getItem('auth_token')
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'text/event-stream',
  }
  if (token) headers.Authorization = `Bearer ${token}`

  const response = await fetch(`${API_BASE_URL}/web-chat/sessions/${sessionID}/messages`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ content }),
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
    const chunks = buffer.split(/\n\n/)
    buffer = chunks.pop() || ''
    for (const chunk of chunks) {
      const errorMessage = dispatchSSEChunk(chunk, handlers)
      if (errorMessage) streamError = errorMessage
    }
  }
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
    handlers.onDone?.()
  } else if (event === 'error') {
    const message = typeof payload === 'string' ? payload : payload?.message || 'Stream error'
    handlers.onError?.(message)
    return message
  }
  return null
}

export const webChatAPI = {
  getOptions,
  listSessions,
  createSession,
  listMessages,
  deleteSession,
  streamMessage,
}

export default webChatAPI
