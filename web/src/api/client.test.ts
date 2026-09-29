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
    expect(parseWailsError(new Error('internal')).code).toBe('internal')
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
})
