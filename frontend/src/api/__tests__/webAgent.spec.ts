import { beforeEach, describe, expect, it, vi } from 'vitest'
import { cancelTask, createTask, getTaskEvents, isTaskTerminal, getArtifactBlob, getArtifactVersions, deleteArtifact, getArtifactStorageUsage } from '../webAgent'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), delete: vi.fn() }))
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
  it('preserves the source version without overwriting its artifact', async () => {
    const request = { kind: 'slides' as const, prompt: 'Change the title', source_artifact_id: 12, idempotency_key: 'revision-action' }
    await createTask(2, request)
    expect(api.post).toHaveBeenCalledWith('/web-chat/sessions/2/tasks', request)
    await getArtifactVersions(12, 8)
    expect(api.get).toHaveBeenCalledWith('/web-chat/artifacts/12/versions', { params: { before: 8 }, signal: undefined })
  })
  it('carries the selected prompt template as task metadata', async () => {
    const request = { kind: 'document' as const, prompt: 'Create a report', template_id: 7, idempotency_key: 'template-action' }
    await createTask(2, request)
    expect(api.post).toHaveBeenCalledWith('/web-chat/sessions/2/tasks', request)
  })
  it('fetches preview and downloads through the authenticated client and forwards aborts', async () => {
    const signal = new AbortController().signal
    const blob = new Blob(['%PDF-test'], { type: 'application/pdf' })
    api.get.mockResolvedValue({ data: blob })
    expect(await getArtifactBlob(12, true, signal)).toBe(blob)
    expect(api.get).toHaveBeenLastCalledWith('/web-chat/artifacts/12/preview', { responseType: 'blob', signal })
    await getArtifactBlob(12, false, signal)
    expect(api.get).toHaveBeenLastCalledWith('/web-chat/artifacts/12/download', { responseType: 'blob', signal })
  })
  it('deletes only the selected version and leaves quota truth to the server', async () => {
    api.delete.mockResolvedValue({ data: { deleted: true, cleanup_pending: true } })
    expect(await deleteArtifact(12)).toEqual({ deleted: true, cleanup_pending: true })
    expect(api.delete).toHaveBeenCalledWith('/web-chat/artifacts/12')
    const signal = new AbortController().signal
    await getArtifactStorageUsage(signal)
    expect(api.get).toHaveBeenLastCalledWith('/web-chat/artifact-storage', { signal })
    expect(api.post).not.toHaveBeenCalled()
  })
})
