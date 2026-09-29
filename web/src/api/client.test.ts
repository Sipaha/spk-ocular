import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, httpClient, isDesktop, parseWailsError } from './client'

afterEach(() => {
  vi.unstubAllGlobals()
  document.head.innerHTML = ''
})

describe('isDesktop', () => {
  it('detects the Wails schemes', () => {
    expect(isDesktop({ protocol: 'wails:', hostname: 'localhost' })).toBe(true)
    expect(isDesktop({ protocol: 'http:', hostname: 'wails.localhost' })).toBe(true)
    expect(isDesktop({ protocol: 'http:', hostname: '127.0.0.1' })).toBe(false)
  })
})

describe('parseWailsError', () => {
  it('splits "code: detail"', () => {
    const e = parseWailsError(new Error('not_found: no target "x" in Kubernetes'))
    expect(e).toBeInstanceOf(ApiError)
    expect(e.code).toBe('not_found')
    expect(e.detail).toBe('no target "x" in Kubernetes')
  })
  it('keeps a bare code', () => {
    const e = parseWailsError(new Error('internal'))
    expect(e.code).toBe('internal')
    expect(e.transport).toBe(false)
  })
  it('a message that is not a coded error is a transport failure', () => {
    const e = parseWailsError(new Error('Call to method failed: runtime not ready'))
    expect(e.transport).toBe(true)
    expect(e.code).toBe('internal')
    expect(e.detail).toBe('Call to method failed: runtime not ready')
  })
})

describe('httpClient', () => {
  it('sends the page token and maps coded errors', async () => {
    document.head.innerHTML = '<meta name="spk-ocular-api-token" content="tok">'
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ code: 'not_found', detail: 'gone' }), {
        status: 400,
        headers: { 'content-type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)
    const err = await httpClient.selectTarget('kubernetes', 'x').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.code).toBe('not_found')
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/SelectTarget')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer tok')
    expect(JSON.parse(init.body as string)).toEqual({ provider: 'kubernetes', id: 'x' })
  })

  it('a failed fetch is a transport ApiError', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new TypeError('Failed to fetch')
    }))
    const err = await httpClient.appInfo().catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.transport).toBe(true)
    expect(err.detail).toBe('Failed to fetch')
  })

  it('a non-JSON failure answer is a transport ApiError; a coded one is not', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('bad gateway', { status: 502, headers: { 'content-type': 'text/plain' } })))
    const err = await httpClient.appInfo().catch((e) => e)
    expect(err.transport).toBe(true)
    expect(err.detail).toBe('HTTP 502')
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'forbidden', detail: 'no' }), { status: 400, headers: { 'content-type': 'application/json' } })))
    const coded = await httpClient.appInfo().catch((e) => e)
    expect(coded.transport).toBe(false)
  })
})
