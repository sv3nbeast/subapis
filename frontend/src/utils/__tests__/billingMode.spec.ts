import { describe, expect, it } from 'vitest'

import { BILLING_MODE_IMAGE, BILLING_MODE_TOKEN, getDisplayBillingMode, isImageUsage } from '@/utils/billingMode'

describe('billingMode utils', () => {
  it('treats image_count rows as image usage even when billed as tokens', () => {
    expect(isImageUsage({ image_count: 1, billing_mode: BILLING_MODE_TOKEN })).toBe(true)
  })

  it('keeps the stored billing mode for display', () => {
    expect(getDisplayBillingMode({ image_count: 1, billing_mode: BILLING_MODE_TOKEN })).toBe(BILLING_MODE_TOKEN)
  })

  it('falls back missing billing_mode image rows to image mode', () => {
    expect(getDisplayBillingMode({ image_count: 1, billing_mode: null })).toBe(BILLING_MODE_IMAGE)
  })

  it('does not treat non-image token rows as image usage', () => {
    expect(isImageUsage({ image_count: 0, billing_mode: BILLING_MODE_TOKEN })).toBe(false)
  })
})
