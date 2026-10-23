import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { streamTokenInjectionTest, type TokenInjectionEvent } from '@/api/admin/accountTokenInjection'

vi.mock('@/api/client', () => ({ buildApiUrl: (path: string) => '/api/v1' + path }))

const separator = '\r\n\r\n'
const sse = (value: unknown) => 'data: ' + JSON.stringify(value) + separator

function mockStream(body: string, split = 7) {
  const bytes = new TextEncoder().encode(body)
  let offset = 0
  const reader = {
    read: vi.fn(async () => {
      if (offset >= bytes.length) return { done: true, value: undefined }
      const value = bytes.slice(offset, offset + split)
      offset += split
      return { done: false, value }
    }),
    cancel: vi.fn().mockResolvedValue(undefined),
    releaseLock: vi.fn()
  }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, body: { getReader: () => reader } }))
  return reader
}

describe('token injection diagnostic stream', () => {
  beforeEach(() => localStorage.setItem('auth_token', 'admin-test-token'))
  afterEach(() => { vi.unstubAllGlobals(); localStorage.clear() })

  it('pins the selected account/model and decodes split UTF-8 SSE events', async () => {
    const events = [
      { type: 'injection_start', data: { requested_model: '模型别名' } },
      { type: 'injection_sample', data: { index: 1, input_tokens: 22438, cache_read_tokens: 20000 } },
      { type: 'injection_complete', data: { status: 'stable', estimate_tokens: 22422 } }
    ]
    const reader = mockStream(events.map(sse).join(''), 1)
    const received: TokenInjectionEvent[] = []
    const signal = new AbortController().signal
    await streamTokenInjectionTest(377, ' 模型别名 ', 'responses', event => received.push(event), signal)
    expect(received).toEqual(events)
    expect(fetch).toHaveBeenCalledOnce()
    expect(fetch).toHaveBeenCalledWith('/api/v1/admin/accounts/377/token-injection-test', expect.objectContaining({
      method: 'POST', signal,
      body: JSON.stringify({ model_id: '模型别名', protocol: 'responses' }),
      headers: expect.objectContaining({ Authorization: 'Bearer admin-test-token' })
    }))
    expect(reader.cancel).toHaveBeenCalledOnce()
    expect(reader.releaseLock).toHaveBeenCalledOnce()
  })

  it('accepts multiline event data and a final event without a delimiter', async () => {
    mockStream('data: {"type":"injection_complete",\r\ndata: "data":{"status":"inconclusive"}}')
    const callback = vi.fn()
    await streamTokenInjectionTest(1, 'gpt-test', 'auto', callback, new AbortController().signal)
    expect(callback).toHaveBeenCalledWith({ type: 'injection_complete', data: { status: 'inconclusive' } })
  })

  it.each([
    [sse({ type: 'error', error: 'Upstream returned HTTP 429; test stopped' }), 'HTTP 429'],
    [sse({ type: 'injection_progress', data: { index: 1, total: 5 } }), 'before completion'],
    ['data: invalid-json' + separator, 'JSON']
  ])('rejects failure/truncation without retrying', async (body, error) => {
    const reader = mockStream(body)
    await expect(streamTokenInjectionTest(1, 'gpt-test', 'auto', vi.fn(), new AbortController().signal)).rejects.toThrow(error)
    expect(fetch).toHaveBeenCalledOnce()
    expect(reader.cancel).toHaveBeenCalledOnce()
    expect(reader.releaseLock).toHaveBeenCalledOnce()
  })

  it('rejects HTTP failures without opening or retrying a stream', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 403 }))
    await expect(streamTokenInjectionTest(1, 'gpt-test', 'auto', vi.fn(), new AbortController().signal)).rejects.toThrow('HTTP 403')
    expect(fetch).toHaveBeenCalledOnce()
  })

  it('propagates cancellation during a read and releases the reader', async () => {
    const controller = new AbortController()
    const reader = mockStream('')
    reader.read.mockImplementation(() => new Promise((_resolve, reject) => {
      controller.signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
    }))
    const pending = streamTokenInjectionTest(1, 'gpt-test', 'auto', vi.fn(), controller.signal)
    await Promise.resolve()
    controller.abort()
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    expect(fetch).toHaveBeenCalledOnce()
    expect(reader.cancel).toHaveBeenCalledOnce()
  })
})
