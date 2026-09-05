import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('Composite channel platform options', () => {
  it('derives pricing and model-mapping sections from the full platform catalog', () => {
    const source = readFileSync(resolve('src/views/admin/ChannelsView.vue'), 'utf8')
    expect(source).toMatch(/import .*CONCRETE_PLATFORM_VALUES.*from '@\/constants\/platforms'/)
    expect(source).toMatch(/platformOrder[^=]*=\s*\[\.\.\.CONCRETE_PLATFORM_VALUES\]/)
  })
})
