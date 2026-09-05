import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(__dirname, '../../HomeView.vue'), 'utf8')

describe('HomeView public model market entry', () => {
  it('keeps every plaza entry behind its intended opt-in feature gate', () => {
    expect(source).toContain('FeatureFlags.publicModelMarket')
    const entries = source.match(/<router-link\b[^>]*\bto="\/model-plaza"[^>]*>/g) || []
    expect(entries).toHaveLength(3)
    expect(entries.map(entry => entry.match(/v-if="([^"]+)"/)?.[1])).toEqual([
      'showModelPlazaEntry',
      'showModelPlazaEntry || publicModelMarketEnabled',
      'publicModelMarketEnabled'
    ])
    expect(source).toContain('modelPlazaEnabled.value && (isAuthenticated.value || !modelPlazaRequiresAuth.value)')
  })
})
