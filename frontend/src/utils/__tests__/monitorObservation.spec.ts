import { describe, expect, it } from 'vitest'
import { monitorObservation, monitorOverall } from '../monitorObservation'

const checked = '2026-09-08T06:00:00Z'
const base = { primary_status: 'operational', primary_checked_at: checked, interval_seconds: 3600, jitter_seconds: 10 }
const at = (seconds: number) => Date.parse(checked) + seconds * 1000

describe('monitor observation, not account health', () => {
  it('does not call no-data, expired evidence or slow success an outage', () => {
    expect(monitorOverall([], at(0))).toBe('unknown')
    expect(monitorOverall([base], at(4000))).toBe('unknown')
    expect(monitorOverall([{ ...base, primary_status: 'degraded' }], at(0))).toBe('slow')
    expect(monitorOverall([{ ...base, primary_status: 'error' }], at(0))).toBe('degraded')
    expect(monitorOverall([base], at(0))).toBe('operational')
    expect(monitorOverall([{ ...base, primary_status: 'error' }, { ...base, primary_checked_at: null }], at(0))).toBe('degraded')
  })
  it('retains a single failure and labels slow success separately', () => {
    expect(monitorObservation({ ...base, primary_status: 'error' }, at(1)).key).toBe('failed')
    expect(monitorObservation({ ...base, primary_status: 'degraded' }, at(1)).key).toBe('slow')
    expect(monitorObservation({ ...base, primary_status: 'degraded', check_mode: 'quota' }, at(1)).key).toBe('quota')
    expect(monitorObservation({ ...base, primary_status: 'degraded', check_mode: 'quota_probe' }, at(1)).key).toBe('slow')
  })
  it('expires both red and green observations, respecting configured interval and jitter', () => {
    expect(monitorObservation(base, at(3600)).key).toBe('passed')
    expect(monitorObservation(base, at(3730)).key).toBe('passed')
    for (const primary_status of ['error', 'failed', 'operational', 'degraded']) {
      const result = monitorObservation({ ...base, primary_status }, at(3731))
      expect(result.key).toBe('stale')
      expect(result.badgeStatus).toBe('')
    }
  })
  it('does not invent health when observations are missing or malformed', () => {
    expect(monitorObservation({ ...base, primary_checked_at: null }, at(0)).key).toBe('unknown')
    expect(monitorObservation({ ...base, primary_checked_at: 'bad' }, at(0)).key).toBe('unknown')
    expect(monitorObservation({ ...base, primary_status: '' }, at(0)).key).toBe('unknown')
  })
  it('keeps old backend payloads readable without inventing an expiry', () => {
    expect(monitorObservation({ primary_status: 'error' }, at(5000)).key).toBe('failed')
  })
})
