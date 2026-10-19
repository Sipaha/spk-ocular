import { describe, expect, it, vi } from 'vitest'
import { compileFilter, serverLogFilter, compileRegex, plainRanges, regexRanges } from './match'
import { RegexSearch, workerCore, type FromWorker, type ToWorker, type WorkerLike } from './regexSearch'

describe('plain search and filter', () => {
  it('finds case-insensitive ranges', () => {
    expect(plainRanges('Error: ERROR error', 'error')).toEqual([[0, 5], [7, 12], [13, 18]])
    expect(plainRanges('abc', '')).toEqual([])
  })

  it('filters with * wildcards in linear time', () => {
    const f = compileFilter('get*/api*200')!
    expect(f('GET /api/v1 → 200')).toBe(true)
    expect(f('GET /web → 200')).toBe(false)
    expect(compileFilter('a')).toBeNull()
    expect(compileFilter('**')).toBeNull()
    // a pathological input stays fast (no backtracking)
    const g = compileFilter('a*a*a*a*a*a*a*a*b')!
    const t0 = performance.now()
    expect(g('a'.repeat(100_000))).toBe(false)
    expect(performance.now() - t0).toBeLessThan(200)
  })

  it('compiles regexes or explains', () => {
    expect('error' in compileRegex('(')).toBe(true)
    const r = compileRegex('t.ck')
    expect('re' in r && regexRanges(r.re, 'tick tock')).toEqual([[0, 4], [5, 9]])
    expect('re' in compileRegex('x*') && regexRanges((compileRegex('x*') as { re: RegExp }).re, 'axx')).toEqual([[1, 3]])
  })
})

/** A worker that runs workerCore asynchronously, like a real one. */
function fakeWorker(): WorkerLike & { sent: ToWorker[] } {
  const w: WorkerLike & { sent: ToWorker[] } = {
    sent: [],
    onmessage: null,
    postMessage(m) {
      w.sent.push(m)
      queueMicrotask(() => core(m))
    },
    terminate: vi.fn(),
  }
  const core = workerCore((m: FromWorker) => w.onmessage?.({ data: m }))
  return w
}

describe('RegexSearch', () => {
  it('matches existing and new lines, forgets dropped ones', async () => {
    let changes = 0
    const rs = new RegexSearch(fakeWorker, () => changes++)
    rs.setPattern('b+', [{ id: 1, plain: 'abba' }, { id: 2, plain: 'none' }])
    await Promise.resolve()
    await Promise.resolve()
    expect([...rs.hits]).toEqual([[1, [[1, 3]]]])
    rs.add([{ id: 2, plain: 'none' }, { id: 3, plain: 'bb' }])
    await Promise.resolve()
    expect(rs.hits.get(3)).toEqual([[0, 2]])
    expect(rs.state).toBe('ready')
    rs.drop(2)
    expect(rs.hits.has(1)).toBe(false)
    expect(changes).toBeGreaterThan(0)
    rs.stop()
  })

  it('keeps searching the lines of a new stream (ids go on across streams)', async () => {
    const rs = new RegexSearch(fakeWorker, () => {})
    rs.setPattern('ok', [{ id: 1, plain: 'ok' }, { id: 2, plain: 'ok' }])
    rs.dropAll() // the buffer started over
    rs.add([{ id: 3, plain: 'ok again' }])
    for (let i = 0; i < 4; i++) await Promise.resolve()
    expect([...rs.hits.keys()]).toEqual([3])
    rs.stop()
  })

  it('ignores queued replies from a replaced worker and replies below the dropped floor', () => {
    const workers: WorkerLike[] = []
    const rs = new RegexSearch(() => {
      const w: WorkerLike = { onmessage: null, postMessage() {}, terminate: vi.fn() }
      workers.push(w)
      return w
    }, () => {})
    rs.setPattern('old', [{ id: 1, plain: 'old' }])
    rs.setPattern('new', [{ id: 2, plain: 'new' }])
    workers[1].onmessage!({ data: { t: 'hits', seq: 4, reset: false, hits: [[2, [[0, 3]]]] } })
    workers[0].onmessage!({ data: { t: 'hits', seq: 2, reset: true, hits: [[1, [[0, 3]]]] } })
    expect([...rs.hits.keys()]).toEqual([2])
    rs.drop(3)
    workers[1].onmessage!({ data: { t: 'hits', seq: 3, reset: false, hits: [[2, [[0, 3]]]] } })
    expect([...rs.hits]).toEqual([])
    rs.stop()
  })

  it('looks up only the newly added suffix of monotonic retained entries', async () => {
    const w = fakeWorker()
    const rs = new RegexSearch(() => w, () => {})
    const entries = Array.from({ length: 50_000 }, (_, i) => ({ id: i + 1, plain: 'ok' }))
    rs.setPattern('ok', entries)
    await Promise.resolve()
    let reads = 0
    const next = new Proxy([...entries, { id: 50_001, plain: 'ok' }], {
      get(target, key, receiver) {
        if (typeof key === 'string' && /^\d+$/.test(key)) reads++
        return Reflect.get(target, key, receiver)
      },
    })
    rs.add(next)
    await Promise.resolve()
    expect(reads).toBeLessThan(25)
    expect(w.sent.at(-1)).toMatchObject({ t: 'add', items: [[50_001, 'ok']] })
    expect(rs.hits.has(50_001)).toBe(true)
    rs.stop()
  })

  it('kills a worker that does not answer in time', () => {
    vi.useFakeTimers()
    const hung: WorkerLike = { onmessage: null, postMessage() {}, terminate: vi.fn() }
    const rs = new RegexSearch(() => hung, () => {}, 100)
    rs.setPattern('(a|aa)+$', [{ id: 1, plain: 'a'.repeat(40) + 'b' }])
    expect(rs.state).toBe('busy')
    vi.advanceTimersByTime(150)
    expect(rs.state).toBe('slow')
    expect(hung.terminate).toHaveBeenCalled()
    vi.useRealTimers()
  })
})

it('bounds the server predicate before deciding whether it is active', () => {
 expect(serverLogFilter('*'.repeat(500) + 'ERROR')).toBeUndefined()
 expect(serverLogFilter('x')).toBeUndefined()
 expect(serverLogFilter('ERROR')).toBe('ERROR')
 expect(serverLogFilter('ERROR'.repeat(200))).toHaveLength(500)
})
