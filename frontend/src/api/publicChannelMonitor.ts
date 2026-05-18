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

function assertMonitorListResponse(value: unknown): asserts value is PublicMonitorListResponse {
  if (!value || typeof value !== 'object' || !Array.isArray((value as PublicMonitorListResponse).items)) {
    throw new Error('Invalid public monitor response')
  }
}

function assertMonitorDetailResponse(value: unknown): asserts value is PublicMonitorDetail {
  if (!value || typeof value !== 'object' || !Array.isArray((value as PublicMonitorDetail).models)) {
    throw new Error('Invalid public monitor detail response')
  }
}

export async function listPublicChannelMonitors(options?: { signal?: AbortSignal }): Promise<PublicMonitorListResponse> {
  const { data } = await apiClient.get<unknown>('/public/channel-monitors', {
    signal: options?.signal,
  })
  assertMonitorListResponse(data)
  return data
}

export async function getPublicChannelMonitorStatus(id: number): Promise<PublicMonitorDetail> {
  const { data } = await apiClient.get<unknown>(`/public/channel-monitors/${id}/status`)
  assertMonitorDetailResponse(data)
  return data
}

export const publicChannelMonitorAPI = {
  list: listPublicChannelMonitors,
  status: getPublicChannelMonitorStatus,
}

export default publicChannelMonitorAPI
