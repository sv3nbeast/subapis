import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

describe('OpenAI WS mode locale descriptions', () => {
  it('documents the global v2 router requirement for account WS modes', () => {
    expect(zh.admin.accounts.openai.wsModeDesc).toContain('mode_router_v2_enabled')
    expect(zh.admin.accounts.openai.wsModeDesc).toContain('http_bridge')
    expect(en.admin.accounts.openai.wsModeDesc).toContain('mode_router_v2_enabled')
    expect(en.admin.accounts.openai.wsModeDesc).toContain('http_bridge')
  })
})
