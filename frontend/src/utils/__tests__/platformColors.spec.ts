import { describe, expect, it } from 'vitest'

import {
  platformAccentColor,
  platformBadgeClass,
  platformLabel,
  platformTextClass
} from '@/utils/platformColors'

describe('platformColors CN providers', () => {
  it.each([
    ['kimi', 'Kimi', '#ec4899', 'pink'],
    ['zhipu', 'Zhipu GLM', '#6366f1', 'indigo'],
    ['deepseek', 'DeepSeek', '#14b8a6', 'teal']
  ])('uses the configured palette for %s', (platform, label, accent, color) => {
    expect(platformLabel(platform)).toBe(label)
    expect(platformAccentColor(platform)).toBe(accent)
    expect(platformBadgeClass(platform)).toContain(color)
    expect(platformTextClass(platform)).toContain(color)
  })
})
