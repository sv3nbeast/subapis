export type AvailabilityLevel = 'excellent' | 'stable' | 'unstable' | 'critical' | 'noData'

/**
 * Keep availability display and health thresholds consistent across cards,
 * dialogs, and any future summary widgets.
 */
export function normalizeAvailability(value: number | null | undefined): number | null {
  if (value == null || !Number.isFinite(value)) return null
  return Math.min(100, Math.max(0, value))
}

export function availabilityLevel(value: number | null | undefined): AvailabilityLevel {
  const normalized = normalizeAvailability(value)
  if (normalized == null) return 'noData'
  if (normalized >= 99.9) return 'excellent'
  if (normalized >= 99) return 'stable'
  if (normalized >= 95) return 'unstable'
  return 'critical'
}
