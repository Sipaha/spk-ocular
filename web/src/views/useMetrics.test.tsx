import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Client } from '../api/client'
import { METRICS_ABSENT_RETRY_MS, METRICS_INTERVAL_MS, useMetrics } from './useMetrics'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('useMetrics', () => {
  it('polls after each completion and retries rarely when the API is missing', async () => {
    const getMetrics = vi.fn()
      .mockResolvedValueOnce({ status: 'unsupported', values: {} })
      .mockResolvedValue({ status: 'ok', values: { u1: { cpu: 0.1, memory: 1 } } })
    const client = { getMetrics } as unknown as Client
    const { result } = renderHook(() => useMetrics(client, 'v1', true))
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

  it('does not poll without a view id', async () => {
    const getMetrics = vi.fn()
    renderHook(() => useMetrics({ getMetrics } as unknown as Client, null, true))
    await act(async () => vi.advanceTimersByTimeAsync(60_000))
    expect(getMetrics).not.toHaveBeenCalled()
  })
})
