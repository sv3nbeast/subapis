/**
 * Usage tracking API endpoints
 * Handles usage logs and statistics retrieval
 */

import { getLocale } from '@/i18n'
import { USER_UI_REQUEST_HEADER } from './adminUIRequest'
import { apiClient } from './client'
import { refreshAuthTokens } from './tokenRefresh'
import { buildApiUrl } from './url'
import type {
  ApiResponse,
  UsageLog,
  UsageQueryParams,
  UsageStatsResponse,
  PaginatedResponse,
  TrendDataPoint,
  ModelStat,
  GroupStat,
  UsageRequestType,
  UserErrorRequest,
  UserErrorRequestDetail,
  UserErrorListParams
} from '@/types'

// ==================== Dashboard Types ====================

export interface PlatformDashboardStats {
  platform: string
  total_requests: number
  total_tokens: number
  total_actual_cost: number
  today_requests: number
  today_tokens: number
  today_actual_cost: number
}

export interface UserDashboardStats {
  total_api_keys: number
  active_api_keys: number
  total_requests: number
  total_input_tokens: number
  total_output_tokens: number
  total_cache_creation_tokens: number
  total_cache_read_tokens: number
  total_tokens: number
  total_cost: number // 标准计费
  total_actual_cost: number // 实际扣除
  today_requests: number
  today_input_tokens: number
  today_output_tokens: number
  today_cache_creation_tokens: number
  today_cache_read_tokens: number
  today_tokens: number
  today_cost: number // 今日标准计费
  today_actual_cost: number // 今日实际扣除
  average_duration_ms: number
  rpm: number // 近5分钟平均每分钟请求数
  tpm: number // 近5分钟平均每分钟Token数
  by_platform?: PlatformDashboardStats[]
}

export interface TrendParams {
  start_date?: string
  end_date?: string
  granularity?: 'day' | 'hour'
  api_key_id?: number
  model?: string
  group_id?: number
  request_type?: UsageRequestType
  stream?: boolean
  native_compaction_v2?: boolean | null
  billing_type?: number | null
  billing_mode?: string | null
  timezone?: string
}

export interface TrendResponse {
  trend: TrendDataPoint[]
  start_date: string
  end_date: string
  granularity: string
}

export interface ModelStatsResponse {
  models: ModelStat[]
  start_date: string
  end_date: string
}

export interface ApiKeyDailyUsagePoint {
  date: string
  requests: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  total_tokens: number
  cost: number
  actual_cost: number
}

export interface ApiKeyDailyUsageResponse {
  items: ApiKeyDailyUsagePoint[]
  days: number
  start_date: string
  end_date: string
}

export interface UsageDashboardSnapshotV2Params extends TrendParams {
  include_trend?: boolean
  include_model_stats?: boolean
  include_group_stats?: boolean
}

export interface UsageDashboardSnapshotV2Response {
  generated_at: string
  start_date: string
  end_date: string
  granularity: string
  trend?: TrendDataPoint[]
  models?: ModelStat[]
  groups?: GroupStat[]
}

/**
 * List usage logs with optional filters
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param apiKeyId - Filter by API key ID
 * @returns Paginated list of usage logs
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  apiKeyId?: number
): Promise<PaginatedResponse<UsageLog>> {
  const params: UsageQueryParams = {
    page,
    page_size: pageSize
  }

  if (apiKeyId !== undefined) {
    params.api_key_id = apiKeyId
  }

  const { data } = await apiClient.get<PaginatedResponse<UsageLog>>('/usage', {
    params
  })
  return data
}

/**
 * Get usage logs with advanced query parameters
 * @param params - Query parameters for filtering and pagination
 * @returns Paginated list of usage logs
 */
export async function query(
  params: UsageQueryParams & { sort_by?: string; sort_order?: 'asc' | 'desc' },
  config: { signal?: AbortSignal } = {}
): Promise<PaginatedResponse<UsageLog>> {
  const { data } = await apiClient.get<PaginatedResponse<UsageLog>>('/usage', {
    ...config,
    params
  })
  return data
}

/**
 * Get usage statistics for a specific period
 * @param period - Time period ('today', 'week', 'month', 'year')
 * @param apiKeyId - Optional API key ID filter
 * @returns Usage statistics
 */
export async function getStats(
  paramsOrPeriod: (UsageQueryParams & { period?: string; timezone?: string }) | string = 'today',
  apiKeyId?: number
): Promise<UsageStatsResponse> {
  const params: Record<string, unknown> = typeof paramsOrPeriod === 'string'
    ? { period: paramsOrPeriod }
    : { ...paramsOrPeriod }

  if (apiKeyId !== undefined) {
    params.api_key_id = apiKeyId
  }

  const { data } = await apiClient.get<UsageStatsResponse>('/usage/stats', {
    params
  })
  return data
}

/**
 * Get usage statistics for a date range
 * @param startDate - Start date (YYYY-MM-DD format)
 * @param endDate - End date (YYYY-MM-DD format)
 * @param apiKeyId - Optional API key ID filter
 * @returns Usage statistics
 */
export async function getStatsByDateRange(
  startDate: string,
  endDate: string,
  apiKeyId?: number
): Promise<UsageStatsResponse> {
  const params: Record<string, unknown> = {
    start_date: startDate,
    end_date: endDate
  }

  if (apiKeyId !== undefined) {
    params.api_key_id = apiKeyId
  }

  const { data } = await apiClient.get<UsageStatsResponse>('/usage/stats', {
    params
  })
  return data
}

/**
 * Get usage by date range
 * @param startDate - Start date (YYYY-MM-DD format)
 * @param endDate - End date (YYYY-MM-DD format)
 * @param apiKeyId - Optional API key ID filter
 * @returns Usage logs within date range
 */
export async function getByDateRange(
  startDate: string,
  endDate: string,
  apiKeyId?: number
): Promise<PaginatedResponse<UsageLog>> {
  const params: UsageQueryParams = {
    start_date: startDate,
    end_date: endDate,
    page: 1,
    page_size: 100
  }

  if (apiKeyId !== undefined) {
    params.api_key_id = apiKeyId
  }

  const { data } = await apiClient.get<PaginatedResponse<UsageLog>>('/usage', {
    params
  })
  return data
}

/**
 * Get detailed usage log by ID
 * @param id - Usage log ID
 * @returns Usage log details
 */
export async function getById(id: number): Promise<UsageLog> {
  const { data } = await apiClient.get<UsageLog>(`/usage/${id}`)
  return data
}

// ==================== Dashboard API ====================

/**
 * Get user dashboard statistics
 * @returns Dashboard statistics for current user
 */
export async function getDashboardStats(): Promise<UserDashboardStats> {
  const { data } = await apiClient.get<UserDashboardStats>('/usage/dashboard/stats')
  return data
}

/**
 * Get user usage trend data
 * @param params - Query parameters for filtering
 * @returns Usage trend data for current user
 */
export async function getDashboardTrend(params?: TrendParams): Promise<TrendResponse> {
  const { data } = await apiClient.get<TrendResponse>('/usage/dashboard/trend', { params })
  return data
}

/**
 * Get user model usage statistics
 * @param params - Query parameters for filtering
 * @returns Model usage statistics for current user
 */
export async function getDashboardModels(params?: {
  start_date?: string
  end_date?: string
  api_key_id?: number
  model?: string
  model_source?: 'requested'
  group_id?: number
  request_type?: UsageRequestType
  stream?: boolean
  native_compaction_v2?: boolean | null
  billing_type?: number | null
  billing_mode?: string | null
  timezone?: string
}): Promise<ModelStatsResponse> {
  const { data } = await apiClient.get<ModelStatsResponse>('/usage/dashboard/models', { params })
  return data
}

/**
 * Get daily usage details for one API key owned by the current user.
 * @param apiKeyId - API key ID
 * @param days - Number of days to include (1-90)
 * @returns Daily usage detail rows
 */
export async function getMyApiKeyDailyUsage(
  apiKeyId: number,
  days: number = 30
): Promise<ApiKeyDailyUsageResponse> {
  const { data } = await apiClient.get<ApiKeyDailyUsageResponse>(
    `/user/api-keys/${apiKeyId}/usage/daily`,
    { params: { days } }
  )
  return data
}

export async function getDashboardSnapshotV2(
  params?: UsageDashboardSnapshotV2Params
): Promise<UsageDashboardSnapshotV2Response> {
  const { data } = await apiClient.get<UsageDashboardSnapshotV2Response>(
    '/usage/dashboard/snapshot-v2',
    { params }
  )
  return data
}

export interface BatchApiKeyUsageStats {
  api_key_id: number
  today_actual_cost: number
  total_actual_cost: number
}

export interface BatchApiKeysUsageResponse {
  stats: Record<string, BatchApiKeyUsageStats>
}

/**
 * Get batch usage stats for user's own API keys
 * @param apiKeyIds - Array of API key IDs
 * @param options - Optional request options
 * @returns Usage stats map keyed by API key ID
 */
export async function getDashboardApiKeysUsage(
  apiKeyIds: number[],
  options?: {
    signal?: AbortSignal
  }
): Promise<BatchApiKeysUsageResponse> {
  const { data } = await apiClient.post<BatchApiKeysUsageResponse>(
    '/usage/dashboard/api-keys-usage',
    {
      api_key_ids: apiKeyIds
    },
    {
      signal: options?.signal
    }
  )
  return data
}

// ==================== CSV Export (server-side streaming) ====================

/** Filters accepted by the streaming CSV export; pagination is never part of it. */
export type UsageExportParams = Omit<UsageQueryParams, 'page' | 'page_size'>

export interface UsageExportProgress {
  /** Bytes received so far. The server streams, so the total size is unknown up front. */
  receivedBytes: number
  /** Data rows received so far, derived from newline count (header excluded). */
  receivedRows: number
}

export interface UsageExportOptions {
  /** Aborting the signal cancels the download and the server-side query. */
  signal?: AbortSignal
  onProgress?: (progress: UsageExportProgress) => void
}

export interface UsageExportResult {
  blob: Blob
  filename: string
  /** Data rows contained in the CSV (header excluded). */
  rows: number
}

/** Normalized failure raised by {@link exportCsv}, mirroring the axios interceptor shape. */
export interface UsageExportError {
  status: number
  code?: number | string
  reason?: string
  message: string
  metadata?: Record<string, string>
  retryAfterSeconds?: number
}

export const USAGE_EXPORT_PATH = '/usage/export'
export const USAGE_EXPORT_MIME = 'text/csv;charset=utf-8;'

const NEWLINE_BYTE = 0x0a

function getUserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    return 'UTC'
  }
}

/**
 * Serialize export filters the same way the paginated list request does (axios drops
 * null/undefined, booleans become "true"/"false") and attach the browser timezone so the
 * server resolves date boundaries exactly like GET /usage.
 */
export function buildUsageExportQuery(params: UsageExportParams): URLSearchParams {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    query.set(key, String(value))
  }
  if (!query.has('timezone')) {
    query.set('timezone', getUserTimezone())
  }
  return query
}

/** Parse RFC 6266 `Content-Disposition` filenames (both `filename*=` and `filename=`). */
export function parseAttachmentFilename(header: string | null | undefined): string | null {
  if (!header) return null
  const extended = /filename\*=utf-8''([^;]+)/i.exec(header)
  if (extended) {
    try {
      const decoded = decodeURIComponent(extended[1].trim().replace(/^"|"$/g, ''))
      if (decoded) return decoded
    } catch {
      // fall through to the plain filename
    }
  }
  const plain = /filename=(?:"([^"]*)"|([^;]+))/i.exec(header)
  if (!plain) return null
  const value = (plain[1] ?? plain[2] ?? '').trim()
  return value || null
}

export function defaultUsageExportFilename(params: UsageExportParams): string {
  if (params.start_date && params.end_date) {
    return `usage_${params.start_date}_to_${params.end_date}.csv`
  }
  return 'usage_export.csv'
}

export function isUsageExportAborted(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const candidate = error as { name?: unknown; code?: unknown }
  return candidate.name === 'AbortError' || candidate.code === 'ERR_CANCELED'
}

function exportHeaders(token: string | null): Record<string, string> {
  const headers: Record<string, string> = {
    Accept: 'text/csv',
    'Accept-Language': getLocale(),
    [USER_UI_REQUEST_HEADER]: '1'
  }
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  return headers
}

async function requestExport(url: string, signal?: AbortSignal): Promise<Response> {
  const token = localStorage.getItem('auth_token')
  const response = await fetch(url, { method: 'GET', headers: exportHeaders(token), credentials: 'include', signal })
  if (response.status !== 401 || !localStorage.getItem('refresh_token')) {
    return response
  }
  // Mirror the axios client: refresh once, then retry with the rotated access token.
  let tokens
  try {
    tokens = await refreshAuthTokens({ failedAccessToken: token })
  } catch {
    const refreshFailed: UsageExportError = {
      status: 401,
      code: 'TOKEN_REFRESH_FAILED',
      message: 'Session expired. Please log in again.'
    }
    throw refreshFailed
  }
  return fetch(url, { method: 'GET', headers: exportHeaders(tokens.access_token), credentials: 'include', signal })
}

async function readExportError(response: Response): Promise<UsageExportError> {
  const error: UsageExportError = {
    status: response.status,
    message: response.statusText || 'Export failed'
  }
  const retryAfter = Number(response.headers.get('Retry-After'))
  if (Number.isFinite(retryAfter) && retryAfter > 0) {
    error.retryAfterSeconds = retryAfter
  }
  try {
    const payload = (await response.json()) as Partial<ApiResponse<unknown>> & {
      code?: number | string
      reason?: string
      metadata?: Record<string, string>
    }
    if (payload && typeof payload === 'object') {
      error.code = payload.code
      error.reason = payload.reason
      error.metadata = payload.metadata
      if (typeof payload.message === 'string' && payload.message) {
        error.message = payload.message
      }
    }
  } catch {
    // Non-JSON error body (proxy page, empty body): keep the HTTP status text.
  }
  return error
}

function countNewlines(chunk: Uint8Array): number {
  let count = 0
  for (let i = 0; i < chunk.length; i++) {
    if (chunk[i] === NEWLINE_BYTE) count++
  }
  return count
}

async function readCsvBody(response: Response, options: UsageExportOptions): Promise<{ blob: Blob; rows: number }> {
  const body = response.body
  if (!body || typeof body.getReader !== 'function') {
    const buffered = await response.blob()
    const bytes = new Uint8Array(await buffered.arrayBuffer())
    return { blob: new Blob([bytes], { type: USAGE_EXPORT_MIME }), rows: Math.max(0, countNewlines(bytes) - 1) }
  }

  const reader = body.getReader()
  const chunks: Uint8Array[] = []
  let receivedBytes = 0
  let newlines = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    if (!value || value.byteLength === 0) continue
    chunks.push(value)
    receivedBytes += value.byteLength
    newlines += countNewlines(value)
    options.onProgress?.({ receivedBytes, receivedRows: Math.max(0, newlines - 1) })
  }
  return { blob: new Blob(chunks, { type: USAGE_EXPORT_MIME }), rows: Math.max(0, newlines - 1) }
}

/**
 * Download the current user's usage records as CSV in a single streamed request.
 *
 * The server applies exactly the same filters as `query()`, so the export never triggers
 * the per-user panel rate limit the old page-by-page approach ran into. Pass `signal` to
 * cancel: the browser drops the connection and the server stops its query.
 */
export async function exportCsv(
  params: UsageExportParams,
  options: UsageExportOptions = {}
): Promise<UsageExportResult> {
  const url = `${buildApiUrl(USAGE_EXPORT_PATH)}?${buildUsageExportQuery(params).toString()}`
  const response = await requestExport(url, options.signal)
  if (!response.ok) {
    throw await readExportError(response)
  }
  const filename =
    parseAttachmentFilename(response.headers.get('Content-Disposition')) ?? defaultUsageExportFilename(params)
  const { blob, rows } = await readCsvBody(response, options)
  return { blob, filename, rows }
}

export async function listMyErrorRequests(
  params: UserErrorListParams
): Promise<PaginatedResponse<UserErrorRequest>> {
  const { data } = await apiClient.get<PaginatedResponse<UserErrorRequest>>('/usage/errors', {
    params
  })
  return data
}

export async function getMyErrorDetail(id: number): Promise<UserErrorRequestDetail> {
  const { data } = await apiClient.get<UserErrorRequestDetail>(`/usage/errors/${id}`)
  return data
}

export const usageAPI = {
  list,
  query,
  getStats,
  getStatsByDateRange,
  getByDateRange,
  getById,
  // Dashboard
  getDashboardStats,
  getDashboardTrend,
  getDashboardModels,
  getMyApiKeyDailyUsage,
  getDashboardSnapshotV2,
  getDashboardApiKeysUsage,
  // CSV export
  exportCsv,
  // Error requests
  listMyErrorRequests,
  getMyErrorDetail
}

export default usageAPI
