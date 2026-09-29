import { useEffect, useState } from 'react'
import type { Client } from '../api/client'
import type { MetricsView } from '../api/types'

export const METRICS_INTERVAL_MS = 15_000

/**
 * Usage for the visible table, refreshed every 15 s after the previous
 * request finished (never overlapping), paused while the page is hidden.
 * Results of a previous view are dropped.
 */
export function useMetrics(client: Client, viewId: string | null, enabled: boolean): MetricsView | null {
  const [state, setState] = useState<{ viewId: string; m: MetricsView } | null>(null)
  useEffect(() => {
    if (!enabled || !viewId) return
    let live = true
    let busy = false
    let timer: ReturnType<typeof setTimeout> | null = null
    const schedule = () => {
      if (!live) return
      timer = setTimeout(tick, METRICS_INTERVAL_MS)
    }
    const tick = async () => {
      timer = null
      if (document.hidden || busy) return // resumed by visibilitychange
      busy = true
      try {
        const m = await client.getMetrics(viewId)
        if (!live) return
        setState({ viewId, m })
        if (m.status === 'unsupported') {
          busy = false
          return // no metrics API: stop asking for this view
        }
      } catch {
        // gone/unavailable: the view reopens with a new id and a new effect
      } finally {
        busy = false
      }
      schedule()
    }
    const onVisible = () => {
      if (!document.hidden && live && timer === null) void tick()
    }
    document.addEventListener('visibilitychange', onVisible)
    void tick()
    return () => {
      live = false
      if (timer) clearTimeout(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [client, viewId, enabled])
  return state && state.viewId === viewId ? state.m : null
}
