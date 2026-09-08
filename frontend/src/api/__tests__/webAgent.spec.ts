import { beforeEach, describe, expect, it, vi } from 'vitest'
import { cancelTask, createTask, getTaskEvents, isTaskTerminal } from '../webAgent'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../client', () => ({ apiClient: api }))
beforeEach(() => { vi.clearAllMocks(); api.get.mockResolvedValue({ data: { items: [], next_after: 9 } }); api.post.mockResolvedValue({ data: { id: 1 } }) })
describe('durable task API', () => {
  it('preserves the operation key across caller-requested transport retries', async () => {
    const request = { kind: 'document' as const, prompt: 'Create a report', idempotency_key: 'one-user-action' }
    await createTask(2, request)
    await createTask(2, request)
    expect(api.post.mock.calls[0]).toEqual(api.post.mock.calls[1])
    expect(api.post.mock.calls[0]?.[1].idempotency_key).toBe('one-user-action')
  })
  it('separates aborting observation from cancelling execution', async () => {
    const controller = new AbortController()
    await getTaskEvents(1, 9, controller.signal)
    controller.abort()
    expect(api.post).not.toHaveBeenCalled()
    expect(api.get).toHaveBeenCalledWith('/web-chat/tasks/1/events', { params: { after: 9 }, signal: controller.signal })
    await cancelTask(1)
    expect(api.post).toHaveBeenCalledWith('/web-chat/tasks/1/cancel')
  })
  it('does not mistake cancellation requests or lost execution for success', () => {
    expect(isTaskTerminal('cancel_requested')).toBe(false)
    expect(isTaskTerminal('running')).toBe(false)
    expect(isTaskTerminal('interrupted')).toBe(true)
    expect(isTaskTerminal('cancelled')).toBe(true)
  })
})
