import { Call } from '@wailsio/runtime'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, httpClient, isDesktop, parseWailsError, wailsClient } from './client'

vi.mock('@wailsio/runtime', () => ({ Call: { ByName: vi.fn() }, Events: { On: vi.fn() } }))

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
  it('takes the reason from the cause the runtime attaches (the CodedError JSON)', () => {
    const why = { key: 'kubernetes.error.changed', params: { kind: 'deployment', name: 'web' }, text: 'deployment web changed' }
    const e = parseWailsError(Object.assign(new Error('conflict: deployment web changed'), { cause: { code: 'conflict', detail: 'deployment web changed', why } }))
    expect(e.code).toBe('conflict')
    expect(e.detail).toBe('deployment web changed')
    expect(e.transport).toBe(false)
    expect(e.why).toEqual(why)
  })
  it('a text-only reason is a reason; a malformed one is ignored', () => {
    const ok = parseWailsError(Object.assign(new Error('conflict: x'), { cause: { code: 'conflict', why: { text: 'x' } } }))
    expect(ok.why).toEqual({ text: 'x' })
    for (const why of [{ key: 'k' }, { text: 1 }, { text: 'x', key: 2 }, { text: 'x', params: { a: 1 } }, { text: 'x', params: 'p' }, 'why', null]) {
      const e = parseWailsError(Object.assign(new Error('conflict: x'), { cause: { code: 'conflict', why } }))
      expect(e.why).toBeUndefined()
      expect(e.detail).toBe('x')
    }
    expect(parseWailsError(Object.assign(new Error('conflict: x'), { cause: 'text' })).why).toBeUndefined()
  })
  it('a transport failure stays one whatever its cause says', () => {
    const e = parseWailsError(Object.assign(new Error('Call to method failed'), { cause: { why: { text: 'x' } } }))
    expect(e.transport).toBe(true)
    expect(e.why).toBeUndefined()
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

  it('closeTarget posts the target', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    await httpClient.closeTarget('kubernetes', 'x')
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/CloseTarget')
    expect(JSON.parse(init.body as string)).toEqual({ provider: 'kubernetes', id: 'x' })
  })

  it('an error answer carries its reason', async () => {
    const why = { key: 'api.configChanged', params: { target: 'prod' }, text: 'the configuration of prod changed' }
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'conflict', detail: why.text, why }), { status: 400, headers: { 'content-type': 'application/json' } })))
    const err = await httpClient.selectTarget('kubernetes', 'x').catch((e) => e)
    expect(err.code).toBe('conflict')
    expect(err.why).toEqual(why)
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'conflict', detail: 'd', why: { key: 'k' } }), { status: 400, headers: { 'content-type': 'application/json' } })))
    const bad = await httpClient.selectTarget('kubernetes', 'x').catch((e) => e)
    expect(bad.why).toBeUndefined()
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

describe('getMetrics', () => {
  it('numbers the requests; the HTTP one sends its seq', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ status: 'ok', values: {} }), { headers: { 'content-type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await httpClient.getMetrics('v1', ['a'])
    await httpClient.getMetrics('v1', ['b'])
    const bodies = fetchMock.mock.calls.map((c) => JSON.parse((c as unknown as [string, RequestInit])[1].body as string))
    expect(bodies[0]).toMatchObject({ viewId: 'v1', rowIds: ['a'] })
    expect(bodies[1].seq).toBeGreaterThan(bodies[0].seq)
  })

  it('a desktop abort cancels the call and tells Go which seq was given up', async () => {
    const cancel = vi.fn()
    vi.mocked(Call.ByName).mockImplementation(((method: string) => {
      if (method.endsWith('.GetMetrics')) {
        const p = new Promise(() => {}) as Promise<unknown> & { cancel: () => void }
        p.cancel = cancel
        return p
      }
      return Promise.resolve(undefined)
    }) as unknown as typeof Call.ByName)
    const ac = new AbortController()
    const got = wailsClient.getMetrics('v1', ['a'], ac.signal).catch((e) => e)
    const [, req] = vi.mocked(Call.ByName).mock.calls[0] as unknown as [string, { viewId: string; rowIds: string[]; seq: number }]
    expect(req).toMatchObject({ viewId: 'v1', rowIds: ['a'] })
    ac.abort()
    expect((await got).name).toBe('AbortError')
    expect(cancel).toHaveBeenCalled()
    expect(vi.mocked(Call.ByName)).toHaveBeenCalledWith(expect.stringMatching(/\.CancelMetrics$/), 'v1', req.seq)
  })
})

describe('values', () => {
  const ref = { provider: 'kubernetes', target: 'ctx', scope: 'ns', kind: 'secrets', name: 'db', uid: 'u1' }
  const run = { ref, base: 'b', key: 'password', op: 'set' as const, value: 'x', encoding: 'text' as const, token: 't' }

  it('the HTTP client sends one key of one object and the run flat with its token', async () => {
    document.head.innerHTML = '<meta name="spk-ocular-api-token" content="tok">'
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200, headers: { 'content-type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await httpClient.revealValue(ref, 'password')
    await httpClient.runValueEdit(run)
    const sent = (fetchMock.mock.calls as unknown as [string, RequestInit][]).map(([url, init]) => [url, JSON.parse(init.body as string)])
    expect(sent).toEqual([
      ['/api/RevealValue', { ref, key: 'password' }],
      ['/api/RunValueEdit', run],
    ])
  })

  it('the Wails client calls the same methods with the same shapes', async () => {
    const byName = vi.mocked(Call.ByName)
    byName.mockReset()
    byName.mockResolvedValue({})
    await wailsClient.getValues(ref)
    await wailsClient.revealValue(ref, 'password')
    await wailsClient.prepareValueEdit(run)
    await wailsClient.runValueEdit(run)
    expect(byName.mock.calls.map((c) => [String(c[0]).split('.').pop(), c[1]])).toEqual([
      ['GetValues', ref],
      ['RevealValue', { ref, key: 'password' }],
      ['PrepareValueEdit', run],
      ['RunValueEdit', run],
    ])
  })
})
