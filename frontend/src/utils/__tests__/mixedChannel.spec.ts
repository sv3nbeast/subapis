import { describe, expect, it } from 'vitest'
import { needsMixedChannelCheck } from '@/utils/mixedChannel'

describe('needsMixedChannelCheck', () => {
  it.each(['anthropic', 'antigravity', 'kiro', 'droid'])(
    'enables mixed-channel confirmation for %s',
    (platform) => {
      expect(needsMixedChannelCheck(platform)).toBe(true)
    }
  )

  it('normalizes case and surrounding whitespace', () => {
    expect(needsMixedChannelCheck(' KIRO ')).toBe(true)
  })

  it.each(['openai', 'gemini', 'grok', '', undefined, null])(
    'does not enable mixed-channel confirmation for %s',
    (platform) => {
      expect(needsMixedChannelCheck(platform)).toBe(false)
    }
  )
})
