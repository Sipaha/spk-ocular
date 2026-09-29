import { useEffect, useMemo, useState, useSyncExternalStore } from 'react'
import type { Query } from '../api/types'
import type { ViewHub, ViewState } from './viewSync'

/** A live table for (provider, target, query); reopened when any of them changes. */
export function useView(hub: ViewHub, provider: string, target: string, query: Query): ViewState {
  const key = JSON.stringify([provider, target, query])
  // prepare() has no side effects; the effect starts and stops the view.
  const sync = useMemo(() => {
    const [p, t, q] = JSON.parse(key) as [string, string, Query]
    return hub.prepare(p, t, q)
  }, [hub, key])
  useEffect(() => {
    hub.attach(sync)
    return () => hub.release(sync)
  }, [hub, sync])
  return useSyncExternalStore(sync.subscribe, sync.snapshot)
}

/** Re-renders every `ms` (ages tick in the UI, not the backend). */
export function useNow(ms: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms)
    return () => clearInterval(t)
  }, [ms])
  return now
}
