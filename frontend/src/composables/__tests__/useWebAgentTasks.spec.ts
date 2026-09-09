import { defineComponent, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useWebAgentTasks } from '../useWebAgentTasks'
const api = vi.hoisted(() => ({ listTasks: vi.fn(), listArtifacts: vi.fn(), getTaskEvents: vi.fn(), createTask: vi.fn(), cancelTask: vi.fn(), deleteArtifact: vi.fn() }))
vi.mock('@/api/webAgent', () => ({ ...api, isTaskTerminal: (status: string) => ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(status) }))
beforeEach(() => {
  vi.clearAllMocks(); sessionStorage.clear()
  api.listTasks.mockResolvedValue({ items: [], next_before: 0 }); api.listArtifacts.mockResolvedValue({ items: [], next_before: 0 }); api.getTaskEvents.mockResolvedValue({ items: [], next_after: 0 })
})
function setup() {
  const session = ref<number | null>(1), user = ref<number | undefined>(3)
  let state!: ReturnType<typeof useWebAgentTasks>
  const wrapper = mount(defineComponent({ setup() { state = useWebAgentTasks(session, user); return () => null } }))
  return { state, wrapper, session, user }
}
describe('file task observation and submission', () => {
  it('ignores a late response belonging to another session', async () => {
    let first!: (value: unknown) => void
    api.listTasks.mockImplementation(({ session_id }) => session_id === 1 ? new Promise(resolve => { first = resolve }) : Promise.resolve({ items: [{ id: 20, status: 'succeeded', session_id: 2 }], next_before: 0 }))
    const view = setup(); await flushPromises(); view.session.value = 2; await flushPromises()
    first({ items: [{ id: 10, status: 'succeeded', session_id: 1 }], next_before: 0 }); await flushPromises()
    expect(view.state.tasks.value.map(t => t.id)).toEqual([20]); view.wrapper.unmount()
    expect(api.cancelTask).not.toHaveBeenCalled()
  })
  it('recovers the same submission intent after reload without an automatic replay', async () => {
    api.createTask.mockRejectedValueOnce(new Error('connection lost'))
    const first = setup(); await flushPromises()
    await expect(first.state.create({ kind: 'document', prompt: 'a report' })).rejects.toThrow('connection lost')
    const key = api.createTask.mock.calls[0]![1].idempotency_key
    first.wrapper.unmount()
    api.createTask.mockResolvedValue({ id: 11, session_id: 1, status: 'queued' })
    const resumed = setup(); await flushPromises()
    expect(api.createTask).toHaveBeenCalledTimes(1)
    expect(resumed.state.pending.value?.input.idempotency_key).toBe(key)
    await resumed.state.retryPending()
    expect(api.createTask.mock.calls[1]![1].idempotency_key).toBe(key)
    expect(resumed.state.pending.value).toBeNull()
    expect(sessionStorage.length).toBe(0); resumed.wrapper.unmount()
  })
  it('does not expose another signed-in user’s pending intent', async () => {
    api.createTask.mockRejectedValue(new Error('connection lost'))
    const view = setup(); await flushPromises()
    await expect(view.state.create({ kind: 'document', prompt: 'private request' })).rejects.toThrow()
    view.user.value = 4; await flushPromises()
    expect(view.state.pending.value).toBeNull(); view.wrapper.unmount()
  })
  it('only cancels execution when explicitly requested', async () => {
    api.cancelTask.mockResolvedValue({ id: 11, session_id: 1, status: 'cancel_requested' })
    const view = setup(); await flushPromises()
    await view.state.cancel(11)
    expect(api.cancelTask).toHaveBeenCalledTimes(1); expect(api.cancelTask).toHaveBeenCalledWith(11)
    view.wrapper.unmount()
  })
  it('does not resurrect a confirmed deletion from an older list response', async () => {
    const view = setup(); await flushPromises()
    let finish!: (value: unknown) => void
    api.listArtifacts.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const refresh = view.state.refresh(); view.state.markArtifactDeleted(12)
    finish({ items: [{ id: 12, session_id: 1 }], next_before: 0 }); await refresh
    expect(view.state.artifacts.value).toEqual([]); view.wrapper.unmount()
  })
  it('does not reopen exhausted history pagination on refresh', async () => {
    api.listTasks.mockResolvedValue({ items: [{ id: 50, status: 'succeeded' }], next_before: 50 })
    const view = setup(); await flushPromises()
    api.listTasks.mockResolvedValueOnce({ items: [{ id: 1, status: 'succeeded' }], next_before: 0 })
    await view.state.older(); expect(view.state.nextBefore.value).toBe(0)
    await view.state.refresh(); expect(view.state.nextBefore.value).toBe(0); view.wrapper.unmount()
  })
})
