import { beforeEach, describe, expect, it } from 'vitest'
import {
  getStablePublicUiSubject,
  publicUiV2CohortBucket,
  resolvePublicUiVersion,
  usePublicUiVersion,
} from '../usePublicUiVersion'

describe('resolvePublicUiVersion', () => {
  it('keeps legacy as the default in preview mode', () => {
    expect(resolvePublicUiVersion({
      mode: 'preview',
      percentage: 0,
      queryPreference: null,
      storedPreference: null,
      subject: 'visitor-1',
    })).toBe('legacy')
  })

  it('honors explicit preview and rollback preferences', () => {
    expect(resolvePublicUiVersion({
      mode: 'preview',
      percentage: 0,
      queryPreference: 'v2',
      storedPreference: null,
      subject: 'visitor-1',
    })).toBe('v2')
    expect(resolvePublicUiVersion({
      mode: 'full',
      percentage: 100,
      queryPreference: 'legacy',
      storedPreference: null,
      subject: 'visitor-1',
    })).toBe('legacy')
  })

  it('lets off mode override all v2 preferences', () => {
    expect(resolvePublicUiVersion({
      mode: 'off',
      percentage: 100,
      queryPreference: 'v2',
      storedPreference: 'v2',
      subject: 'visitor-1',
    })).toBe('legacy')
  })

  it('uses deterministic percentage cohorts', () => {
    const bucket = publicUiV2CohortBucket('visitor-42')
    expect(bucket).toBeGreaterThanOrEqual(0)
    expect(bucket).toBeLessThan(100)
    expect(publicUiV2CohortBucket('visitor-42')).toBe(bucket)
    expect(resolvePublicUiVersion({
      mode: 'percentage',
      percentage: bucket + 1,
      queryPreference: null,
      storedPreference: null,
      subject: 'visitor-42',
    })).toBe('v2')
  })
})

describe('usePublicUiVersion', () => {
  beforeEach(() => {
    localStorage.clear()
    window.history.replaceState({}, '', '/home')
  })

  it('creates and reuses a stable anonymous subject', () => {
    const first = getStablePublicUiSubject()
    const second = getStablePublicUiSubject()
    expect(first).not.toBe('anonymous')
    expect(second).toBe(first)
    expect(localStorage.getItem('sub2api:public-ui-anonymous-subject')).toBe(first)
  })

  it('persists a v2 query preference independently and removes only its query key', () => {
    window.history.replaceState({}, '', '/home?public_ui=v2&source=preview#models')
    const { publicUiVersion } = usePublicUiVersion('visitor-7')

    expect(publicUiVersion.value).toBe('v2')
    expect(localStorage.getItem('sub2api:public-ui-version:visitor-7')).toBe('v2')
    expect(window.location.search).toBe('?source=preview')
    expect(window.location.hash).toBe('#models')
    expect(localStorage.getItem('sub2api:ui-version:visitor-7')).toBeNull()
  })

  it('supports an explicit rollback and imperative switching', () => {
    localStorage.setItem('sub2api:public-ui-version:visitor-8', 'v2')
    window.history.replaceState({}, '', '/models?public_ui=legacy')
    const { publicUiVersion, useV2PublicUi, useLegacyPublicUi } = usePublicUiVersion('visitor-8')

    expect(publicUiVersion.value).toBe('legacy')
    expect(window.location.search).toBe('')

    useV2PublicUi()
    expect(publicUiVersion.value).toBe('v2')
    useLegacyPublicUi()
    expect(publicUiVersion.value).toBe('legacy')
  })
})
