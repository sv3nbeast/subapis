import { apiClient } from '../client'

export interface CursorAuthUrlResponse {
  auth_url: string
  session_id: string
}

/** 轮询结果：pending 为 true 表示用户还没在浏览器完成登录。 */
export interface CursorTokenInfo {
  pending?: boolean
  access_token?: string
  refresh_token?: string
  machine_id?: string
  mac_machine_id?: string
  expires_at?: string
  user_id?: string
  email?: string
}

export async function generateAuthUrl(payload: {
  proxy_id?: number
}): Promise<CursorAuthUrlResponse> {
  const { data } = await apiClient.post<CursorAuthUrlResponse>(
    '/admin/cursor/oauth/auth-url',
    payload
  )
  return data
}

export async function pollAuthorization(payload: {
  session_id: string
  proxy_id?: number
}): Promise<CursorTokenInfo> {
  const { data } = await apiClient.post<CursorTokenInfo>('/admin/cursor/oauth/poll', payload)
  return data
}

export default {
  generateAuthUrl,
  pollAuthorization
}
