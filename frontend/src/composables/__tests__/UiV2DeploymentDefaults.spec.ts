import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..')
const dockerfile = readFileSync(resolve(repositoryRoot, 'Dockerfile'), 'utf8')
const deployScript = readFileSync(resolve(repositoryRoot, 'scripts/deploy-prod.sh'), 'utf8')
const rebuildScript = readFileSync(
  resolve(repositoryRoot, 'scripts/rebuild-prod-sub2api.sh'),
  'utf8'
)

describe('UI v2 production defaults', () => {
  it('builds both authenticated and public UI in full rollout mode by default', () => {
    expect(dockerfile).toContain('ARG VITE_UI_V2_ROLLOUT_MODE=full')
    expect(dockerfile).toContain('ARG VITE_PUBLIC_UI_V2_ROLLOUT_MODE=full')
  })

  it('keeps both production entry points on full rollout by default', () => {
    for (const script of [deployScript, rebuildScript]) {
      expect(script).toContain('UI_V2_ROLLOUT_MODE="${VITE_UI_V2_ROLLOUT_MODE:-full}"')
      expect(script).toContain(
        'PUBLIC_UI_V2_ROLLOUT_MODE="${VITE_PUBLIC_UI_V2_ROLLOUT_MODE:-full}"'
      )
    }
  })
})
