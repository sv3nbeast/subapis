import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(__dirname, '../../HomeView.vue'), 'utf8')

describe('HomeView public navigation entries', () => {
  it('orders pricing, model status, and documentation in the desktop navigation', () => {
    const header = source.slice(source.indexOf('<!-- Header -->'), source.indexOf('<Teleport'))
    const pricingIndex = header.indexOf('to="/models"')
    const statusIndex = header.indexOf('to="/monitor"')
    const docsIndex = header.indexOf('to="/docs"')

    expect(pricingIndex).toBeGreaterThan(-1)
    expect(statusIndex).toBeGreaterThan(pricingIndex)
    expect(docsIndex).toBeGreaterThan(statusIndex)
    expect(header).toContain("t('nav.modelStatus')")
    expect(header).toContain("t('nav.docs')")
  })
})
