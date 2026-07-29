import { readdirSync, readFileSync } from 'node:fs'
import { extname, join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

const sourceRoot = resolve(process.cwd(), 'src')

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      return entry.name === '__tests__' || entry.name === 'locales' ? [] : sourceFiles(path)
    }
    return ['.ts', '.vue'].includes(extname(entry.name)) && !entry.name.endsWith('.spec.ts')
      ? [path]
      : []
  })
}

function usedLocaleKeys(): string[] {
  const keys = new Set<string>()
  const staticTranslationCall = /(?:\bt|\$t|i18n\.global\.t)\(\s*(['"])([A-Za-z0-9_.-]+)\1/g

  for (const file of sourceFiles(sourceRoot)) {
    const source = readFileSync(file, 'utf8')
    for (const match of source.matchAll(staticTranslationCall)) {
      if (!match[2].endsWith('.')) keys.add(match[2])
    }
  }

  return [...keys].sort()
}

function missingKeys(locale: Record<string, unknown>, keys: string[]): string[] {
  return keys.filter((key) => {
    const value = key.split('.').reduce<unknown>((current, part) => {
      if (!current || typeof current !== 'object') return undefined
      return (current as Record<string, unknown>)[part]
    }, locale)
    return typeof value !== 'string' && !Array.isArray(value)
  })
}

describe('locale usage completeness', () => {
  it.each([
    ['en', en],
    ['zh', zh]
  ] as const)('%s locale defines every statically referenced key', (_name, locale) => {
    expect(missingKeys(locale, usedLocaleKeys())).toEqual([])
  })
})
