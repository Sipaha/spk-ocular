import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, asMessage, type Client } from '../api/client'
import { messageText } from '../i18n'
import type { LogQuery, Ref } from '../api/types'
import { append, EMPTY_WINDOW, entryChars, Ingest, MAX_LOG_CHARS, MAX_LOG_LINES, trim, type LogEntry, type Window } from './buffer'
import { NdjsonDecoder, type Frame } from './ndjson'
import { errorDetail } from '../errors'

/** Coalescing window: received lines are applied at most this often. */
export const LOG_FLUSH_MS = 80

export interface SourceState {
  state: string
  class?: string
  msg?: string
}

export interface LogSource {
  id: number
  key: string
  label: string
  channel?: string
  /** The current state (streaming, waiting, ended, error, …). */
  state?: SourceState
  /**
   * The last "history may be incomplete" signal (gap, truncated): sticky, so
   * it cannot be overwritten by the "streaming" that follows it at once.
   */
  warn?: SourceState
}

/**
 * The stream's own lifecycle. opening: registering / connecting; backlog:
 * connected, recent lines arriving; live: backlog done, following; done:
 * complete (not following, or every source ended); gone: its session was
 * closed (context changed) — terminal; disconnected: the connection broke;
 * error: it could not be opened or failed (message).
 */
export type StreamPhase = 'opening' | 'backlog' | 'live' | 'done' | 'gone' | 'disconnected' | 'error'

export interface StreamStatus {
  phase: StreamPhase
  cls?: string
  message?: string
  /** Stream-level provider state (e.g. "showing 20 of 31 container streams"). */
  notice?: SourceState
}

interface Options {
  client: Client
  ref: Ref
  query: LogQuery
  /** While true (a mouse drag-selection), received lines wait: any DOM update would collapse the selection. */
  paused: boolean
  /** Not following: the window may stretch instead of trimming under the reader. */
  frozen: boolean
}

const WARN_STATES = new Set(['gap', 'truncated'])
const DONE_STATES = new Set(['ended', 'error'])

/**
 * useLogStream owns one tab's stream and its line buffer. One request per
 * stream: the backend sends the recent lines, then live ones, and handles
 * reconnects to the cluster itself. A broken loopback connection or a
 * closed session ends the stream; "reopen" starts a fresh one (the buffer
 * starts over: the new stream sends its own recent lines).
 *
 * Every async continuation checks that its stream is still the current one
 * (a late chunk of a replaced stream must not reach the new buffer), and a
 * stream that can no longer be read (broken frame) is aborted at once.
 */
export function useLogStream({ client, ref, query, paused, frozen }: Options) {
  const [win, setWin] = useState<Window>(EMPTY_WINDOW)
  const [sources, setSources] = useState<Map<number, LogSource>>(() => new Map())
  const [status, setStatus] = useState<StreamStatus>({ phase: 'opening' })
  const [generation, setGeneration] = useState(0)

  const winRef = useRef<Window>(EMPTY_WINDOW)
  // One Ingest for the tab's life: line ids stay unique across reopened
  // streams (search results and row keys are by id).
  const ingestRef = useRef(new Ingest())
  const pendingRef = useRef<LogEntry[]>([])
  const pendingChars = useRef(0)
  const droppedWhilePaused = useRef(0)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const pausedRef = useRef(paused)
  const frozenRef = useRef(frozen)
  useEffect(() => {
    pausedRef.current = paused
    frozenRef.current = frozen
  }, [paused, frozen])

  const commit = useCallback((w: Window) => {
    winRef.current = w
    setWin(w)
  }, [])

  // Sources that finished and have no line left in the buffer are forgotten
  // (label, state, ANSI/level carry): a tab held open through many rollouts
  // does not accumulate them.
  const prune = useCallback((w: Window) => {
    setSources((m) => {
      const used = new Set<number>()
      for (const e of w.entries) used.add(e.src)
      for (const e of pendingRef.current) used.add(e.src)
      let next: Map<number, LogSource> | null = null
      for (const [id, s] of m) {
        if (used.has(id) || !s.state || !DONE_STATES.has(s.state.state)) continue
        next ??= new Map(m)
        next.delete(id)
        ingestRef.current.forgetSource(id)
      }
      return next ?? m
    })
  }, [])

  const flush = useCallback(() => {
    if (pausedRef.current) return
    const add = pendingRef.current
    if (!add.length && !droppedWhilePaused.current) return
    pendingRef.current = []
    pendingChars.current = 0
    const before = winRef.current.evicted
    let w = append(winRef.current, add, frozenRef.current)
    if (droppedWhilePaused.current) w = { ...w, evicted: w.evicted + droppedWhilePaused.current }
    droppedWhilePaused.current = 0
    commit(w)
    if (w.evicted !== before) prune(w)
  }, [commit, prune])

  const schedule = useCallback(() => {
    if (timerRef.current) return
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      flush()
    }, LOG_FLUSH_MS)
  }, [flush])

  const queue = useCallback(
    (src: number, lines: Frame & { k: 'lines' }) => {
      // Ingested at once: the per-source level/ANSI carry advances even for
      // lines dropped below while a selection drag holds them back.
      const entries = ingestRef.current.entries(src, lines.l)
      const pending = pendingRef.current
      for (const e of entries) {
        pending.push(e)
        pendingChars.current += entryChars(e)
      }
      let drop = 0
      while (pending.length - drop > MAX_LOG_LINES || (pendingChars.current > MAX_LOG_CHARS && pending.length - drop > 1)) {
        pendingChars.current -= entryChars(pending[drop++])
      }
      if (drop) {
        pendingRef.current = pending.slice(drop)
        droppedWhilePaused.current += drop
      }
      schedule()
    },
    [schedule],
  )

  // Selection released → apply what waited; following again → trim back.
  useEffect(() => {
    if (!paused) flush()
  }, [paused, flush])
  useEffect(() => {
    if (!frozen) {
      const w = trim(winRef.current, false)
      if (w !== winRef.current) commit(w)
    }
  }, [frozen, commit])

  const refKey = `${ref.provider}/${ref.target}/${ref.kind}/${ref.scope ?? ''}/${ref.name}/${ref.uid ?? ''}`
  const queryKey = JSON.stringify(query)

  useEffect(() => {
    const ac = new AbortController()
    let live = true // this effect's stream is the current one
    const q = JSON.parse(queryKey) as LogQuery
    ingestRef.current.forget()
    pendingRef.current = []
    pendingChars.current = 0
    droppedWhilePaused.current = 0
    commit(EMPTY_WINDOW)
    setSources(new Map())
    setStatus({ phase: 'opening' })
    let ended = false

    const onFrame = (f: Frame) => {
      switch (f.k) {
        case 'source':
          setSources((m) => new Map(m).set(f.id, { id: f.id, key: f.key, label: f.label, channel: f.channel }))
          break
        case 'lines':
          queue(f.s, f)
          break
        case 'state':
          if (f.s === 0) {
            setStatus((st) => ({ ...st, notice: f.state === 'streaming' ? undefined : { state: f.state, class: f.class, msg: f.msg } }))
            break
          }
          // A state change is a boundary (restart, reconnect): the next
          // line does not continue the previous one's ANSI style or level.
          flush()
          ingestRef.current.forgetSource(f.s)
          setSources((m) => {
            const s = m.get(f.s)
            if (!s) return m
            const st = { state: f.state, class: f.class, msg: f.msg }
            return new Map(m).set(f.s, { ...s, state: st, warn: WARN_STATES.has(f.state) ? st : s.warn })
          })
          break
        case 'ready':
          flush()
          setStatus((st) => ({ ...st, phase: q.follow ? 'live' : 'backlog' }))
          break
        case 'end':
          ended = true
          flush()
          setStatus((st) => {
            const why = asMessage(f.why)
            // "gone" also says why (closed by the user, a login needed): the
            // bare phase text reads like a context change, which it may not be.
            if (f.reason === 'done') return { ...st, phase: 'done' }
            if (f.reason === 'gone') return why ? { ...st, phase: 'gone', message: messageText(why) } : { ...st, phase: 'gone' }
            return { ...st, phase: 'error', cls: f.class, message: why ? messageText(why) : f.message }
          })
          break
      }
    }

    void (async () => {
      try {
        const { streamId } = await client.openLogStream(ref, q)
        const base = await client.streamBase()
        if (!live) return // the tab went away before connecting: no request (the ticket expires)
        const res = await fetch(`${base}/logs/${encodeURIComponent(streamId)}`, { signal: ac.signal, cache: 'no-store' })
        if (!live) return
        if (res.status === 410) {
          setStatus({ phase: 'gone' })
          return
        }
        if (!res.ok || !res.body) {
          setStatus({ phase: 'error', message: `HTTP ${res.status}` })
          return
        }
        setStatus((st) => ({ ...st, phase: 'backlog' }))
        const reader = res.body.getReader()
        const dec = new NdjsonDecoder()
        for (;;) {
          const { done, value } = await reader.read()
          if (!live) return
          if (done) break
          for (const f of dec.push(value)) onFrame(f)
          if (ended) break
        }
        if (!ended) for (const f of dec.end()) onFrame(f)
        ac.abort() // finished (or ended by the server): release the connection
        if (!ended) {
          flush()
          setStatus((st) => ({ ...st, phase: 'disconnected' }))
        }
      } catch (e) {
        if (!live || (ac.signal.aborted && ended)) return
        ac.abort() // a stream that cannot be read (a broken frame) must not stay open
        flush()
        if (e instanceof ApiError) setStatus({ phase: e.code === 'gone' ? 'gone' : 'error', cls: e.code, message: e.why ? errorDetail(e) : e.detail })
        else if (e instanceof SyntaxError || (e instanceof Error && e.message.includes('frame'))) setStatus((st) => ({ ...st, phase: 'error', message: e.message }))
        else setStatus((st) => ({ ...st, phase: ended ? st.phase : 'disconnected', message: e instanceof Error ? e.message : String(e) }))
      }
    })()

    return () => {
      live = false
      ac.abort()
      if (timerRef.current) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
    }
    // ref is identified by refKey; a new generation reopens
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, refKey, queryKey, generation, commit, flush, queue])

  const reopen = useCallback(() => setGeneration((g) => g + 1), [])
  const clear = useCallback(() => {
    pendingRef.current = []
    pendingChars.current = 0
    commit({ entries: [], chars: 0, evicted: 0 })
  }, [commit])

  return { win, sources, status, reopen, clear }
}
