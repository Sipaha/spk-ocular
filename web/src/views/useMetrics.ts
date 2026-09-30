import { useEffect, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { MetricsView, Row, Usage } from '../api/types'
import { errorDetail } from '../errors'

export const METRICS_INTERVAL_MS = 15_000
export const METRICS_ABSENT_RETRY_MS = 120_000
/** A new visible area is asked for once scrolling settles this long. */
export const METRICS_SETTLE_MS = 300
/**
 * A visible row's change asks for the view again at once, but at most this
 * often after the previous request (a crashlooping row must not keep the
 * whole screen's stats busy); its old value is dropped at once regardless.
 */
export const METRICS_REFRESH_MIN_MS = 5_000
/** A sample is shown at most this long (rows scrolled away keep theirs). */
export const METRICS_KEEP_MS = 10 * 60_000

/**
 * A row's state for metrics (`states` of useMetrics): its health and its
 * cells' values — a service's running count, a container's status and
 * restarts — without times (an age is no change). A sample taken in
 * another state is not shown as current.
 */
export const metricsState = (r: Row) => r.health.state + '\t' + r.cells.map((c) => `${c.text ?? ''}:${c.num ?? ''}`).join('\t')

const sameIds = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i])

/**
 * Usage of the rows the table shows (`visible`, in order): asked for at
 * once when the visible set changes (after METRICS_SETTLE_MS), else every
 * 15 s after the previous request finished. One request in flight per view:
 * a newer visible set waits for it (only the latest is kept), and leaving
 * the view or hiding the page aborts it — in the app's Go side too.
 * Values of rows scrolled away are kept (with their sample time) until
 * asked for again or METRICS_KEEP_MS pass (checked at every request, also
 * after a long hide); results of an older view or an aborted request are
 * dropped. A failed request shows no values: its error is the status.
 * `states` (parallel to `visible`) is each row's state: a sample taken in
 * another state (a container since stopped) is not shown — the row loses
 * it at once and the view is asked for again soon (METRICS_REFRESH_MIN_MS
 * after the previous request at the earliest; changes meanwhile coalesce).
 */
export function useMetrics(client: Client, viewId: string | null, enabled: boolean, visible: string[], states?: string[]): MetricsView | null {
  const [state, setState] = useState<{ viewId: string; m: MetricsView } | null>(null)
  const visibleRef = useRef<string[]>(visible)
  const statesRef = useRef(new Map<string, string>())
  const askRef = useRef<(() => void) | null>(null)

  const key = visible.join('\n')
  const stateKey = states?.join('\n') ?? ''
  useEffect(() => {
    visibleRef.current = key ? key.split('\n') : []
    const st = stateKey ? stateKey.split('\n') : []
    statesRef.current = new Map(visibleRef.current.map((id, i) => [id, st[i] ?? '']))
    const t = setTimeout(() => askRef.current?.(), METRICS_SETTLE_MS)
    return () => clearTimeout(t)
  }, [key, stateKey])

  useEffect(() => {
    if (!enabled || !viewId) return
    let live = true
    let inFlight: AbortController | null = null
    let again = false // a newer visible set waits for the request in flight
    let timer: ReturnType<typeof setTimeout> | null = null
    let timerAt = 0
    let restale = false // a row changed state while a request was in flight
    let lastSent = -Infinity
    let absentUntil = 0
    let asked: string[] = []
    let askedStates = new Map<string, string | undefined>() // the visible rows' states when asked
    const values = new Map<string, { u: Usage; seen: number; state: string | undefined }>()
    let shown: MetricsView | null = null // the last "ok" answer
    const publish = (m: MetricsView) => setState({ viewId, m: { ...m, values: Object.fromEntries([...values].map(([id, v]) => [id, v.u])) } })
    const expire = (now: number) => {
      let dropped = false
      for (const [id, v] of values)
        if (now - v.seen > METRICS_KEEP_MS) {
          values.delete(id)
          dropped = true
        }
      return dropped
    }
    // Drops the values of visible rows sampled in another state; says
    // whether one was, or a row asked for has changed state since (a
    // stopped one started: ask now, not at the next poll).
    const restate = () => {
      let dropped = false
      let changed = false
      for (const [id, st] of statesRef.current) {
        const v = values.get(id)
        if (v && v.state !== st) {
          values.delete(id)
          dropped = true
        }
        if (askedStates.has(id) && askedStates.get(id) !== st) changed = true
      }
      return { dropped, changed: changed || dropped }
    }

    const schedule = (ms: number) => {
      if (timer) clearTimeout(timer)
      timerAt = Date.now() + ms
      timer = live ? setTimeout(tick, ms) : null
    }
    // A refresh for changed rows: not before METRICS_REFRESH_MIN_MS after
    // the last request; an earlier scheduled request already serves.
    const soon = () => {
      const at = Math.max(Date.now(), lastSent + METRICS_REFRESH_MIN_MS)
      if (timer !== null && timerAt <= at) return
      schedule(at - Date.now())
    }
    const tick = async () => {
      if (timer) clearTimeout(timer) // an ask between polls replaces the poll
      timer = null
      if (!live || document.hidden) return // resumed by visibilitychange
      if (inFlight) {
        again = true
        return
      }
      const ids = visibleRef.current
      if (!ids.length) return // asked for when rows show
      if (expire(Date.now()) && shown) publish(shown)
      asked = ids
      askedStates = new Map(ids.map((id) => [id, statesRef.current.get(id)]))
      const sent = askedStates
      lastSent = Date.now()
      restale = false
      const ac = new AbortController()
      inFlight = ac
      let next = METRICS_INTERVAL_MS
      let stale = false // a row changed state while asked for: ask again soon
      try {
        const m = await client.getMetrics(viewId, ids, ac.signal)
        if (!live || ac.signal.aborted) return
        const now = Date.now()
        if (m.status === 'ok') {
          for (const id of ids) {
            const u = m.values[id]
            const st = sent.get(id)
            // A row scrolled away meanwhile keeps its state as asked.
            const cur = statesRef.current.has(id) ? statesRef.current.get(id) : st
            if (st !== cur) {
              values.delete(id) // its state changed meanwhile
              stale = true
            } else if (u) values.set(id, { u, seen: now, state: st })
            else values.delete(id) // asked and unknown now
          }
          expire(now)
          absentUntil = 0 // the API answers (again)
          shown = m
          publish(m)
        } else {
          values.clear()
          shown = null
          setState({ viewId, m })
          if (m.status === 'unsupported') {
            // No metrics API (yet): ask rarely, metrics-server may be installed.
            absentUntil = now + METRICS_ABSENT_RETRY_MS
            next = METRICS_ABSENT_RETRY_MS
          }
        }
      } catch (e) {
        if (ac.signal.aborted || !live) return
        // No answer: nothing is current. (gone: the view reopens with a new
        // id and a new effect.)
        values.clear()
        shown = null
        const message = errorDetail(e)
        setState({ viewId, m: { status: 'unavailable', message, values: {} } })
      } finally {
        if (inFlight === ac) inFlight = null
      }
      if (!live) return
      if (again && Date.now() >= absentUntil && !sameIds(asked, visibleRef.current)) {
        again = false
        void tick()
        return
      }
      again = false
      schedule(next)
      if ((stale || restale) && Date.now() >= absentUntil) soon() // not past the absent back-off
    }
    const ask = () => {
      if (!live) return
      // A value of a past state goes at once, whatever is asked for.
      const { dropped, changed } = restate()
      if (dropped && shown) publish(shown)
      if (Date.now() < absentUntil) return
      const moved = !sameIds(asked, visibleRef.current)
      if (inFlight) {
        if (moved) again = true
        else if (changed) restale = true
        return
      }
      if (moved || timer === null) void tick()
      else if (changed) soon()
    }
    askRef.current = ask
    const onVisible = () => {
      if (!live) return
      if (document.hidden) {
        inFlight?.abort()
        inFlight = null
        if (timer) clearTimeout(timer)
        timer = null
      } else if (!inFlight && timer === null) void tick()
    }
    document.addEventListener('visibilitychange', onVisible)
    void tick()
    return () => {
      live = false
      askRef.current = null
      inFlight?.abort()
      if (timer) clearTimeout(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [client, viewId, enabled])
  return state && state.viewId === viewId ? state.m : null
}
