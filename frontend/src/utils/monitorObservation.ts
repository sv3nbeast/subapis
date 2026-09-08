export interface MonitorObservationSource {
  primary_status: string
  primary_checked_at?: string | null
  interval_seconds?: number
  jitter_seconds?: number
  check_mode?: string
  probe_path?: string
}

// This describes evidence freshness, not a prediction of live availability.
// Keep raw statuses/history intact; a failed probe is never turned green.
export function monitorObservation(item: MonitorObservationSource, now = Date.now()) {
  const checked = item.primary_checked_at ? Date.parse(item.primary_checked_at) : NaN
  const interval = item.interval_seconds ?? 0
  // Allow one configured interval, its maximum jitter and the runner's 63s
  // request budget plus queue/persistence grace. Not an "expected next probe".
  const expiresAfter = (interval + Math.max(0, item.jitter_seconds ?? 0) + 120) * 1000
  const stale = interval > 0 && Number.isFinite(checked) && now - checked > expiresAfter
  const unknown = !item.primary_status || (interval > 0 && !Number.isFinite(checked))
  const key = stale ? 'stale' : unknown ? 'unknown'
    : item.check_mode === 'quota' ? 'quota'
    : item.primary_status === 'operational' ? 'passed'
    : item.primary_status === 'degraded' ? 'slow'
    : 'failed'
  return { key, badgeStatus: stale || unknown ? '' : item.primary_status, stale, unknown }
}

export function monitorOverall(items: MonitorObservationSource[], now = Date.now()): 'operational' | 'slow' | 'degraded' | 'unknown' {
  if (!items.length) return 'unknown'
  const observations = items.map(item => monitorObservation(item, now))
  if (observations.some(o => o.badgeStatus === 'error' || o.badgeStatus === 'failed')) return 'degraded'
  if (observations.some(o => o.stale || o.unknown)) return 'unknown'
  if (items.some(item => item.primary_status === 'degraded')) {
    return items.some(item => item.primary_status === 'degraded' && item.check_mode === 'quota') ? 'degraded' : 'slow'
  }
  return items.every(item => item.primary_status === 'operational') ? 'operational' : 'unknown'
}
