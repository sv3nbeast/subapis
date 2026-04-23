import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const currentDir = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(currentDir, '../../..')
const routerFile = path.join(repoRoot, 'src/router/index.ts')
const sidebarFile = path.join(repoRoot, 'src/components/layout/AppSidebar.vue')

function readFile(filePath: string): string {
  return fs.readFileSync(filePath, 'utf8')
}

function collectFiles(dir: string, exts: Set<string>): string[] {
  const entries = fs.readdirSync(dir, { withFileTypes: true })
  const files: string[] = []
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      files.push(...collectFiles(fullPath, exts))
      continue
    }
    if (exts.has(path.extname(entry.name))) {
      files.push(fullPath)
    }
  }
  return files
}

function extractRoutePatterns(source: string): string[] {
  return [...source.matchAll(/path:\s*'([^']+)'/g)].map((match) => match[1])
}

function extractSidebarPaths(source: string): string[] {
  return [...source.matchAll(/path:\s*'([^']+)'/g)]
    .map((match) => match[1])
    .filter((value) => value.startsWith('/'))
}

function extractLiteralRouteReferences(source: string): string[] {
  const patterns = [
    /router\.(?:push|replace)\(\s*['"]([^'"]+)['"]/g,
    /window\.location\.href\s*=\s*['"]([^'"]+)['"]/g,
  ]
  return patterns.flatMap((pattern) =>
    [...source.matchAll(pattern)].map((match) => match[1]),
  )
}

function shouldCheckPath(value: string): boolean {
  return (
    value.startsWith('/') &&
    !value.startsWith('/api/') &&
    !value.startsWith('/v1/') &&
    !value.startsWith('/build/') &&
    !value.includes('://')
  )
}

function pathMatchesPattern(actualPath: string, routePattern: string): boolean {
  if (routePattern.includes('/:pathMatch(')) {
    return false
  }

  const normalizedActual = actualPath.replace(/[?#].*$/, '')
  const normalizedPattern = routePattern.replace(/[?#].*$/, '')

  if (normalizedActual === normalizedPattern) {
    return true
  }

  const actualParts = normalizedActual.split('/').filter(Boolean)
  const patternParts = normalizedPattern.split('/').filter(Boolean)

  if (actualParts.length !== patternParts.length) {
    return false
  }

  return patternParts.every((part, index) => part.startsWith(':') || part === actualParts[index])
}

describe('route references integrity', () => {
  it('covers all sidebar navigation paths with declared routes', () => {
    const routePatterns = extractRoutePatterns(readFile(routerFile))
    const sidebarPaths = extractSidebarPaths(readFile(sidebarFile))

    const missing = sidebarPaths.filter(
      (value) => !routePatterns.some((pattern) => pathMatchesPattern(value, pattern)),
    )

    expect(missing).toEqual([])
  })

  it('covers literal frontend route jumps with declared routes', () => {
    const routePatterns = extractRoutePatterns(readFile(routerFile))
    const sourceFiles = collectFiles(path.join(repoRoot, 'src'), new Set(['.ts', '.vue']))
    const references = sourceFiles
      .flatMap((filePath) => extractLiteralRouteReferences(readFile(filePath)))
      .filter(shouldCheckPath)

    const missing = [...new Set(references)].filter(
      (value) => !routePatterns.some((pattern) => pathMatchesPattern(value, pattern)),
    )

    expect(missing).toEqual([])
  })
})
