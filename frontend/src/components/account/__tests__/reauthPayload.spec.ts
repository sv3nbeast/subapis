import { describe, expect, it } from 'vitest'
import { buildReauthAccountUpdatePayload } from '../reauthPayload'

describe('buildReauthAccountUpdatePayload', () => {
  it('preserves account settings while replacing refreshed token fields', () => {
    const payload = buildReauthAccountUpdatePayload(
      {
        credentials: {
          access_token: 'old-access',
          refresh_token: 'old-refresh',
          model_mapping: { 'claude-opus-4-7': 'claude-opus-4-7' },
          intercept_warmup_requests: true
        },
        extra: {
          org_uuid: 'old-org',
          account_uuid: 'old-account',
          session_id_masking_enabled: true,
          enable_tls_fingerprint: true,
          tls_fingerprint_profile_id: -1,
          cache_ttl_override_enabled: true,
          cache_ttl_override_target: '1h'
        }
      } as any,
      {
        type: 'oauth',
        credentials: {
          access_token: 'new-access',
          expires_at: '2000'
        },
        extra: {
          org_uuid: 'new-org',
          account_uuid: 'new-account'
        }
      }
    )

    expect(payload).toEqual({
      type: 'oauth',
      credentials: {
        access_token: 'new-access',
        refresh_token: 'old-refresh',
        expires_at: '2000',
        model_mapping: { 'claude-opus-4-7': 'claude-opus-4-7' },
        intercept_warmup_requests: true
      },
      extra: {
        org_uuid: 'new-org',
        account_uuid: 'new-account',
        session_id_masking_enabled: true,
        enable_tls_fingerprint: true,
        tls_fingerprint_profile_id: -1,
        cache_ttl_override_enabled: true,
        cache_ttl_override_target: '1h'
      }
    })
  })

  it('does not let empty token fields erase existing values', () => {
    const payload = buildReauthAccountUpdatePayload(
      {
        credentials: {
          refresh_token: 'old-refresh',
          scope: 'old-scope',
          project_id: 'old-project'
        },
        extra: {
          privacy_mode: 'set',
          name: 'old-name'
        }
      } as any,
      {
        credentials: {
          refresh_token: undefined,
          scope: 'new-scope',
          project_id: '',
          token_type: null
        },
        extra: {
          privacy_mode: undefined,
          email: '',
          name: 'new-name'
        }
      }
    )

    expect(payload.credentials).toEqual({
      refresh_token: 'old-refresh',
      scope: 'new-scope',
      project_id: 'old-project',
      token_type: undefined
    })
    expect(payload.extra).toEqual({
      privacy_mode: 'set',
      email: undefined,
      name: 'new-name'
    })
  })

  it('omits maps that are not part of the reauthorization patch', () => {
    const payload = buildReauthAccountUpdatePayload(
      {
        credentials: {
          access_token: 'old-access',
          model_mapping: { old: 'mapping' }
        },
        extra: {
          session_id_masking_enabled: true
        }
      } as any,
      {
        credentials: {
          access_token: 'new-access'
        }
      }
    )

    expect(payload).toEqual({
      credentials: {
        access_token: 'new-access',
        model_mapping: { old: 'mapping' }
      }
    })
  })
})
