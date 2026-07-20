import { beforeEach, describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { resolveUiVersion, uiV2CohortBucket, useUiVersion } from '../useUiVersion'

describe('resolveUiVersion', () => {
  it('keeps the classic interface as the preview-mode default', () => {
    expect(resolveUiVersion({
      mode: 'preview',
      percentage: 0,
      queryPreference: null,
      storedPreference: null,
      subject: '42',
    })).toBe('legacy')
  })

  it('honors explicit preview and rollback preferences', () => {
    expect(resolveUiVersion({
      mode: 'preview',
      percentage: 0,
      queryPreference: 'v2',
      storedPreference: null,
      subject: '42',
    })).toBe('v2')

    expect(resolveUiVersion({
      mode: 'full',
      percentage: 100,
      queryPreference: null,
      storedPreference: 'legacy',
      subject: '42',
    })).toBe('legacy')
  })

  it('lets the emergency off mode override every v2 preference', () => {
    expect(resolveUiVersion({
      mode: 'off',
      percentage: 100,
      queryPreference: 'v2',
      storedPreference: 'v2',
      subject: '42',
    })).toBe('legacy')
  })

  it('assigns percentage rollout cohorts deterministically', () => {
    const bucket = uiV2CohortBucket('user-42')
    expect(bucket).toBeGreaterThanOrEqual(0)
    expect(bucket).toBeLessThan(100)
    expect(uiV2CohortBucket('user-42')).toBe(bucket)

    expect(resolveUiVersion({
      mode: 'percentage',
      percentage: bucket + 1,
      queryPreference: null,
      storedPreference: null,
      subject: 'user-42',
    })).toBe('v2')
  })
})

describe('useUiVersion account preferences', () => {
  beforeEach(() => {
    localStorage.clear()
    window.history.replaceState({}, '', '/dashboard')
  })

  it('rebinds the preference when the authenticated account changes', () => {
    localStorage.setItem('sub2api:ui-version:7', 'v2')
    localStorage.setItem('sub2api:ui-version:8', 'legacy')
    const subject = ref<number | null>(7)
    const { uiVersion } = useUiVersion(subject)

    expect(uiVersion.value).toBe('v2')

    subject.value = 8
    expect(uiVersion.value).toBe('legacy')
  })

  it('persists an explicit preview after a delayed account restore', () => {
    window.history.replaceState({}, '', '/dashboard?ui=v2')
    const subject = ref<number | null>(null)
    const { uiVersion } = useUiVersion(subject)

    expect(uiVersion.value).toBe('v2')

    subject.value = 42
    expect(uiVersion.value).toBe('v2')
    expect(localStorage.getItem('sub2api:ui-version:42')).toBe('v2')
  })
})
