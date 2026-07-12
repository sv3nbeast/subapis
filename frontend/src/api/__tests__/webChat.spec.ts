import { afterEach, describe, expect, it, vi } from 'vitest'
import { streamMessage, WebChatStreamError } from '../webChat'

afterEach(() => vi.unstubAllGlobals())

function sseResponse(parts: string[]): Response {
  const encoder = new TextEncoder()
  const body = new ReadableStream({
    start(controller) {
      parts.forEach(part => controller.enqueue(encoder.encode(part)))
      controller.close()
    },
  })
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

describe('web chat stream client', () => {
  it('dispatches deltas and the canonical done message', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sseResponse([
      'event: delta\ndata: {"text":"hello"}\n\n',
      'event: done\r\ndata: {"message_id":9,"request_id":"req-1","message":{"id":9,"content":"hello"}}\r\n\r\n',
    ])))
    let text = ''
    let requestID = ''
    await streamMessage(1, { content: 'hi' }, {
      onDelta(delta) { text += delta },
      onDone(result) { requestID = result.request_id || '' },
    })
    expect(text).toBe('hello')
    expect(requestID).toBe('req-1')
  })

  it('surfaces persisted SSE errors', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sseResponse([
      'event: error\ndata: {"message":"upstream failed","persisted":{"id":9,"status":"partial"}}\n\n',
    ])))
    let status = ''
    await expect(streamMessage(1, { content: 'hi' }, {
      onError(_message, persisted) { status = persisted?.status || '' },
    })).rejects.toBeInstanceOf(WebChatStreamError)
    expect(status).toBe('partial')
  })

  it('passes AbortController signals to fetch', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new DOMException('aborted', 'AbortError'))
    vi.stubGlobal('fetch', fetchMock)
    const controller = new AbortController()
    controller.abort()
    await expect(streamMessage(1, { content: 'hi' }, { signal: controller.signal })).rejects.toMatchObject({ name: 'AbortError' })
    expect(fetchMock.mock.calls[0][1].signal).toBe(controller.signal)
  })
})
