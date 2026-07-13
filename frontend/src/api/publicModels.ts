import { apiClient } from './client'
import type { UserSupportedModelPricing } from './channels'

export type PublicModelFamily = 'claude' | 'openai' | 'gemini' | 'grok' | 'other'

export interface PublicModel {
  name: string
  family: PublicModelFamily
  pricing: UserSupportedModelPricing | null
}

export interface PublicModelGroup {
  name: string
  subscription_type: string
  rate_multiplier: number
  peak_rate_enabled: boolean
  peak_start: string
  peak_end: string
  peak_rate_multiplier: number
  models: PublicModel[]
}

export interface PublicModelCatalog {
  groups: PublicModelGroup[]
}

function assertPublicModelCatalog(value: unknown): asserts value is PublicModelCatalog {
  if (!value || typeof value !== 'object' || !Array.isArray((value as PublicModelCatalog).groups)) {
    throw new Error('Invalid public model catalog response')
  }
}

export async function getPublicModels(options?: { signal?: AbortSignal }): Promise<PublicModelCatalog> {
  const { data } = await apiClient.get<unknown>('/public/models', {
    signal: options?.signal,
  })
  assertPublicModelCatalog(data)
  return data
}

export const publicModelsAPI = { getPublicModels }

export default publicModelsAPI
