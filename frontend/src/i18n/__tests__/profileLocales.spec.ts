import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

describe('profile locale keys', () => {
  it('contains zh labels for the rebuilt profile cards', () => {
    expect(zh.profile.basicsTitle).toBe('基本资料')
    expect(zh.profile.avatar.title).toBe('头像')
    expect(zh.profile.authBindings.title).toBe('绑定登录方式')
    expect(zh.profile.authBindings.providers.wechat).toBe('微信')
    expect(zh.profile.balanceNotify.extraEmailsHint).toContain('3')
  })

  it('contains en labels for the rebuilt profile cards', () => {
    expect(en.profile.basicsTitle).toBe('Basics')
    expect(en.profile.avatar.title).toBe('Avatar')
    expect(en.profile.authBindings.title).toBe('Connected Sign-In Methods')
    expect(en.profile.authBindings.providers.wechat).toBe('WeChat')
    expect(en.profile.balanceNotify.extraEmailsHint).toContain('3')
  })
})
