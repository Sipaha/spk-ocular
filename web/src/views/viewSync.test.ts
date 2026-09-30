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
    appInfo: vi.fn(), listTargets: vi.fn(), selectTarget: vi.fn(), listKinds: vi.fn(), refreshKinds: vi.fn(), listScopes: vi.fn(),
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

  // Review 2026-09-29: gone arriving while a pull is in flight used to leave
  // pulling=true forever (the stale pull's finally skipped the reset).
  for (const outcome of ['success', 'failure'] as const) {
    it(`gone during an in-flight pull (${outcome}) does not freeze the reopened view`, async () => {
      let n = 0
      const openView = vi.fn(async () => ({ viewId: `v${++n}`, kind: { id: 'pods', title: 'Pods', group: 'W', columns: [], scoped: true } }))
      const slow = deferred<Page>()
      const getRows = vi.fn(async (id: string, since: number) => {
        if (id === 'v1' && since > 0) return slow.p
        return page({ viewId: id, reset: since === 0, version: since + 1, upserts: [row(`${id}-${since}`)] })
      })
      const s = new ViewSync(client({ openView, getRows }), 'kubernetes', 't', q)
      await s.open()
      await flush()
      s.onChanged({ version: 5 }) // v1 pull in flight
      s.onChanged({ gone: true }) // v1 closed meanwhile → reopen as v2
      await flush()
      expect(s.id).toBe('v2')
      expect(s.snapshot().viewId).toBe('v2')
      if (outcome === 'success') slow.resolve(page({ viewId: 'v1', version: 6 }))
      else slow.p.catch(() => {}) // never settles: the old pull stays hung forever
      await flush()
      s.onChanged({ version: 3 })
      await flush()
      await flush()
      expect(getRows).toHaveBeenLastCalledWith('v2', expect.any(Number))
      expect(s.snapshot().rows.some((r) => r.id.startsWith('v2'))).toBe(true)
      expect(s.snapshot().rows.some((r) => r.id.startsWith('v1'))).toBe(false)
    })
  }

  it('gone while already reopening ends on the newest view', async () => {
    let n = 0
    const first = deferred<{ viewId: string; kind: never }>()
    const openView = vi.fn(async () => {
      n++
      if (n === 2) return first.p
      return { viewId: `v${n}`, kind: { id: 'pods', title: 'Pods', group: 'W', columns: [], scoped: true } as never }
    })
    const s = new ViewSync(client({ openView: openView as unknown as Client['openView'] }), 'kubernetes', 't', q)
    await s.open()
    const c = (s as unknown as { client: Client }).client
    s.onChanged({ gone: true }) // reopen #2 hangs
    const again = s.open() // another reopen (#3) supersedes it
    await again
    first.resolve({ viewId: 'v2', kind: undefined as never })
    await flush()
    expect(s.id).toBe('v3')
    expect(c.closeView).toHaveBeenCalledWith('v2') // the late one is closed, not adopted
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

const widgetsKind = (cols: string[]) => ({ id: 'ocular.dev/widgets', title: 'Widgets', group: 'API groups', scoped: true, columns: cols.map((id) => ({ id, title: id, type: 'text' as const })) })

describe('ViewSync: the kind ends', () => {
  it('a view ending "schema changed" opens again with the new columns; its old rows stay until then', async () => {
    const openView = vi.fn()
      .mockResolvedValueOnce({ viewId: 'v1', kind: widgetsKind(['name', 'size']) })
      .mockResolvedValueOnce({ viewId: 'v2', kind: widgetsKind(['name', 'phase']) })
    const getRows = vi.fn(async (id: string, since: number) => {
      if (id === 'v1' && since === 0) return page({ viewId: 'v1', reset: true, version: 1, upserts: [row('a')] })
      if (id === 'v1') return page({ viewId: 'v1', version: 2, upserts: [row('a', 'late')], status: { state: 'error', class: 'schema_changed', message: 'Widgets: the columns changed; opening again' } })
      return page({ viewId: 'v2', reset: true, version: 1, upserts: [row('a', 'new')] })
    })
    const s = new ViewSync(client({ openView, getRows }), 'kubernetes', 't', q)
    await s.open()
    await flush()
    s.onChanged({ version: 2 })
    await flush()
    await flush()
    expect(openView).toHaveBeenCalledTimes(2)
    await flush()
    expect(s.snapshot().kind?.columns.map((c) => c.id)).toEqual(['name', 'phase'])
    expect(s.snapshot().rows[0].cells[0].text).toBe('new')
    expect(s.snapshot().status.state).toBe('ready')
  })

  it('"schema changed" again and again is bounded: then an error, no loop', async () => {
    vi.useFakeTimers()
    let n = 0
    const openView = vi.fn(async () => ({ viewId: `v${++n}`, kind: widgetsKind(['name']) }))
    const getRows = vi.fn(async (id: string) => page({ viewId: id, reset: true, version: 1, status: { state: 'error', class: 'schema_changed', message: 'changed' } }))
    const s = new ViewSync(client({ openView, getRows }), 'kubernetes', 't', q)
    void s.open()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(openView.mock.calls.length).toBeLessThanOrEqual(7)
    expect(s.snapshot().status).toMatchObject({ state: 'error', class: 'schema_changed' })
    const calls = openView.mock.calls.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(openView).toHaveBeenCalledTimes(calls)
  })

  it('a view ending "removed" is final: rows go, nothing reopens or retries', async () => {
    vi.useFakeTimers()
    const openView = vi.fn(async () => ({ viewId: 'v1', kind: widgetsKind(['name']) }))
    const getRows = vi.fn()
      .mockResolvedValueOnce(page({ reset: true, version: 1, upserts: [row('a')] }))
      .mockResolvedValue(page({ version: 2, status: { state: 'error', class: 'removed', message: 'Widgets are no longer served by the API' } }))
    const s = new ViewSync(client({ openView, getRows }), 'kubernetes', 't', q)
    await s.open()
    await vi.advanceTimersByTimeAsync(0)
    s.onChanged({ version: 2 })
    await vi.advanceTimersByTimeAsync(0)
    expect(s.snapshot().rows).toEqual([])
    expect(s.snapshot().status).toMatchObject({ state: 'error', class: 'removed' })
    s.onChanged({ gone: true })
    s.onChanged({ version: 3 })
    s.resync()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(openView).toHaveBeenCalledTimes(1)
    expect(getRows).toHaveBeenCalledTimes(2)
  })

  it('OpenView "removed" is final; OpenView "schema changed" opens again', async () => {
    vi.useFakeTimers()
    const removed = vi.fn(async () => {
      throw new ApiError('removed', 'ocular.dev/widgets is no longer served')
    })
    const r = new ViewSync(client({ openView: removed }), 'kubernetes', 't', q)
    await r.open()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(removed).toHaveBeenCalledTimes(1)
    expect(r.snapshot().status).toMatchObject({ state: 'error', class: 'removed' })

    const changed = vi.fn()
      .mockRejectedValueOnce(new ApiError('schema_changed', 'Widgets: the API resource changed; opening again'))
      .mockResolvedValue({ viewId: 'v2', kind: widgetsKind(['name']) })
    const s = new ViewSync(client({ openView: changed }), 'kubernetes', 't', q)
    await s.open()
    await vi.advanceTimersByTimeAsync(1_000)
    expect(changed).toHaveBeenCalledTimes(2)
    expect(s.id).toBe('v2')
    expect(s.snapshot().openError).toBeNull()
  })

  it('a pull answered "removed" is final too (no retries)', async () => {
    vi.useFakeTimers()
    const getRows = vi.fn()
      .mockResolvedValueOnce(page({ reset: true, version: 1, upserts: [row('a')] }))
      .mockRejectedValue(new ApiError('removed', 'no longer served'))
    const s = new ViewSync(client({ getRows }), 'kubernetes', 't', q)
    await s.open()
    await vi.advanceTimersByTimeAsync(0)
    s.onChanged({ version: 2 })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(getRows).toHaveBeenCalledTimes(2)
    expect(s.snapshot().rows).toEqual([])
    expect(s.snapshot().status).toMatchObject({ state: 'error', class: 'removed' })
  })
})

describe('ViewSync: the kind ends (review)', () => {
  it('loading pages do not reset the reopen budget: schema_changed before ready stays bounded', async () => {
    vi.useFakeTimers()
    let n = 0
    const openView = vi.fn(async () => ({ viewId: `v${++n}`, kind: widgetsKind(['name']) }))
    const getRows = vi.fn(async (id: string, since: number) =>
      since === 0
        ? page({ viewId: id, reset: true, version: 1, status: { state: 'loading' } })
        : page({ viewId: id, version: 2, status: { state: 'error', class: 'schema_changed', message: 'changed' } }))
    const s = new ViewSync(client({ openView, getRows }), 'kubernetes', 't', q)
    await s.open()
    for (let i = 0; i < 12; i++) {
      await vi.advanceTimersByTimeAsync(0)
      s.onChanged({ version: 2 })
      await vi.advanceTimersByTimeAsync(10_000)
    }
    expect(openView.mock.calls.length).toBeLessThanOrEqual(6)
    expect(s.snapshot().status).toMatchObject({ state: 'error', class: 'schema_changed' })
  })

  it('the new columns come with the new rows, never over the old ones', async () => {
    const second = deferred<Page>()
    const openView = vi.fn()
      .mockResolvedValueOnce({ viewId: 'v1', kind: widgetsKind(['name', 'size']) })
      .mockResolvedValueOnce({ viewId: 'v2', kind: widgetsKind(['name', 'phase']) })
    const getRows = vi.fn(async (id: string, since: number) => {
      if (id === 'v1' && since === 0) return page({ viewId: 'v1', reset: true, version: 1, upserts: [row('a', '42')] })
      if (id === 'v1') return page({ viewId: 'v1', version: 2, status: { state: 'error', class: 'schema_changed' } })
      return second.p
    })
    const s = new ViewSync(client({ openView, getRows }), 'kubernetes', 't', q)
    await s.open()
    await flush()
    s.onChanged({ version: 2 })
    await flush()
    await flush()
    await flush()
    expect(openView).toHaveBeenCalledTimes(2)
    expect(s.snapshot().kind?.columns.map((c) => c.id)).toEqual(['name', 'size']) // still the old rows' columns
    second.resolve(page({ viewId: 'v2', reset: true, version: 1, upserts: [row('a', 'Running')] }))
    await flush()
    await flush()
    expect(s.snapshot().kind?.columns.map((c) => c.id)).toEqual(['name', 'phase'])
    expect(s.snapshot().rows[0].cells[0].text).toBe('Running')
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

describe('ViewHub catalog events', () => {
  it('tells its catalog listeners of kinds_changed and of resync', () => {
    const hub = new ViewHub(client())
    const seen: unknown[] = []
    const off = hub.subscribeKinds((p) => seen.push(p ?? 'resync'))
    hub.onKindsChanged({ provider: 'kubernetes', target: 't', session: 1, rev: 3 })
    hub.resyncAll()
    off()
    hub.onKindsChanged({ provider: 'kubernetes', target: 't', session: 1, rev: 4 })
    expect(seen).toEqual([{ provider: 'kubernetes', target: 't', session: 1, rev: 3 }, 'resync'])
  })
})
