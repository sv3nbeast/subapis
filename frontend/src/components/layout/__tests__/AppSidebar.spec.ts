import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('AppSidebar header styles', () => {
  it('keeps a single account identity instead of duplicating the workspace', () => {
    expect(componentSource).not.toContain('class="ui-v2-workspace-switcher"')
    expect(componentSource).not.toContain('ui-v2-sidebar-brand-subtitle')
    expect(componentSource).toContain('class="ui-v2-sidebar-profile"')
  })

  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })

  it('keeps the v2 sidebar and workspace on shared compact width tokens', () => {
    expect(styleSource).toContain('--ui2-sidebar-width: 208px;')
    expect(styleSource).toContain('--ui2-sidebar-collapsed-width: 64px;')
    expect(styleSource).toContain('width: var(--ui2-sidebar-width) !important;')
    expect(styleSource).toContain('margin-left: var(--ui2-sidebar-width) !important;')
    expect(styleSource).toContain('width: min(calc(100vw - 3.5rem), 17.5rem) !important;')
  })

  it('keeps the default logo square and blends its transparent edge into the artwork', () => {
    expect(componentSource).toContain("'sidebar-logo-default': sidebarLogoIsDefault")
    expect(componentSource).toContain('const sidebarLogoIsDefault = computed(() => {')
    expect(styleSource).toContain('flex: 0 0 32px;')
    expect(styleSource).toContain('.ui-v2 .sidebar-logo-default {')
    expect(styleSource).toContain('background: #0d2949;')
    expect(styleSource).toContain('object-fit: cover;')
  })

  it('matches production navigation typography without rem-driven layout changes', () => {
    const linkBlock = styleSource.match(/\.ui-v2 \.sidebar-link \{[\s\S]*?\n\}/)?.[0]
    const activeBlock = styleSource.match(/\.ui-v2 \.sidebar-link-active \{[\s\S]*?\n\}/)?.[0]
    const sectionTitleBlock = styleSource.match(/\.ui-v2 \.sidebar \.sidebar-section-title \{[\s\S]*?\n\}/)?.[0]

    expect(sectionTitleBlock).toContain('min-height: 20px;')
    expect(sectionTitleBlock).toContain('font-size: 9.375px;')
    expect(sectionTitleBlock).toContain('font-weight: 650;')
    expect(sectionTitleBlock).toContain('text-transform: uppercase;')
    expect(linkBlock).toContain('min-height: 38px;')
    expect(linkBlock).toContain('gap: 10px;')
    expect(linkBlock).toContain('margin-bottom: 2px;')
    expect(linkBlock).toContain('padding: 0 10px;')
    expect(linkBlock).toContain('font-weight: 500;')
    expect(linkBlock).toContain('font-size: 12.1875px;')
    expect(linkBlock).toContain('font-synthesis: auto;')
    expect(linkBlock).toContain('font-variation-settings: normal;')
    expect(linkBlock).toContain('line-height: 20px;')
    expect(linkBlock).toContain('transform: none;')
    expect(linkBlock).toContain('will-change: auto;')
    expect(activeBlock).toContain('font-size: 12.1875px;')
    expect(activeBlock).toContain('font-weight: 620;')
    expect(activeBlock).toContain('line-height: 20px;')
    expect(activeBlock).toContain('color: var(--ui2-text-secondary) !important;')
  })

  it('uses color feedback without scaling sidebar text on press', () => {
    const pressedBlock = styleSource.match(/\.ui-v2 \.sidebar-link:active \{[\s\S]*?\n\}/)?.[0]

    expect(pressedBlock).toContain('background: var(--ui2-surface-recessed) !important;')
    expect(pressedBlock).toContain('transform: none;')
    expect(pressedBlock).not.toContain('scale(')
  })

  it('keeps the sidebar collapse icon at a fixed pixel size', () => {
    const collapseIconBlock = styleSource.match(/\.ui-v2 \.sidebar \.ui-v2-sidebar-collapse svg \{[\s\S]*?\n\}/)?.[0]

    expect(collapseIconBlock).toContain('width: 16px;')
    expect(collapseIconBlock).toContain('height: 16px;')
  })

  it('matches the production dashboard metric value typography', () => {
    const metricBlock = styleSource.match(/\.ui-v2 \.dashboard-metric-card \.text-xl \{[\s\S]*?\n\}/)?.[0]

    expect(metricBlock).toContain('font-size: 22px;')
    expect(metricBlock).toContain('font-weight: 650;')
    expect(metricBlock).toContain('line-height: 1.2;')
  })
})

describe('AppSidebar navigation responsiveness', () => {
  it('uses optimistic active feedback and hover prefetch hooks for menu links', () => {
    expect(componentSource).toContain('const pendingActivePath = ref<string | null>(null)')
    expect(componentSource).toContain('const activePath = computed(() => pendingActivePath.value || route.path)')
    expect(componentSource).toContain('function prefetchNavTarget(path: string)')
    expect(componentSource).toContain('@pointerenter="prefetchNavTarget(item.path)"')
    expect(componentSource).toContain('@focus="prefetchNavTarget(item.path)"')
  })

  it('keeps active and pressed states GPU-friendly', () => {
    expect(styleSource).toContain('will-change: background-color, color, transform;')
    expect(styleSource).toContain('.sidebar-link-active::before')
    expect(styleSource).toContain('transform: translateX(2px) scale(0.985);')
  })
})
