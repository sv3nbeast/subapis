/**
 * Detect whether the current device is mobile.
 * Uses navigator.userAgentData (modern API) with UA regex fallback.
 */
export function detectMobileDevice(input?: {
  navigator?: Partial<Navigator> & Record<string, unknown>
  matchMedia?: (query: string) => { matches: boolean }
}): boolean {
  const nav = (input?.navigator ?? navigator) as Partial<Navigator> & Record<string, unknown>
  if (nav.userAgentData && typeof (nav.userAgentData as Record<string, unknown>).mobile === 'boolean') {
    return (nav.userAgentData as Record<string, unknown>).mobile as boolean
  }

  const userAgent = String(nav.userAgent || '')
  if (/Android|iPhone|iPad|iPod|Mobile/i.test(userAgent)) {
    return true
  }

  if (
    /Macintosh/i.test(userAgent) &&
    String(nav.platform || '').toLowerCase().includes('mac') &&
    Number(nav.maxTouchPoints || 0) > 1
  ) {
    return true
  }

  const matchMediaFn =
    input?.matchMedia ??
    (typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia.bind(window)
      : null)

  if (matchMediaFn) {
    const coarsePointer = matchMediaFn('(pointer: coarse)').matches
    const noHover = matchMediaFn('(hover: none)').matches
    if (Number(nav.maxTouchPoints || 0) > 1 && (coarsePointer || noHover)) {
      return true
    }
  }

  return false
}

export function isMobileDevice(): boolean {
  return detectMobileDevice()
}
