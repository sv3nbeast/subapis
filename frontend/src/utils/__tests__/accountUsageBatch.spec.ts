import { describe, expect, it } from 'vitest'

import type { Account } from '@/types'
import { accountSupportsBatchUsage, isKiroRelayAccount } from '../accountUsageBatch'

const makeAccount = (overrides: Partial<Account>): Account => ({
  id: 1,
  name: 'test-account',
  platform: 'anthropic',
  type: 'oauth',
  credentials: {},
  extra: {},
  status: 'active',
  schedulable: true,
  ...overrides
} as Account)

describe('accountSupportsBatchUsage', () => {
  it('includes direct Kiro OAuth accounts', () => {
    expect(accountSupportsBatchUsage(makeAccount({ platform: 'kiro', type: 'oauth' }))).toBe(true)
  })

  it('includes direct Kiro API-key accounts without a relay base URL', () => {
    expect(accountSupportsBatchUsage(makeAccount({
      platform: 'kiro',
      type: 'apikey',
      credentials: {}
    }))).toBe(true)
  })

  it('does not probe Kiro relay API-key accounts', () => {
    const account = makeAccount({
      platform: 'kiro',
      type: 'apikey',
      credentials: { base_url: 'https://relay.example.com' }
    })
    expect(isKiroRelayAccount(account)).toBe(true)
    expect(accountSupportsBatchUsage(account)).toBe(false)
  })
})
