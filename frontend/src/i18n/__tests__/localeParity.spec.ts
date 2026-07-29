import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

function collectLeafPaths(value: unknown, prefix = ''): string[] {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return prefix ? [prefix] : []
  }

  return Object.entries(value).flatMap(([key, child]) =>
    collectLeafPaths(child, prefix ? `${prefix}.${key}` : key)
  )
}

describe('locale key parity', () => {
  it('provides a Chinese value for every English locale key', () => {
    const enPaths = new Set(collectLeafPaths(en))
    const zhPaths = new Set(collectLeafPaths(zh))

    expect([...enPaths].filter((path) => !zhPaths.has(path)).sort()).toEqual([])
  })
})
