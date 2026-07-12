import { beforeEach, describe, expect, it, vi } from 'vitest'
import webChatAPI, { type WebChatDocument } from '@/api/webChat'
import { useWebChatDocuments } from '../useWebChatDocuments'

vi.mock('@/api/webChat', () => ({
  default: {
    deleteDocument: vi.fn().mockResolvedValue(undefined),
    getDocument: vi.fn(),
    retryDocument: vi.fn(),
    uploadSessionDocument: vi.fn(),
  },
}))

const documentFixture = (id: number): WebChatDocument => ({
  id,
  user_id: 1,
  session_id: 9,
  original_name: `${id}.txt`,
  content_type: 'text/plain',
  extension: '.txt',
  size_bytes: 10,
  sha256: String(id),
  status: 'ready',
  enabled: true,
  extracted_chars: 10,
  chunk_count: 1,
  attempt_count: 1,
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
})

describe('useWebChatDocuments attachment lifecycle', () => {
  beforeEach(() => vi.clearAllMocks())

  it('deletes a temporary object when the user removes it', async () => {
    const state = useWebChatDocuments(async () => null, key => key)
    state.pendingDocuments.value.push(documentFixture(7))
    await state.removePendingDocument(7)
    expect(state.pendingDocuments.value).toEqual([])
    expect(webChatAPI.deleteDocument).toHaveBeenCalledWith(7)
  })

  it('deletes all unsent objects when a draft/session is discarded', async () => {
    const state = useWebChatDocuments(async () => null, key => key)
    state.pendingDocuments.value.push(documentFixture(7), documentFixture(8))
    state.failedAttachments.value.push({ key: 'failed', file: new File(['x'], 'x.txt'), error: 'timeout', documentId: 9 })
    await state.discardPendingDocuments()
    expect(state.pendingDocuments.value).toEqual([])
    expect(state.failedAttachments.value).toEqual([])
    expect(webChatAPI.deleteDocument).toHaveBeenCalledTimes(3)
  })

  it('keeps accepted attachments for message history when clearing the composer', () => {
    const state = useWebChatDocuments(async () => null, key => key)
    state.pendingDocuments.value.push(documentFixture(7))
    state.clearPendingDocuments()
    expect(state.pendingDocuments.value).toEqual([])
    expect(webChatAPI.deleteDocument).not.toHaveBeenCalled()
  })
})
