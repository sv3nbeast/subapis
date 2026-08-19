import { describe, expect, it } from 'vitest'
import { availableGroupPlatformLabel } from '../availableGroupPresentation'

describe('availableGroupPlatformLabel', () => {
  it('hides the internal Kiro provider name from the user catalogue', () => {
    expect(availableGroupPlatformLabel('kiro')).toBe('Anthropic')
    expect(availableGroupPlatformLabel(' KIRO ')).toBe('Anthropic')
  })

  it('preserves the normal public labels for other providers', () => {
    expect(availableGroupPlatformLabel('anthropic')).toBe('Anthropic')
    expect(availableGroupPlatformLabel('openai')).toBe('OpenAI')
    expect(availableGroupPlatformLabel('grok')).toBe('Grok')
  })
})
