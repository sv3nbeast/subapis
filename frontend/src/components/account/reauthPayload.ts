import type { Account, AccountType } from '@/types'

type JSONMap = Record<string, unknown>

export interface ReauthAccountPatch {
  type?: AccountType
  credentials?: JSONMap | null
  extra?: JSONMap | null
}

export interface ReauthAccountUpdatePayload {
  type?: AccountType
  credentials?: JSONMap
  extra?: JSONMap
}

function isRecord(value: unknown): value is JSONMap {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function omitEmptyReauthEntries(value: JSONMap): JSONMap {
  const result: JSONMap = {}
  for (const [key, entry] of Object.entries(value)) {
    if (entry !== undefined && entry !== null && entry !== '') {
      result[key] = entry
    }
  }
  return result
}

function mergeReauthRecord(existing: unknown, incoming: JSONMap): JSONMap {
  const base = isRecord(existing) ? existing : {}
  return {
    ...base,
    ...omitEmptyReauthEntries(incoming)
  }
}

export function buildReauthAccountUpdatePayload(
  account: Pick<Account, 'credentials' | 'extra'>,
  patch: ReauthAccountPatch
): ReauthAccountUpdatePayload {
  const payload: ReauthAccountUpdatePayload = {}

  if (patch.type !== undefined) {
    payload.type = patch.type
  }
  if (patch.credentials !== undefined && patch.credentials !== null) {
    payload.credentials = mergeReauthRecord(account.credentials, patch.credentials)
  }
  if (patch.extra !== undefined && patch.extra !== null) {
    payload.extra = mergeReauthRecord(account.extra, patch.extra)
  }

  return payload
}
