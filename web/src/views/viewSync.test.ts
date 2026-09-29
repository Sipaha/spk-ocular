import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, type Client } from '../api/client'
import type { Page, Row } from '../api/types'
import { ViewHub, ViewSync } from './viewSync'

const row = (id: string, text = id): Row => ({
  id,
  ref: { provider: 'kubernetes', target: 't', kind: 'pods', name: id },
  cells: [{ text }],
  health: { state: 'ok' },
})

const page = (p: Partial<Page>): Page => ({
  viewId: 'v1', version: 1, reset: false, upserts: [], deleted: [], status: { state: 'ready' }, ...p,
})

function client(over: Partial<Client> = {}): Client {
  return {
    appInfo: vi.fn(), listTargets: vi.fn(), selectTarget: vi.fn(), listKinds: vi.fn(), listScopes: vi.fn(),
    openView: vi.fn(async () => ({ viewId: 'v1', kind: { id: 'pods', title: 'Pods', group: 'W', columns: [], scoped: true } })),
    getRows: vi.fn(async () => page({ reset: true, version: 1 })),
    closeView: vi.fn(async () => {}),
    touchViews: vi.fn(async () => []),
    subscribeEvents: vi.fn(() => () => {}),
    ...over,
  } as Client
}

const q = { kind: 'pods', scope: { mode: 'all' as const } }
const flush = () => new Promise((r) => setTimeout(r, 0))

function deferred<T>() {
  let resolve!: (v: T) => void
  const p = new Promise<T>((r) => (resolve = r))
  return { p, resolve }
}

afterEach(() => vi.useRealTimers())

describe('ViewSync', () => {
  it('opens, pulls a snapshot, then applies increments with the page cursor', async () => {
    const getRows = vi.fn()
      .mockResolvedValueOnce(page({ reset: true, version: 3, upserts: [row('a'), row('b')] }))
      .mockResolvedValueOnce(page({ version: 5, upserts: [row('c')], deleted: ['a'] }))
    const s = new ViewSync(client({ getRows }), 'kubernetes', 't', q)
    await s.open()
    await flush()
    expect(s.snapshot().rows.map((r) => r.id)).toEqual(['a', 'b'])
    expect(s.snapshot().kind?.id).toBe('pods')

    s.onChanged({ version: 5 })
    await flush()
    expect(getRows).toHaveBeenLastCalledWith('v1', 3)
    expect(s.snapshot().rows.map((r) => r.id).sort()).toEqual(['b', 'c'])
  })

  it('keeps one pull in flight and re-pulls while behind the announced version', async () => {
    const first = deferred<Page>()
    const getRows = vi.fn()
      .mockReturnValueOnce(first.p)
      .mockResolvedValueOnce(page({ version: 9, upserts: [row('late')] }))
    const s = new ViewSync(client({ getRows }), 'kubernetes', 't', q)
    await s.open()
    s.onChanged({ version: 4 })
    s.onChanged({ version: 9 })
    expect(getRows).toHaveBeenCalledTimes(1)
    first.resolve(page({ reset: true, version: 4, upserts: [row('a')] }))
    await flush()
    await flush()
    expect(getRows).toHaveBeenCalledTimes(2)
    expect(getRows).toHaveBeenLastCalledWith('v1', 4)
    expect(s.snapshot().rows.map((r) => r.id).sort()).toEqual(['a', 'late'])
  })

  it('reopens a gone view and starts from a fresh snapshot', async () => {
    const openView = vi.fn()
      .mockResolvedValueOnce({ viewId: 'v1', kind: { id: 'pods', title: 'Pods', group: 'W', columns: [], scoped: true } })
      .mockResolvedValueOnce({ viewId: 'v2', kind: { id: 'pods', title: 'Pods', group: 'W', columns: [], scoped: true } })
    const getRows = vi.fn(async (id: string, since: number) => {
      if (id === 'v1' && since > 0) throw new ApiError('gone', 'view is gone')
      return page({ viewId: id, reset: true, version: id === 'v1' ? 2 : 7, upserts: [row(id)] })
    })
    const s = new ViewSync(client({ openView, getRows }), 'kubernetes', 't', q)
    await s.open()
    await flush()
    s.onChanged({ version: 5 })
    await flush()
    await flush()
    await flush()
    expect(openView).toHaveBeenCalledTimes(2)
    expect(s.id).toBe('v2')
    expect(getRows).toHaveBeenLastCalledWith('v2', 0)
    expect(s.snapshot().rows.map((r) => r.id)).toEqual(['v2'])
  })

  it('a gone notice reopens even without a pending pull', async () => {
    const openView = vi.fn(async () => ({ viewId: 'vX', kind: { id: 'pods', title: 'Pods', group: 'W', columns: [], scoped: true } }))
    const s = new ViewSync(client({ openView }), 'kubernetes', 't', q)
    await s.open()
    s.onChanged({ gone: true })
    await flush()
    expect(openView).toHaveBeenCalledTimes(2)
  })

  it('retries a failed pull with backoff, bounded', async () => {
    vi.useFakeTimers()
    const getRows = vi.fn(async () => {
      throw new ApiError('internal', 'boom')
    })
    const s = new ViewSync(client({ getRows }), 'kubernetes', 't', q)
    await s.open()
    await vi.runAllTimersAsync()
    expect(getRows).toHaveBeenCalledTimes(6) // first try + 5 retries
    expect(s.snapshot().status).toMatchObject({ state: 'error', class: 'internal' })
  })

  it('ignores responses after dispose and closes a late-opened view', async () => {
    const opened = deferred<{ viewId: string; kind: never }>()
    const c = client({ openView: vi.fn(() => opened.p) as unknown as Client['openView'] })
    const s = new ViewSync(c, 'kubernetes', 't', q)
    const p = s.open()
    s.dispose()
    opened.resolve({ viewId: 'late', kind: undefined as never })
    await p
    expect(c.closeView).toHaveBeenCalledWith('late')
    expect(c.getRows).not.toHaveBeenCalled()
  })

  it('reports an open failure as an error state', async () => {
    const s = new ViewSync(client({ openView: vi.fn(async () => { throw new ApiError('unsupported', 'unknown kind') }) }), 'kubernetes', 't', q)
    await s.open()
    expect(s.snapshot().status).toMatchObject({ state: 'error', class: 'unsupported' })
    expect(s.snapshot().openError).toContain('unknown kind')
  })
})

describe('ViewHub', () => {
  beforeEach(() => vi.useFakeTimers())

  it('routes events, renews leases and reopens views reported gone', async () => {
    let n = 0
    const openView = vi.fn(async () => ({ viewId: `v${++n}`, kind: { id: 'pods', title: 'Pods', group: 'W', columns: [], scoped: true } }))
    const touchViews = vi.fn(async (ids: string[]) => ids.filter((id) => id === 'v1'))
    const c = client({ openView, touchViews })
    const hub = new ViewHub(c)
    const s = hub.create('kubernetes', 't', q)
    await vi.advanceTimersByTimeAsync(0)
    expect(s.id).toBe('v1')
    hub.onViewChanged({ viewId: 'v1', version: 3 })
    await vi.advanceTimersByTimeAsync(0)
    expect(c.getRows).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(20_000)
    expect(touchViews).toHaveBeenCalledWith(['v1'])
    await vi.advanceTimersByTimeAsync(0)
    expect(s.id).toBe('v2')

    hub.release(s)
    expect(c.closeView).toHaveBeenCalledWith('v2')
    await vi.advanceTimersByTimeAsync(40_000)
    expect(touchViews).toHaveBeenCalledTimes(1) // timer stopped with no views
  })
})
