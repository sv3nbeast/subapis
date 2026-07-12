import { describe, expect, it } from 'vitest'
import { availabilityLevel, normalizeAvailability } from '@/utils/availability'

describe('availability helpers', () => {
  it.each([
    [100, 'excellent'],
    [99.9, 'excellent'],
    [99.89, 'stable'],
    [99, 'stable'],
    [98.99, 'unstable'],
    [95, 'unstable'],
    [94.99, 'critical'],
    [0, 'critical'],
  ] as const)('classifies %s as %s', (value, level) => {
    expect(availabilityLevel(value)).toBe(level)
  })

  it.each([null, undefined, Number.NaN, Number.POSITIVE_INFINITY])(
    'treats %s as no data',
    (value) => {
      expect(availabilityLevel(value)).toBe('noData')
      expect(normalizeAvailability(value)).toBeNull()
    },
  )

  it('clamps invalid percentage ranges for safe rendering', () => {
    expect(normalizeAvailability(-10)).toBe(0)
    expect(normalizeAvailability(120)).toBe(100)
  })
})
