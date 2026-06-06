import { apiClient } from '../client'

export interface DroidAuthUrlResponse {
  session_id: string
  verification_uri?: string
  verification_uri_complete?: string
  user_code?: string
  expires_in?: number
  interval?: number
  pending?: boolean
  retry_after?: number
  message?: string
  error?: string
}

export interface DroidTokenInfo {
  access_token?: string
  refresh_token?: string
  expires_at?: string
  token_type?: string
  [key: string]: unknown
}

export async function generateAuthUrl(payload: {
  proxy_id?: number
}): Promise<DroidAuthUrlResponse> {
  const { data } = await apiClient.post<DroidAuthUrlResponse>('/admin/droid/oauth/auth-url', payload)
  return data
}

export async function exchangeCode(payload: {
  session_id: string
  proxy_id?: number
}): Promise<DroidAuthUrlResponse | DroidTokenInfo> {
  const { data } = await apiClient.post<DroidAuthUrlResponse | DroidTokenInfo>('/admin/droid/oauth/exchange-code', payload)
  return data
}

export default {
  generateAuthUrl,
  exchangeCode
}
