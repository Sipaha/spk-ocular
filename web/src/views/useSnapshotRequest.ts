import { useContext, useEffect, useRef, useState } from 'react'
import { ViewConnectionContext } from './useView'

/** Finite reads share refresh, cancellation and late-response fencing. Live
 * resource watches retain ViewSync and use the same loading presentation. */
export function useSnapshotRequest<T>(load: (signal: AbortSignal) => Promise<T>, scopeKey: string, options: {enabled?: boolean; connectionScoped?: boolean} = {}) {
 const enabled = options.enabled ?? true
 const connection = useContext(ViewConnectionContext)
 const key = JSON.stringify([scopeKey, options.connectionScoped === false ? null : connection])
 const [revision, setRevision] = useState(0)
 const [state, setState] = useState(() => ({ load, key, revision, enabled, value: null as T | null, busy: enabled, cancelled: false, error: null as unknown, startedAt: undefined as number | undefined }))
 const active = useRef<AbortController | null>(null)
 const pending = useRef(false)
 if (state.load !== load || state.key !== key || state.revision !== revision || state.enabled !== enabled) {
  setState({ load, key, revision, enabled, value: enabled && state.enabled && state.key === key && state.load === load ? state.value : null, busy: enabled, cancelled: false, error: null, startedAt: undefined })
 }
 useEffect(() => {
  if (!enabled) return
  const controller = new AbortController()
  active.current = controller; pending.current = true
  const current = () => active.current === controller && !controller.signal.aborted
  void Promise.resolve().then(() => {
   if (current()) {
    setState(old => old.load === load && old.key === key && old.revision === revision ? { ...old, startedAt: Date.now() } : old)
    return load(controller.signal)
   }
  }).then(value => {
   if (current()) setState(old => old.load === load && old.key === key && old.revision === revision ? { ...old, value: value!, error: null, busy: false } : old)
  }, error => {
   if (current()) setState(old => old.load === load && old.key === key && old.revision === revision ? { ...old, error, busy: false } : old)
  }).finally(() => { if (active.current === controller) pending.current = false })
  return () => { controller.abort(); if (active.current === controller) { active.current = null; pending.current = false } }
 }, [load, key, revision, enabled])
 const refresh = () => {
  if (!enabled || state.busy || pending.current) return
  pending.current = true
  setState(old => ({ ...old, busy: true, cancelled: false, error: null, startedAt: Date.now() }))
  setRevision(value => value + 1)
 }
 const cancel = () => {
  const controller = active.current; active.current = null; pending.current = false; controller?.abort()
  setState(old => ({ ...old, busy: false, cancelled: true, error: null }))
 }
 return { value: state.value, busy: state.busy, cancelled: state.cancelled, error: state.error, refresh, cancel,
  loading: { loadingStartedAt: state.startedAt, status: { state: state.busy ? 'loading' as const : 'ready' as const }, cancelLoading: cancel },
 }
}
