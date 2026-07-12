import { describe, expect, it } from 'vitest'
import {
  PROVIDERS,
  PROVIDER_GROK,
} from '../channelMonitor'

describe('channel monitor providers', () => {
  it('includes Grok as a first-class provider', () => {
    expect(PROVIDER_GROK).toBe('grok')
    expect(PROVIDERS).toContain(PROVIDER_GROK)
  })
})
