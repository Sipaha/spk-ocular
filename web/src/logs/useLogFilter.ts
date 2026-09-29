import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { LogEntry } from './buffer'
import { LOG_LEVELS, type LogLevel } from './levels'
import { compileFilter, compileRegex, plainRanges, type Range } from './match'
import { RegexSearch, type RegexState, type WorkerLike } from './regexSearch'

// DEBUG is hidden by default (high volume, low signal); one click brings it back.
const DEFAULT_LEVELS: LogLevel[] = LOG_LEVELS.filter((l) => l !== 'DEBUG')

const makeWorker = (): WorkerLike => new Worker(new URL('./search.worker.ts', import.meta.url), { type: 'module' }) as unknown as WorkerLike

const useDebounced = (v: string, ms = 250) => {
  const [d, setD] = useState(v)
  useEffect(() => {
    const t = setTimeout(() => setD(v.slice(0, 500)), ms)
    return () => clearTimeout(t)
  }, [v, ms])
  return d
}

/**
 * useLogFilter owns what lies between the buffer and the rendered list:
 * level toggles, the "*" hide-filter, search (plain, or regex in a worker)
 * and match navigation. navTick bumps only on explicit navigation, so new
 * lines never yank the viewport to a match.
 */
export function useLogFilter(entries: LogEntry[], opts: { worker?: () => WorkerLike } = {}) {
  const [search, setSearch] = useState('')
  const [useRegex, setUseRegex] = useState(false)
  const [filterText, setFilterText] = useState('')
  const [levels, setLevels] = useState<Set<LogLevel>>(() => new Set(DEFAULT_LEVELS))
  const [matchIndex, setMatchIndex] = useState(0)
  const [navTick, setNavTick] = useState(0)
  const bumpNav = useCallback(() => setNavTick((n) => n + 1), [])
  const q = useDebounced(search)
  const f = useDebounced(filterText)

  const filtered = useMemo(() => {
    const pass = compileFilter(f)
    const all = levels.size === LOG_LEVELS.length
    if (!pass && all) return entries
    return entries.filter((e) => (all || levels.has(e.level ?? 'UNKNOWN')) && (!pass || pass(e.plain)))
  }, [entries, levels, f])

  // regex: compiled here only to validate; matching happens in the worker
  const regex = useMemo(() => (useRegex && q ? compileRegex(q) : null), [useRegex, q])
  const regexError = regex && 'error' in regex ? regex.error : null
  // the worker's answers, copied out of RegexSearch for rendering
  const [rx, setRx] = useState<{ state: RegexState; hits: Map<number, Range[]> }>({ state: 'idle', hits: new Map() })
  const regexState = rx.state
  const rsRef = useRef<RegexSearch | null>(null)
  const factory = opts.worker ?? makeWorker
  const entriesRef = useRef(entries)
  useEffect(() => {
    entriesRef.current = entries
  }, [entries])

  useEffect(() => {
    const rs = new RegexSearch(factory, () => setRx({ state: rs.state, hits: new Map(rs.hits) }))
    rsRef.current = rs
    rs.setPattern(regex && 're' in regex ? regex.re.source : '', entriesRef.current)
    return () => rs.stop()
  }, [regex, factory])

  useEffect(() => {
    const rs = rsRef.current
    if (!rs || rs.state === 'idle' || rs.state === 'slow') return
    if (entries.length) rs.drop(entries[0].id)
    rs.add(entries)
  }, [entries])

  const qLower = q.toLowerCase()
  const regexOn = !!regex && 're' in regex && regexState !== 'slow'
  const hits = rx.hits
  const rangesFor = useCallback(
    (e: LogEntry): Range[] => (!q ? [] : regexOn ? (hits.get(e.id) ?? []) : useRegex ? [] : plainRanges(e.plain, qLower)),
    [q, qLower, regexOn, useRegex, hits],
  )

  const matches = useMemo(() => {
    const out: number[] = []
    if (!q) return out
    if (regexOn) {
      filtered.forEach((e, i) => hits.has(e.id) && out.push(i))
    } else if (!useRegex) {
      filtered.forEach((e, i) => e.plain.toLowerCase().includes(qLower) && out.push(i))
    }
    return out
  }, [filtered, q, qLower, regexOn, useRegex, hits])

  const current = matches.length ? ((matchIndex % matches.length) + matches.length) % matches.length : 0

  // a new query starts at its first match and scrolls there
  const [prev, setPrev] = useState({ q, r: useRegex })
  if (prev.q !== q || prev.r !== useRegex) {
    setPrev({ q, r: useRegex })
    setMatchIndex(0)
    if (q) setNavTick((n) => n + 1)
  }

  const toggleLevel = useCallback((l: LogLevel) => {
    setLevels((s) => {
      const n = new Set(s)
      if (n.has(l)) n.delete(l)
      else n.add(l)
      return n
    })
  }, [])

  return {
    search, setSearch, useRegex, setUseRegex, filterText, setFilterText, levels, toggleLevel,
    filtered, matches, current, setMatchIndex, navTick, bumpNav, rangesFor,
    regexState: regexError ? ('invalid' as const) : regexState, regexError,
  }
}
