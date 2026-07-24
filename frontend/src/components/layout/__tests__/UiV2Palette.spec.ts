import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const currentDir = dirname(fileURLToPath(import.meta.url))
const appStyles = readFileSync(resolve(currentDir, '../../../style.css'), 'utf8')
const publicStyles = readFileSync(resolve(currentDir, '../../../styles/public-v2.css'), 'utf8')
const publicLayout = readFileSync(resolve(currentDir, '../../public/PublicLayout.vue'), 'utf8')
const announcementDialog = readFileSync(
  resolve(currentDir, '../../public/PublicAnnouncementDialog.vue'),
  'utf8'
)
const homeView = readFileSync(resolve(currentDir, '../../../views/HomeView.vue'), 'utf8')
const publicHome = readFileSync(resolve(currentDir, '../../public/PublicHomeV2.vue'), 'utf8')
const dataTable = readFileSync(resolve(currentDir, '../../common/DataTable.vue'), 'utf8')

describe('UI v2 dark palette', () => {
  it('keeps distinct page, card, raised, and interaction surfaces', () => {
    expect(appStyles).toContain('--ui2-page: #161618;')
    expect(appStyles).toContain('--ui2-surface-soft: #232326;')
    expect(appStyles).toContain('--ui2-surface-raised: #2d2d32;')
    expect(appStyles).toContain('--ui2-surface-recessed: #19191c;')
  })

  it('keeps secondary text readable in both themes', () => {
    expect(appStyles).toContain('--ui2-text-secondary: #4f4f55;')
    expect(appStyles).toContain('--ui2-text-tertiary: #727279;')
    expect(appStyles).toContain('--ui2-text-secondary: #dedee3;')
    expect(appStyles).toContain('--ui2-text-tertiary: #aaaab2;')
  })

  it('reserves blue for selection while primary commands invert with the theme', () => {
    expect(appStyles).toContain('--ui2-accent: #409cff;')
    expect(appStyles).toContain('--ui2-accent-strong: #0f72d6;')
    expect(appStyles).toContain('--ui2-command: #1d1d1f;')
    expect(appStyles).toContain('--ui2-command: #f1f1f3;')
    expect(appStyles).toContain('background: var(--ui2-command) !important;')
    expect(appStyles).toContain('background: var(--ui2-surface-recessed);')
  })

  it('keeps dashboard metrics data-first with neutral corner icons', () => {
    expect(appStyles).toContain('.ui-v2 .dashboard-metric-card {')
    expect(appStyles).toContain('position: absolute;')
    expect(appStyles).toContain('top: 16px;')
    expect(appStyles).toContain('right: 16px;')
    expect(appStyles).toContain('font-variant-numeric: tabular-nums;')
  })

  it('exposes palette tokens to teleported controls and neutralizes legacy table colors', () => {
    expect(appStyles).toContain('html.ui-v2-active,')
    expect(appStyles).toContain('html.dark.ui-v2-active,')
    expect(appStyles).toContain('html.ui-v2-active .select-dropdown-portal')
    expect(appStyles).toContain(
      'background: color-mix(in srgb, var(--ui2-surface), var(--ui2-page) 35%) !important;'
    )
    expect(appStyles).toContain('background: transparent !important;')
    expect(dataTable).toContain('data-table-empty')
    expect(appStyles).toContain('.ui-v2 .data-table-empty {')
  })

  it('keeps public pages and overlays aligned with the console palette', () => {
    for (const source of [publicStyles, publicLayout, announcementDialog]) {
      expect(source).toContain('--ui2-surface: #29292e;')
      expect(source).toContain('--ui2-text-secondary: #c5c5ca;')
      expect(source).toContain('--ui2-accent: #409cff;')
    }
  })

  it('gives the public home an explicit dark brand and proof surface', () => {
    expect(homeView).toContain('--ui2-text: #f4f4f6;')
    expect(homeView).toContain('background: var(--ui2-page) !important;')
    expect(publicHome).toContain(':global(.home-dark .home-v2 .home-v2-proof)')
    expect(publicHome).toContain('background: #232326;')
    expect(publicHome).toContain('color: #10211d;')
  })
})
