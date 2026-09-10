import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { refreshAuthTokens } = vi.hoisted(() => ({ refreshAuthTokens: vi.fn() }))

vi.mock('@/api/tokenRefresh', () => ({ refreshAuthTokens }))
vi.mock('@/i18n', () => ({ getLocale: () => 'zh' }))

import {
  buildUsageExportQuery,
  defaultUsageExportFilename,
  exportCsv,
  isUsageExportAborted,
  parseAttachmentFilename
} from '@/api/usage'

const encoder = new TextEncoder()

function streamResponse(
  chunks: string[],
  init: { status?: number; headers?: Record<string, string> } = {}
): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk))
      controller.close()
    }
  })
  return new Response(stream, {
    status: init.status ?? 200,
    headers: { 'Content-Type': 'text/csv; charset=utf-8', ...init.headers }
  })
}

function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers }
  })
}

function blobText(blob: Blob): Promise<string> {
  if (typeof blob.text === 'function') return blob.text()
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsText(blob)
  })
}

describe('usageAPI.exportCsv', () => {
  beforeEach(() => {
    localStorage.clear()
    refreshAuthTokens.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('streams the CSV in one request with list-equivalent filters and panel auth headers', async () => {
    localStorage.setItem('auth_token', 'jwt-1')
    const fetchMock = vi.fn().mockResolvedValue(
      streamResponse(
        ['﻿Time,Model\n', '2026-03-08 00:00:00,gpt-5.4\n', '2026-03-08 00:00:01,gpt-5.4\n'],
        { headers: { 'Content-Disposition': 'attachment; filename=usage_2026-03-01_to_2026-03-08.csv' } }
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    const progress: number[] = []
    const result = await exportCsv(
      {
        start_date: '2026-03-01',
        end_date: '2026-03-08',
        model: 'gpt-5.4',
        stream: false,
        native_compaction_v2: null,
        billing_type: null,
        sort_by: 'created_at',
        sort_order: 'desc',
        timezone: 'Asia/Shanghai'
      },
      { onProgress: (p) => progress.push(p.receivedRows) }
    )

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    const parsed = new URL(url, 'http://localhost')
    expect(parsed.pathname).toBe('/api/v1/usage/export')
    expect(parsed.searchParams.get('start_date')).toBe('2026-03-01')
    expect(parsed.searchParams.get('end_date')).toBe('2026-03-08')
    expect(parsed.searchParams.get('model')).toBe('gpt-5.4')
    expect(parsed.searchParams.get('stream')).toBe('false')
    expect(parsed.searchParams.get('sort_order')).toBe('desc')
    expect(parsed.searchParams.get('timezone')).toBe('Asia/Shanghai')
    expect(parsed.searchParams.has('native_compaction_v2')).toBe(false)
    expect(parsed.searchParams.has('billing_type')).toBe(false)
    expect(parsed.searchParams.has('page')).toBe(false)
    expect(parsed.searchParams.has('page_size')).toBe(false)
    expect(init.headers).toMatchObject({
      Authorization: 'Bearer jwt-1',
      'Accept-Language': 'zh',
      Accept: 'text/csv',
      'X-User-UI-Request': '1'
    })
    expect(init.credentials).toBe('include')

    expect(result.filename).toBe('usage_2026-03-01_to_2026-03-08.csv')
    expect(result.rows).toBe(2)
    expect(progress).toEqual([0, 1, 2])
    const text = await blobText(result.blob)
    expect(text).toContain('Time,Model')
    expect(text).toContain('2026-03-08 00:00:01,gpt-5.4')
  })

  it('adds the browser timezone when the caller omits it', async () => {
    const fetchMock = vi.fn().mockResolvedValue(streamResponse(['Time\n']))
    vi.stubGlobal('fetch', fetchMock)

    const result = await exportCsv({ start_date: '2026-03-01', end_date: '2026-03-08' })

    const parsed = new URL(String(fetchMock.mock.calls[0][0]), 'http://localhost')
    expect(parsed.searchParams.get('timezone')).toBeTruthy()
    expect(result.rows).toBe(0)
    expect(result.filename).toBe('usage_2026-03-01_to_2026-03-08.csv')
  })

  it('refreshes the session once on 401 and retries with the rotated token', async () => {
    localStorage.setItem('auth_token', 'expired')
    localStorage.setItem('refresh_token', 'refresh-1')
    refreshAuthTokens.mockResolvedValue({
      access_token: 'fresh',
      refresh_token: 'refresh-2',
      expires_in: 3600,
      token_type: 'Bearer'
    })
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(401, { code: 'TOKEN_EXPIRED', message: 'expired' }))
      .mockResolvedValueOnce(streamResponse(['Time\n', 'row\n']))
    vi.stubGlobal('fetch', fetchMock)

    const result = await exportCsv({})

    expect(refreshAuthTokens).toHaveBeenCalledWith({ failedAccessToken: 'expired' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const retryInit = fetchMock.mock.calls[1][1] as RequestInit
    expect(retryInit.headers).toMatchObject({ Authorization: 'Bearer fresh' })
    expect(result.rows).toBe(1)
  })

  it('fails with a session error when the refresh itself fails', async () => {
    localStorage.setItem('auth_token', 'expired')
    localStorage.setItem('refresh_token', 'refresh-1')
    refreshAuthTokens.mockRejectedValue(new Error('refresh failed'))
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(401, { code: 'TOKEN_EXPIRED', message: 'expired' })))

    await expect(exportCsv({})).rejects.toMatchObject({ status: 401, code: 'TOKEN_REFRESH_FAILED' })
  })

  it('surfaces the server error envelope for rejected exports', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(400, {
          code: 400,
          message: 'Export exceeds the maximum row limit; narrow the date range or filters',
          reason: 'USAGE_EXPORT_TOO_LARGE',
          metadata: { limit: '500000' }
        })
      )
    )

    await expect(exportCsv({})).rejects.toMatchObject({
      status: 400,
      reason: 'USAGE_EXPORT_TOO_LARGE',
      message: 'Export exceeds the maximum row limit; narrow the date range or filters',
      metadata: { limit: '500000' }
    })
  })

  it('exposes Retry-After for rate limited exports', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(429, { code: 'RATE_LIMITED', message: 'Too many requests' }, { 'Retry-After': '5' })
      )
    )

    await expect(exportCsv({})).rejects.toMatchObject({
      status: 429,
      code: 'RATE_LIMITED',
      retryAfterSeconds: 5
    })
  })

  it('keeps the HTTP status when the error body is not JSON', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>bad gateway</html>', { status: 502, statusText: 'Bad Gateway' })))

    await expect(exportCsv({})).rejects.toMatchObject({ status: 502, message: 'Bad Gateway' })
  })

  it('propagates cancellation so callers can tell it apart from failures', async () => {
    const controller = new AbortController()
    const fetchMock = vi.fn().mockImplementation(
      (_url: string, init: RequestInit) =>
        new Promise((_resolve, reject) => {
          init.signal?.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')))
        })
    )
    vi.stubGlobal('fetch', fetchMock)

    const pending = exportCsv({}, { signal: controller.signal })
    controller.abort()
    const error = await pending.catch((e: unknown) => e)

    expect(isUsageExportAborted(error)).toBe(true)
    expect(isUsageExportAborted({ status: 400, message: 'nope' })).toBe(false)
  })

  it('falls back to a buffered body when streaming is unavailable', async () => {
    const payload = encoder.encode('Time\na\nb\n')
    const response = {
      ok: true,
      status: 200,
      headers: new Headers(),
      body: null,
      blob: async () => ({ arrayBuffer: async () => payload.buffer }) as unknown as Blob
    } as unknown as Response
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))

    const result = await exportCsv({})

    expect(result.rows).toBe(2)
    expect(result.filename).toBe('usage_export.csv')
  })
})

describe('usage export helpers', () => {
  it('parses RFC 6266 attachment filenames', () => {
    expect(parseAttachmentFilename('attachment; filename=usage_all.csv')).toBe('usage_all.csv')
    expect(parseAttachmentFilename('attachment; filename="usage all.csv"')).toBe('usage all.csv')
    expect(parseAttachmentFilename("attachment; filename=\"x.csv\"; filename*=UTF-8''%E4%BD%BF%E7%94%A8.csv")).toBe('使用.csv')
    expect(parseAttachmentFilename('inline')).toBeNull()
    expect(parseAttachmentFilename(null)).toBeNull()
  })

  it('names the file from the date range when the server omits it', () => {
    expect(defaultUsageExportFilename({ start_date: '2026-03-01', end_date: '2026-03-08' })).toBe('usage_2026-03-01_to_2026-03-08.csv')
    expect(defaultUsageExportFilename({})).toBe('usage_export.csv')
  })

  it('serializes only meaningful filters and always includes a timezone', () => {
    const query = buildUsageExportQuery({
      model: '',
      api_key_id: undefined,
      group_id: 3,
      stream: false,
      native_compaction_v2: null,
      request_type: 'stream'
    })

    expect(query.get('group_id')).toBe('3')
    expect(query.get('stream')).toBe('false')
    expect(query.get('request_type')).toBe('stream')
    expect(query.has('model')).toBe(false)
    expect(query.has('api_key_id')).toBe(false)
    expect(query.has('native_compaction_v2')).toBe(false)
    expect(query.has('timezone')).toBe(true)
  })
})
