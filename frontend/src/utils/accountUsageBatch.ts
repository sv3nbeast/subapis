import type { Account } from '@/types'

/** Kiro API-key accounts with a base URL are external relay accounts. */
export const isKiroRelayAccount = (account: Account): boolean => {
  return (
    account.platform === 'kiro' &&
    account.type === 'apikey' &&
    typeof account.credentials?.base_url === 'string' &&
    account.credentials.base_url.trim() !== ''
  )
}

/**
 * Whether an account can be loaded by the account-table usage batch endpoint.
 *
 * Kiro relay API-key accounts are external Anthropic-compatible upstreams and
 * must not trigger an upstream Kiro quota probe. Direct Kiro OAuth/API-key
 * accounts are supported by AccountUsageService.GetUsage and must be included
 * here so the table does not suppress their usage-window request.
 */
export const accountSupportsBatchUsage = (account: Account): boolean => {
  if (account.platform === 'anthropic') {
    return account.type === 'oauth' || account.type === 'setup-token'
  }
  if (account.platform === 'gemini') return true
  if (account.platform === 'antigravity') return account.type === 'oauth'
  if (account.platform === 'openai') return account.type === 'oauth'
  if (account.platform === 'grok') return account.type === 'oauth'
  if (account.platform === 'kiro') {
    return !isKiroRelayAccount(account) && (account.type === 'oauth' || account.type === 'apikey')
  }
  return false
}
