import { apiClient } from './client'
import type { Provider, MonitorStatus } from './admin/channelMonitor'

export interface PublicMonitorExtraModel {
  model: string
  status: MonitorStatus
  latency_ms: number | null
}

export interface PublicMonitorTimelinePoint {
  status: MonitorStatus
  latency_ms: number | null
  ping_latency_ms: number | null
  checked_at: string
}

export interface PublicMonitorView {
  id: number
  name: string
  provider: Provider
  group_name: string
  primary_model: string
  primary_status: MonitorStatus
  primary_latency_ms: number | null
  primary_ping_latency_ms: number | null
  availability_7d: number
  extra_models: PublicMonitorExtraModel[]
  timeline: PublicMonitorTimelinePoint[]
}

export interface PublicMonitorListResponse {
  items: PublicMonitorView[]
}

export interface PublicMonitorModelDetail {
  model: string
  latest_status: MonitorStatus
  latest_latency_ms: number | null
  availability_7d: number
  availability_15d: number
  availability_30d: number
  avg_latency_7d_ms: number | null
}

export interface PublicMonitorDetail {
  id: number
  name: string
  provider: Provider
  group_name: string
  models: PublicMonitorModelDetail[]
}

export async function listPublicChannelMonitors(options?: { signal?: AbortSignal }): Promise<PublicMonitorListResponse> {
  const { data } = await apiClient.get<PublicMonitorListResponse>('/public/channel-monitors', {
    signal: options?.signal,
  })
  return data
}

export async function getPublicChannelMonitorStatus(id: number): Promise<PublicMonitorDetail> {
  const { data } = await apiClient.get<PublicMonitorDetail>(`/public/channel-monitors/${id}/status`)
  return data
}

export const publicChannelMonitorAPI = {
  list: listPublicChannelMonitors,
  status: getPublicChannelMonitorStatus,
}

export default publicChannelMonitorAPI
