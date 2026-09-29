import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, type Client } from '../api/client'
import type { LogQuery, Ref } from '../api/types'
import { append, EMPTY_WINDOW, Ingest, MAX_LOG_CHARS, MAX_LOG_LINES, trim, type Window } from './buffer'
import { NdjsonDecoder, type Frame, type LineTuple } from './ndjson'

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
  state?: SourceState
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

interface Pending {
  src: number
  lines: LineTuple[]
}

/**
 * useLogStream owns one tab's stream and its line buffer. One request per
 * stream: the backend sends the recent lines, then live ones, and handles
 * reconnects to the cluster itself; a broken loopback connection or a closed
 * session ends the stream (the user reopens it explicitly).
 */
export function useLogStream({ client, ref, query, paused, frozen }: Options) {
  const [win, setWin] = useState<Window>(EMPTY_WINDOW)
  const [sources, setSources] = useState<Map<number, LogSource>>(() => new Map())
  const [status, setStatus] = useState<StreamStatus>({ phase: 'opening' })
  const [generation, setGeneration] = useState(0)

  const winRef = useRef<Window>(EMPTY_WINDOW)
  const ingestRef = useRef(new Ingest())
  const pendingRef = useRef<Pending[]>([])
  const pendingSize = useRef({ lines: 0, chars: 0 })
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

  const flush = useCallback(() => {
    if (pausedRef.current) return
    const batches = pendingRef.current
    if (!batches.length && !droppedWhilePaused.current) return
    pendingRef.current = []
    pendingSize.current = { lines: 0, chars: 0 }
    const add = batches.flatMap((b) => ingestRef.current.entries(b.src, b.lines))
    const w = append(winRef.current, add, frozenRef.current)
    commit(droppedWhilePaused.current ? { ...w, evicted: w.evicted + droppedWhilePaused.current } : w)
    droppedWhilePaused.current = 0
  }, [commit])

  const schedule = useCallback(() => {
    if (timerRef.current) return
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      flush()
    }, LOG_FLUSH_MS)
  }, [flush])

  const queue = useCallback(
    (src: number, lines: LineTuple[]) => {
      pendingRef.current.push({ src, lines })
      const size = pendingSize.current
      size.lines += lines.length
      for (const l of lines) size.chars += l[1].length
      // A firehose during a long selection drag must not grow without bound:
      // the oldest waiting lines go (and are counted as evicted).
      while ((size.lines > MAX_LOG_LINES || size.chars > MAX_LOG_CHARS) && pendingRef.current.length > 1) {
        const old = pendingRef.current.shift()!
        size.lines -= old.lines.length
        for (const l of old.lines) size.chars -= l[1].length
        droppedWhilePaused.current += old.lines.length
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
    const q = JSON.parse(queryKey) as LogQuery
    ingestRef.current = new Ingest()
    pendingRef.current = []
    pendingSize.current = { lines: 0, chars: 0 }
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
          queue(f.s, f.l)
          break
        case 'state':
          if (f.s === 0) {
            setStatus((st) => ({ ...st, notice: f.state === 'streaming' ? undefined : { state: f.state, class: f.class, msg: f.msg } }))
          } else {
            setSources((m) => {
              const s = m.get(f.s)
              return s ? new Map(m).set(f.s, { ...s, state: { state: f.state, class: f.class, msg: f.msg } }) : m
            })
          }
          break
        case 'ready':
          flush()
          setStatus((st) => ({ ...st, phase: q.follow ? 'live' : 'backlog' }))
          break
        case 'end':
          ended = true
          flush()
          setStatus((st) =>
            f.reason === 'done' ? { ...st, phase: 'done' } : f.reason === 'gone' ? { ...st, phase: 'gone' } : { ...st, phase: 'error', cls: f.class, message: f.message },
          )
          break
      }
    }

    void (async () => {
      try {
        const { streamId } = await client.openLogStream(ref, q)
        const base = await client.streamBase()
        const res = await fetch(`${base}/logs/${encodeURIComponent(streamId)}`, { signal: ac.signal, cache: 'no-store' })
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
          if (done) break
          for (const f of dec.push(value)) onFrame(f)
        }
        for (const f of dec.end()) onFrame(f)
        if (!ended) {
          flush()
          setStatus((st) => ({ ...st, phase: 'disconnected' }))
        }
      } catch (e) {
        if (ac.signal.aborted) return
        flush()
        if (e instanceof ApiError) setStatus({ phase: e.code === 'gone' ? 'gone' : 'error', cls: e.code, message: e.detail })
        else setStatus((st) => ({ ...st, phase: ended ? st.phase : 'disconnected', message: e instanceof Error ? e.message : String(e) }))
      }
    })()

    return () => {
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
    pendingSize.current = { lines: 0, chars: 0 }
    commit({ entries: [], chars: 0, evicted: 0 })
  }, [commit])

  return { win, sources, status, reopen, clear }
}
