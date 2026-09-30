import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Client } from '../api/client'
import type { MetricsView, Row } from '../api/types'
import { METRICS_ABSENT_RETRY_MS, METRICS_INTERVAL_MS, METRICS_KEEP_MS, METRICS_SETTLE_MS, metricsState, useMetrics } from './useMetrics'

beforeEach(() => vi.useFakeTimers())
afterEach(() => {
  vi.useRealTimers()
  Object.defineProperty(document, 'hidden', { value: false, configurable: true })
})

const ok = (values: MetricsView['values']): MetricsView => ({ status: 'ok', values })

/** A getMetrics whose answers the test releases, recording each call. */
function slowMetrics() {
  const calls: { ids: string[]; signal?: AbortSignal; resolve: (m: MetricsView) => void }[] = []
  const getMetrics = vi.fn(
    (_view: string, ids: string[], signal?: AbortSignal) =>
      new Promise<MetricsView>((resolve, reject) => {
        calls.push({ ids, signal, resolve })
        signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
      }),
  )
  const inFlight = () => calls.filter((c) => !c.signal?.aborted && !(c as { done?: boolean }).done).length
  const answer = async (i: number, m: MetricsView) => {
    ;(calls[i] as { done?: boolean }).done = true
    await act(async () => calls[i].resolve(m))
  }
  return { client: { getMetrics } as unknown as Client, getMetrics, calls, inFlight, answer }
}

describe('useMetrics', () => {
  it('polls after each completion and retries rarely when the API is missing', async () => {
    const getMetrics = vi.fn()
      .mockResolvedValueOnce({ status: 'unsupported', values: {} })
      .mockResolvedValue(ok({ u1: { cpu: 0.1, memory: 1 } }))
    const client = { getMetrics } as unknown as Client
    const { result } = renderHook(() => useMetrics(client, 'v1', true, ['u1']))
    await act(async () => {})
    expect(result.current?.status).toBe('unsupported')
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_INTERVAL_MS))
    expect(getMetrics).toHaveBeenCalledTimes(1) // not every 15 s while missing
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_ABSENT_RETRY_MS - METRICS_INTERVAL_MS))
    expect(getMetrics).toHaveBeenCalledTimes(2) // metrics-server appeared
    expect(result.current?.status).toBe('ok')
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_INTERVAL_MS))
    expect(getMetrics).toHaveBeenCalledTimes(3)
  })

  it('does not poll without a view id or without visible rows', async () => {
    const getMetrics = vi.fn(async () => ok({}))
    renderHook(() => useMetrics({ getMetrics } as unknown as Client, null, true, ['a']))
    renderHook(() => useMetrics({ getMetrics } as unknown as Client, 'v1', true, []))
    await act(async () => vi.advanceTimersByTimeAsync(60_000))
    expect(getMetrics).not.toHaveBeenCalled()
  })

  it('asks for the visible rows; fast scrolling keeps one request in flight and the latest area wins', async () => {
    const m = slowMetrics()
    const { result, rerender } = renderHook(({ ids }) => useMetrics(m.client, 'v1', true, ids), { initialProps: { ids: ['r0', 'r1'] } })
    await act(async () => {})
    expect(m.calls.map((c) => c.ids)).toEqual([['r0', 'r1']])
    // 20 scroll steps while the first request is slow
    for (let i = 1; i <= 20; i++) {
      rerender({ ids: [`r${i * 2}`, `r${i * 2 + 1}`] })
      await act(async () => vi.advanceTimersByTimeAsync(METRICS_SETTLE_MS + 10))
      expect(m.inFlight()).toBeLessThanOrEqual(1)
    }
    expect(m.calls).toHaveLength(1)
    await m.answer(0, ok({ r0: { cpu: 1 } }))
    // the latest area is asked for next, the ones in between never
    expect(m.calls.map((c) => c.ids)).toEqual([['r0', 'r1'], ['r40', 'r41']])
    await m.answer(1, ok({ r40: { cpu: 2, cpuPartial: true }, r41: { memory: 5 } }))
    expect(result.current?.values).toEqual({ r0: { cpu: 1 }, r40: { cpu: 2, cpuPartial: true }, r41: { memory: 5 } })
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_INTERVAL_MS))
    expect(m.calls).toHaveLength(3) // then the regular poll
  })

  it('a row asked for again without a value becomes unknown; rows scrolled away keep theirs', async () => {
    const m = slowMetrics()
    const { result, rerender } = renderHook(({ ids }) => useMetrics(m.client, 'v1', true, ids), { initialProps: { ids: ['a', 'b'] } })
    await act(async () => {})
    await m.answer(0, ok({ a: { cpu: 1 }, b: { cpu: 2 } }))
    rerender({ ids: ['c'] })
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_SETTLE_MS + 10))
    await m.answer(1, ok({ c: { cpu: 3 } }))
    expect(result.current?.values).toEqual({ a: { cpu: 1 }, b: { cpu: 2 }, c: { cpu: 3 } })
    rerender({ ids: ['a', 'b'] })
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_SETTLE_MS + 10))
    await m.answer(2, ok({ a: { cpu: 4 } }))
    expect(result.current?.values).toEqual({ a: { cpu: 4 }, c: { cpu: 3 } })
  })

  it('leaving the view or hiding the page aborts the request in flight; its late answer is dropped', async () => {
    const m = slowMetrics()
    const { result, unmount, rerender } = renderHook(({ view }) => useMetrics(m.client, view, true, ['a']), { initialProps: { view: 'v1' } })
    await act(async () => {})
    Object.defineProperty(document, 'hidden', { value: true, configurable: true })
    await act(async () => document.dispatchEvent(new Event('visibilitychange')))
    expect(m.calls[0].signal?.aborted).toBe(true)
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_INTERVAL_MS * 2))
    expect(m.calls).toHaveLength(1) // nothing while hidden
    Object.defineProperty(document, 'hidden', { value: false, configurable: true })
    await act(async () => document.dispatchEvent(new Event('visibilitychange')))
    expect(m.calls).toHaveLength(2)
    rerender({ view: 'v2' })
    await act(async () => {})
    expect(m.calls[1].signal?.aborted).toBe(true)
    await act(async () => m.calls[1].resolve(ok({ a: { cpu: 9 } })))
    expect(result.current).toBeNull()
    unmount()
    expect(m.calls[2].signal?.aborted).toBe(true)
  })

  it('a request asked for between polls replaces the scheduled poll: one chain', async () => {
    const m = slowMetrics()
    const { rerender } = renderHook(({ ids }) => useMetrics(m.client, 'v1', true, ids), { initialProps: { ids: ['a'] } })
    await act(async () => {})
    await m.answer(0, ok({ a: { cpu: 1 } })) // the next poll is due in 15 s
    await act(async () => vi.advanceTimersByTimeAsync(5_000))
    rerender({ ids: ['b'] })
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_SETTLE_MS))
    expect(m.calls).toHaveLength(2)
    await m.answer(1, ok({ b: { cpu: 2 } }))
    for (let i = 0; i < 4; i++) {
      await act(async () => vi.advanceTimersByTimeAsync(METRICS_INTERVAL_MS - 1))
      expect(m.calls).toHaveLength(2 + i) // not the old chain's poll
      await act(async () => vi.advanceTimersByTimeAsync(1))
      expect(m.calls).toHaveLength(3 + i)
      await m.answer(2 + i, ok({ b: { cpu: 2 } }))
    }
  })

  it('a failed request shows no values as current and says why', async () => {
    const getMetrics = vi.fn()
      .mockResolvedValueOnce(ok({ a: { cpu: 1 } }))
      .mockRejectedValueOnce(Object.assign(new Error('connection refused'), { detail: 'connection refused' }))
      .mockResolvedValue(ok({ a: { cpu: 3 } }))
    const client = { getMetrics } as unknown as Client
    const { result } = renderHook(() => useMetrics(client, 'v1', true, ['a']))
    await act(async () => {})
    expect(result.current?.values).toEqual({ a: { cpu: 1 } })
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_INTERVAL_MS))
    expect(result.current?.status).toBe('unavailable')
    expect(result.current?.message).toBe('connection refused')
    expect(result.current?.values).toEqual({})
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_INTERVAL_MS))
    expect(result.current).toEqual(ok({ a: { cpu: 3 } }))
  })

  it('a row whose state changed loses its value at once and is asked for again', async () => {
    const m = slowMetrics()
    const { result, rerender } = renderHook(({ states }) => useMetrics(m.client, 'v1', true, ['a', 'b'], states), {
      initialProps: { states: ['ok', 'ok'] },
    })
    await act(async () => {})
    await m.answer(0, ok({ a: { cpu: 1 }, b: { cpu: 2 } }))
    rerender({ states: ['error', 'ok'] }) // a stopped
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_SETTLE_MS + 10))
    expect(result.current?.values).toEqual({ b: { cpu: 2 } }) // not a's sample of when it ran
    expect(m.calls).toHaveLength(2) // not in 15 s
    await m.answer(1, ok({ b: { cpu: 3 } }))
    expect(result.current?.values).toEqual({ b: { cpu: 3 } })
    rerender({ states: ['error', 'ok'] }) // the same states: nothing new to ask
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_SETTLE_MS + 10))
    expect(m.calls).toHaveLength(2)
  })

  it('a row without a value that changed state (started) is asked for at once', async () => {
    const m = slowMetrics()
    const { result, rerender } = renderHook(({ states }) => useMetrics(m.client, 'v1', true, ['a', 'b'], states), {
      initialProps: { states: ['error', 'ok'] },
    })
    await act(async () => {})
    await m.answer(0, ok({ b: { cpu: 2 } })) // a is stopped: no usage
    rerender({ states: ['ok', 'ok'] }) // a started
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_SETTLE_MS + 10))
    expect(m.calls).toHaveLength(2)
    await m.answer(1, ok({ a: { cpu: 1 }, b: { cpu: 2 } }))
    expect(result.current?.values).toEqual({ a: { cpu: 1 }, b: { cpu: 2 } })
  })

  it('a row that started while asked for is asked for again after the answer', async () => {
    const m = slowMetrics()
    const { rerender } = renderHook(({ states }) => useMetrics(m.client, 'v1', true, ['a'], states), {
      initialProps: { states: ['error'] },
    })
    await act(async () => {})
    rerender({ states: ['ok'] })
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_SETTLE_MS + 10))
    await m.answer(0, ok({})) // read while it was stopped
    expect(m.calls).toHaveLength(2)
  })

  it('an answer asked for before a row changed state is not shown for it; the row is asked for again', async () => {
    const m = slowMetrics()
    const { result, rerender } = renderHook(({ states }) => useMetrics(m.client, 'v1', true, ['a', 'b'], states), {
      initialProps: { states: ['ok', 'ok'] },
    })
    await act(async () => {})
    rerender({ states: ['error', 'ok'] }) // while the first request is in flight
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_SETTLE_MS + 10))
    expect(m.calls).toHaveLength(1)
    await m.answer(0, ok({ a: { cpu: 1 }, b: { cpu: 2 } }))
    expect(result.current?.values).toEqual({ b: { cpu: 2 } })
    expect(m.calls).toHaveLength(2)
    await m.answer(1, ok({ b: { cpu: 2 } }))
    expect(result.current?.values).toEqual({ b: { cpu: 2 } })
  })

  it('values older than METRICS_KEEP_MS are not shown after a long hide', async () => {
    const m = slowMetrics()
    const { result } = renderHook(() => useMetrics(m.client, 'v1', true, ['a']))
    await act(async () => {})
    await m.answer(0, ok({ a: { cpu: 1 } }))
    Object.defineProperty(document, 'hidden', { value: true, configurable: true })
    await act(async () => document.dispatchEvent(new Event('visibilitychange')))
    await act(async () => vi.advanceTimersByTimeAsync(METRICS_KEEP_MS + 1))
    Object.defineProperty(document, 'hidden', { value: false, configurable: true })
    await act(async () => document.dispatchEvent(new Event('visibilitychange')))
    expect(m.calls).toHaveLength(2)
    expect(result.current?.values).toEqual({}) // not the 10-minute-old sample while asking again
  })
})

describe('metricsState', () => {
  const row = (running: string, age: number, health: Row['health']['state'] = 'error'): Row => ({
    id: 'p/logger',
    ref: { provider: 'compose', target: 't', kind: 'services', name: 'p/logger' },
    cells: [{ text: 'logger' }, { text: running }, { text: 'Exited' }, { num: 0 }, { time: age }, {}, {}],
    health: { state: health },
  })

  it('changes when a service loses its last running replica under the same health', () => {
    expect(metricsState(row('1/2', 1))).not.toBe(metricsState(row('0/2', 1)))
  })

  it('does not change with time alone', () => {
    expect(metricsState(row('1/2', 1))).toBe(metricsState(row('1/2', 2)))
    expect(metricsState(row('1/2', 1, 'ok'))).not.toBe(metricsState(row('1/2', 1)))
  })
})
