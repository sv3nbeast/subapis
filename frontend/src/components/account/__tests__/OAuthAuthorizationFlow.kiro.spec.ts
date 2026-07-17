import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import OAuthAuthorizationFlow from '../OAuthAuthorizationFlow.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() })
}))

function mountKiroFlow() {
  return mount(OAuthAuthorizationFlow, {
    props: {
      addMethod: 'oauth',
      platform: 'kiro',
      authUrl: 'https://app.kiro.dev/signin',
      sessionId: 'kiro-session',
      showCookieOption: false
    },
    global: {
      stubs: { Icon: true }
    }
  })
}

describe('OAuthAuthorizationFlow Kiro callbacks', () => {
  it('preserves the complete External IdP descriptor callback', async () => {
    const wrapper = mountKiroFlow()
    const callback =
      'http://localhost:49153/?code=portal-code&state=portal-state&login_option=external_idp' +
      '&client_id=client-id&issuer_url=https%3A%2F%2Flogin.microsoftonline.com%2Ftenant%2Fv2.0' +
      '&scopes=openid%20offline_access&login_hint=user%40example.com'

    await wrapper.get('textarea').setValue(callback)

    expect((wrapper.vm as any).authCode).toBe(callback)
    expect((wrapper.vm as any).oauthState).toBe('portal-state')
    expect((wrapper.vm as any).oauthLoginOption).toBe('external_idp')
    expect((wrapper.vm as any).oauthIssuerURL).toBe(
      'https://login.microsoftonline.com/tenant/v2.0'
    )
  })

  it('extracts the code from the final External IdP callback', async () => {
    const wrapper = mountKiroFlow()

    await wrapper
      .get('textarea')
      .setValue('http://localhost:3128/oauth/callback?code=final-code&state=idp-state')

    expect((wrapper.vm as any).authCode).toBe('final-code')
    expect((wrapper.vm as any).oauthState).toBe('idp-state')
    expect((wrapper.vm as any).oauthCallbackPath).toBe('/oauth/callback')
  })
})
