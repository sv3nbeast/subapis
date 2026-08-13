/**
 * Admin Grok/xAI API endpoints
 * Handles xAI OAuth flows for administrators.
 */

import { apiClient } from '../client'
import type { GrokBillingSnapshot } from '@/types'
import type { Account, CreateAccountRequest } from '@/types'

export interface GrokAuthUrlResponse {
  auth_url: string
  session_id: string
  state: string
}

export interface GrokAuthUrlRequest {
  proxy_id?: number
  redirect_uri?: string
}

export interface GrokOAuthCapabilities {
  password_auth_enabled: boolean
}

const GROK_AUTHORIZATION_TIMEOUT_MS = 120_000

export async function getCapabilities(): Promise<GrokOAuthCapabilities> {
  const { data } = await apiClient.get<GrokOAuthCapabilities>('/admin/grok/oauth/capabilities')
  return data
}

export interface GrokExchangeCodeRequest {
  session_id: string
  state: string
  code: string
  proxy_id?: number
  redirect_uri?: string
}

export interface GrokDeviceAuthorizationStartResponse {
  session_id: string
  user_code: string
  verification_uri: string
  verification_uri_complete?: string
  interval_seconds: number
  expires_at: number
}

export interface GrokDeviceAuthorizationPollResponse {
  status: 'pending' | 'authorized'
  retry_after_seconds?: number
}

export type GrokCompleteDeviceAccountRequest = Omit<
  CreateAccountRequest,
  'platform' | 'type'
> & {
  session_id: string
}

export interface GrokTokenInfo {
  access_token?: string
  refresh_token?: string
  token_type?: string
  id_token?: string
  expires_at?: number | string
  expires_in?: number
  scope?: string
  client_id?: string
  email?: string
  user_id?: string
  team_id?: string
  identity_key?: string
  subscription_tier?: string
  entitlement_status?: string
  base_url?: string
  [key: string]: unknown
}

export interface GrokSSOToOAuthRequest {
  sso_tokens: string[]
  name?: string
  notes?: string | null
  proxy_id?: number | null
  group_ids?: number[]
  credentials?: Record<string, unknown>
  extra?: Record<string, unknown>
  concurrency?: number
  load_factor?: number
  priority?: number
  rate_multiplier?: number
  expires_at?: number | null
  auto_pause_on_expired?: boolean
}

export interface GrokSSOToOAuthItemResult {
  index: number
  name?: string
  email?: string
  account?: unknown
  error?: string
}

export interface GrokSSOToOAuthResponse {
  created: GrokSSOToOAuthItemResult[]
  failed: GrokSSOToOAuthItemResult[]
}

const GROK_SSO_IMPORT_CONCURRENCY = 3
const GROK_SSO_IMPORT_TIMEOUT_PER_BATCH_MS = 90_000
const GROK_SSO_IMPORT_TIMEOUT_BUFFER_MS = 90_000

export function getGrokSSOImportTimeout(keyCount: number): number {
  const batches = Math.ceil(Math.max(1, keyCount) / GROK_SSO_IMPORT_CONCURRENCY)
  return batches * GROK_SSO_IMPORT_TIMEOUT_PER_BATCH_MS + GROK_SSO_IMPORT_TIMEOUT_BUFFER_MS
}

export interface GrokQuotaWindow {
  limit?: number | null
  remaining?: number | null
  reset_unix?: number | null
  reset_at?: string | null
}

export interface GrokQuotaSnapshot {
  requests?: GrokQuotaWindow | null
  tokens?: GrokQuotaWindow | null
  retry_after_seconds?: number | null
  subscription_tier?: string
  entitlement_status?: string
  status_code?: number
  headers?: Record<string, string>
  headers_observed: boolean
  observation_source?: string
  last_probe_at?: string
  last_headers_seen_at?: string
  updated_at: string
}

export interface GrokQuotaProbeResult {
  source: 'billing'
  billing?: GrokBillingSnapshot | null
  status_code?: number
  reset_supported: boolean
  fetched_at: number
}

export interface GrokQuotaResetResult {
  supported: boolean
  code: string
  message: string
}

export async function generateAuthUrl(
  payload: GrokAuthUrlRequest
): Promise<GrokAuthUrlResponse> {
  const { data } = await apiClient.post<GrokAuthUrlResponse>(
    '/admin/grok/oauth/auth-url',
    payload
  )
  return data
}

export async function exchangeCode(payload: GrokExchangeCodeRequest): Promise<GrokTokenInfo> {
  const { data } = await apiClient.post<GrokTokenInfo>(
    '/admin/grok/oauth/exchange-code',
    payload
  )
  return data
}

export async function startDeviceAuthorization(payload: {
  proxy_id?: number
  account_id?: number
}): Promise<GrokDeviceAuthorizationStartResponse> {
  const { data } = await apiClient.post<GrokDeviceAuthorizationStartResponse>(
    '/admin/grok/oauth/device/start',
    payload
  )
  return data
}

export async function pollDeviceAuthorization(
  sessionId: string
): Promise<GrokDeviceAuthorizationPollResponse> {
  const { data } = await apiClient.post<GrokDeviceAuthorizationPollResponse>(
    '/admin/grok/oauth/device/poll',
    { session_id: sessionId }
  )
  return data
}

export async function completeDeviceAccount(
  payload: GrokCompleteDeviceAccountRequest
): Promise<Account> {
  const { data } = await apiClient.post<Account>(
    '/admin/grok/oauth/device/create-account',
    payload
  )
  return data
}

export async function completeDeviceReauthorization(
  accountId: number,
  sessionId: string
): Promise<Account> {
  const { data } = await apiClient.post<Account>(
    `/admin/grok/accounts/${accountId}/device-reauthorize`,
    { session_id: sessionId }
  )
  return data
}

/** Validate a browser SSO cookie and exchange it for Build OAuth tokens. */
export async function validateSSOToken(
  ssoToken: string,
  proxyId?: number | null
): Promise<GrokTokenInfo> {
  const payload: Record<string, unknown> = { sso_token: ssoToken }
  if (proxyId) payload.proxy_id = proxyId
  const { data } = await apiClient.post<GrokTokenInfo>(
    '/admin/grok/oauth/sso-token',
    payload,
    { timeout: GROK_AUTHORIZATION_TIMEOUT_MS }
  )
  return data
}

/**
 * Password login to ephemeral SSO to Build OAuth. The password is sent only
 * for this request and is never persisted in account credentials.
 */
export async function authorizePassword(
  emailAndPassword: string,
  proxyId?: number | null
): Promise<GrokTokenInfo> {
  const separator = '----'
  const separatorIndex = emailAndPassword.indexOf(separator)
  const email = (
    separatorIndex >= 0
      ? emailAndPassword.slice(0, separatorIndex)
      : emailAndPassword
  ).trim()
  const password = separatorIndex >= 0
    ? emailAndPassword.slice(separatorIndex + separator.length)
    : ''
  const payload: Record<string, unknown> = { email, password }
  if (proxyId) payload.proxy_id = proxyId
  const { data } = await apiClient.post<GrokTokenInfo>(
    '/admin/grok/oauth/password',
    payload,
    { timeout: GROK_AUTHORIZATION_TIMEOUT_MS }
  )
  return data
}

export async function refreshGrokToken(
  refreshToken: string,
  proxyId?: number | null
): Promise<GrokTokenInfo> {
  const payload: Record<string, unknown> = { refresh_token: refreshToken }
  if (proxyId) payload.proxy_id = proxyId

  const { data } = await apiClient.post<GrokTokenInfo>(
    '/admin/grok/oauth/refresh-token',
    payload
  )
  return data
}

export async function queryQuota(id: number): Promise<GrokQuotaProbeResult> {
  const { data } = await apiClient.get<GrokQuotaProbeResult>(`/admin/grok/accounts/${id}/quota`)
  return data
}

export async function resetQuota(id: number): Promise<GrokQuotaResetResult> {
  const { data } = await apiClient.post<GrokQuotaResetResult>(`/admin/grok/accounts/${id}/reset-quota`)
  return data
}

export async function createFromSSO(payload: GrokSSOToOAuthRequest): Promise<GrokSSOToOAuthResponse> {
  const { data } = await apiClient.post<GrokSSOToOAuthResponse>(
    '/admin/grok/sso-to-oauth',
    payload,
    { timeout: getGrokSSOImportTimeout(payload.sso_tokens.length) }
  )
  return data
}

export default {
  generateAuthUrl,
  getCapabilities,
  exchangeCode,
  startDeviceAuthorization,
  pollDeviceAuthorization,
  completeDeviceAccount,
  completeDeviceReauthorization,
  refreshGrokToken,
  queryQuota,
  resetQuota,
  createFromSSO,
  validateSSOToken,
  authorizePassword
}
