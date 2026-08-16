import type { CodexUsageSnapshot } from '@/types'

export interface ResolvedCodexUsageWindow {
  usedPercent: number | null
  resetAt: string | null
}

type WindowKind = '5h' | '7d'

function asNumber(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return null
}

function asString(value: unknown): string | null {
  if (typeof value !== 'string') return null
  const trimmed = value.trim()
  return trimmed === '' ? null : trimmed
}

function asISOTime(value: unknown): string | null {
  const raw = asString(value)
  if (!raw) return null
  const date = new Date(raw)
  return Number.isNaN(date.getTime()) ? null : date.toISOString()
}

function resolveLegacyWindow(
  snapshot: Record<string, unknown>,
  kind: WindowKind
): { used: number | null; resetAfterSeconds: number | null } {
  const primaryWindow = asNumber(snapshot.codex_primary_window_minutes)
  const secondaryWindow = asNumber(snapshot.codex_secondary_window_minutes)
  const primaryUsed = asNumber(snapshot.codex_primary_used_percent)
  const secondaryUsed = asNumber(snapshot.codex_secondary_used_percent)
  const primaryReset = asNumber(snapshot.codex_primary_reset_after_seconds)
  const secondaryReset = asNumber(snapshot.codex_secondary_reset_after_seconds)

  if (kind === '5h') {
    if (primaryWindow != null && primaryWindow <= 360) {
      return { used: primaryUsed, resetAfterSeconds: primaryReset }
    }
    if (secondaryWindow != null && secondaryWindow <= 360) {
      return { used: secondaryUsed, resetAfterSeconds: secondaryReset }
    }
    return { used: secondaryUsed, resetAfterSeconds: secondaryReset }
  }

  if (primaryWindow != null && primaryWindow >= 10000) {
    return { used: primaryUsed, resetAfterSeconds: primaryReset }
  }
  if (secondaryWindow != null && secondaryWindow >= 10000) {
    return { used: secondaryUsed, resetAfterSeconds: secondaryReset }
  }
  return { used: primaryUsed, resetAfterSeconds: primaryReset }
}

function resolveFromSeconds(snapshot: Record<string, unknown>, resetAfterSeconds: number | null): string | null {
  if (resetAfterSeconds == null) return null
  const updatedAt = asString(snapshot.codex_usage_updated_at)
  const base = updatedAt ? new Date(updatedAt) : new Date()
  if (Number.isNaN(base.getTime())) return null
  return new Date(base.getTime() + Math.max(0, resetAfterSeconds) * 1000).toISOString()
}

function applyExpiredRule(window: ResolvedCodexUsageWindow, now: Date): ResolvedCodexUsageWindow {
  if (window.usedPercent == null || !window.resetAt) return window
  const resetDate = new Date(window.resetAt)
  if (Number.isNaN(resetDate.getTime()) || resetDate.getTime() > now.getTime()) return window
  return { usedPercent: 0, resetAt: resetDate.toISOString() }
}

/**
 * Resolve the persisted Codex usage snapshot for degraded UI rendering.
 * The live /usage response remains authoritative whenever it is available.
 */
export function resolveCodexUsageWindow(
  snapshot: (CodexUsageSnapshot & Record<string, unknown>) | null | undefined,
  kind: WindowKind,
  now: Date = new Date()
): ResolvedCodexUsageWindow {
  if (!snapshot) return { usedPercent: null, resetAt: null }

  const values = snapshot as Record<string, unknown>
  const prefix = kind === '5h' ? 'codex_5h' : 'codex_7d'
  let usedPercent = asNumber(values[`${prefix}_used_percent`])
  let resetAfterSeconds = asNumber(values[`${prefix}_reset_after_seconds`])
  let resetAt = asISOTime(values[`${prefix}_reset_at`])

  if (usedPercent == null || (resetAfterSeconds == null && !resetAt)) {
    const legacy = resolveLegacyWindow(values, kind)
    if (usedPercent == null) usedPercent = legacy.used
    if (resetAfterSeconds == null) resetAfterSeconds = legacy.resetAfterSeconds
  }

  if (!resetAt) resetAt = resolveFromSeconds(values, resetAfterSeconds)
  return applyExpiredRule({ usedPercent, resetAt }, now)
}
