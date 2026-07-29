import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ProfilePasskeyCard from '../ProfilePasskeyCard.vue'

const { listMock, showErrorMock } = vi.hoisted(() => ({
  listMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.mock('@/api', () => ({
  passkeyAPI: {
    isSupported: () => true,
    list: listMock,
    register: vi.fn(),
    rename: vi.fn(),
    remove: vi.fn()
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

function mountCard(enabled: boolean) {
  return mount(ProfilePasskeyCard, {
    props: { enabled },
    global: { stubs: { Icon: true } }
  })
}

describe('ProfilePasskeyCard', () => {
  beforeEach(() => {
    listMock.mockReset()
    showErrorMock.mockReset()
  })

  it('does not request credentials when passkeys are disabled', async () => {
    mountCard(false)
    await flushPromises()

    expect(listMock).not.toHaveBeenCalled()
    expect(showErrorMock).not.toHaveBeenCalled()
  })

  it('silences a PASSKEY_DISABLED race returned in the reason field', async () => {
    listMock.mockRejectedValue({ code: 403, reason: 'PASSKEY_DISABLED' })

    mountCard(true)
    await flushPromises()

    expect(listMock).toHaveBeenCalledOnce()
    expect(showErrorMock).not.toHaveBeenCalled()
  })

  it('still reports unexpected credential loading failures', async () => {
    listMock.mockRejectedValue({ code: 500, reason: 'INTERNAL_ERROR' })

    mountCard(true)
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('profile.passkey.loadFailed')
  })
})
