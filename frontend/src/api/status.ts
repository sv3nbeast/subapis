import { apiClient } from './client'

// === Types ===

export interface ProbeFailure {
  time: string
  error: string
}

export interface HourlyStat {
  hour: string
  success: number
  total: number
  avg_latency_ms: number
  failures?: ProbeFailure[]
}

export interface ModelStatus {
  model: string
  display_name: string
  current_status: 'operational' | 'degraded' | 'outage' | 'unknown'
  uptime_percentage: number
  hourly_stats: HourlyStat[]
}

export interface ServiceStatusResponse {
  overall_status: 'operational' | 'degraded' | 'major_outage' | 'unknown'
  models: ModelStatus[]
  last_updated: string | null
}

// === API ===

async function getStatus(): Promise<ServiceStatusResponse> {
  const { data } = await apiClient.get<ServiceStatusResponse>('/status')
  return data
}

export const statusAPI = { getStatus }
export default statusAPI
