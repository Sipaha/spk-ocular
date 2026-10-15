import { createContext, useContext, useEffect, useMemo, useState, useSyncExternalStore } from 'react'
import type { Query } from '../api/types'
import type { ViewHub, ViewState } from './viewSync'

export const ViewConnectionContext = createContext<string | number>(0)

/** A live table for (provider, target, query); reopened when any of them changes. */
export function useView(hub: ViewHub, provider: string, target: string, query: Query): ViewState & { cancelLoading: () => void; retry: () => void } {
  const connection = useContext(ViewConnectionContext)
  const key = JSON.stringify([provider, target, query, connection])
  // prepare() has no side effects; the effect starts and stops the view.
  const sync = useMemo(() => {
    const [p, t, q, c] = JSON.parse(key) as [string, string, Query, string | number]
    return hub.prepare(p, t, q, c)
  }, [hub, key])
  useEffect(() => {
    hub.attach(sync)
    return () => hub.release(sync)
  }, [hub, sync])
  const state = useSyncExternalStore(sync.subscribe, sync.snapshot)
  return { ...state, cancelLoading: sync.cancelLoading, retry: sync.retry }
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
