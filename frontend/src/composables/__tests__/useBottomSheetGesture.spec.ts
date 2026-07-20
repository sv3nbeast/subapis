import { describe, expect, it } from 'vitest'
import { projectMomentum, rubberbandOffset } from '../useBottomSheetGesture'

describe('bottom sheet gesture physics', () => {
  it('keeps overscroll continuous while adding progressive resistance', () => {
    const offset = rubberbandOffset(-120, 400)
    expect(offset).toBeLessThan(0)
    expect(Math.abs(offset)).toBeLessThan(120)
    expect(rubberbandOffset(0, 400)).toBe(0)
  })

  it('projects the release direction using exponential deceleration', () => {
    expect(projectMomentum(1000, 0.99)).toBeCloseTo(99)
    expect(projectMomentum(-1000, 0.99)).toBeCloseTo(-99)
  })
})
