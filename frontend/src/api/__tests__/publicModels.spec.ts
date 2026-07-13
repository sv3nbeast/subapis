import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '@/api/client'
import { getPublicModels } from '@/api/publicModels'

vi.mock('@/api/client', () => ({
  apiClient: { get: vi.fn() },
}))

describe('public models API', () => {
  beforeEach(() => vi.mocked(apiClient.get).mockReset())

  it('loads the anonymous public catalog', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({ data: { groups: [] } })

    await expect(getPublicModels()).resolves.toEqual({ groups: [] })
    expect(apiClient.get).toHaveBeenCalledWith('/public/models', { signal: undefined })
  })

  it('rejects malformed responses', async () => {
    vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [] } })
    await expect(getPublicModels()).rejects.toThrow('Invalid public model catalog response')
  })
})
