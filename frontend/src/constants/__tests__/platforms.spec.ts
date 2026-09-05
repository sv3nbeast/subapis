import { describe, expect, it } from 'vitest'
import {
  COMPOSITE_ROUTE_PLATFORM_OPTIONS,
  CONCRETE_PLATFORM_OPTIONS,
  CONCRETE_PLATFORM_VALUES,
  GROUP_PLATFORM_OPTIONS
} from '@/constants/platforms'

const concretePlatforms = [
  'anthropic',
  'openai',
  'gemini',
  'antigravity',
  'kiro',
  'droid',
  'grok',
  'kimi',
  'zhipu',
  'deepseek'
]

describe('platform option catalogs', () => {
  it('exposes every concrete account platform', () => {
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(concretePlatforms)
  })

  it('adds composite for group-backed filters', () => {
    expect(GROUP_PLATFORM_OPTIONS.map((option) => option.value)).toEqual([
      ...concretePlatforms,
      'composite'
    ])
  })

  it('keeps the value catalog aligned with the concrete options', () => {
    expect(CONCRETE_PLATFORM_VALUES).toEqual(concretePlatforms)
  })

  it('limits composite routes to backend-supported targets', () => {
    expect(COMPOSITE_ROUTE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual([
      'anthropic',
      'openai',
      'gemini',
      'antigravity',
      'grok',
      'kimi',
      'zhipu',
      'deepseek'
    ])
  })
})
