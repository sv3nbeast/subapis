import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(__dirname, '../../HomeView.vue'), 'utf8')

describe('HomeView public model market entry', () => {
  it('gates both model market links behind the opt-in feature flag', () => {
    expect(source).toContain('FeatureFlags.publicModelMarket')
    expect(source.match(/to="\/models"/g)).toHaveLength(2)
    expect(source.match(/v-if="publicModelMarketEnabled"/g)).toHaveLength(2)
  })
})
