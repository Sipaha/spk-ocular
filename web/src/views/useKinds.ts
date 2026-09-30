import { useCallback, useEffect, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { KindDescriptor, KindsView } from '../api/types'
import type { ViewHub } from './viewSync'

export interface Kinds {
  /** The session's catalog as last listed; null until the first answer. */
  view: KindsView | null
  /** Listing failed (and nothing was listed yet or since). */
  error: string | null
  /** Kinds a later listing no longer had (their pages say "no longer served"). */
  removed: ReadonlyMap<string, KindDescriptor>
  /** Per kind, how many times it came back after being absent: a page is
   * of one appearance (a kind served again gets a fresh view). */
  appeared: ReadonlyMap<string, number>
  /** F5 in the navigation: the target reads its kinds again. */
  refresh: () => void
}

interface State {
  view: KindsView | null
  error: string | null
  removed: Map<string, KindDescriptor>
  appeared: Map<string, number>
}

/**
 * The target's kinds, kept current: listed on mount, again on the
 * catalog's kinds_changed hint (this target's, newer than what was
 * listed), on resync (hints may have been lost) and on F5. The latest
 * request wins; an answer to an older one is dropped.
 */
export function useKinds(client: Client, hub: ViewHub, provider: string, target: string): Kinds {
  const [st, setSt] = useState<State>({ view: null, error: null, removed: new Map(), appeared: new Map() })
  const seq = useRef(0)
  const known = useRef<{ session: number; rev: number } | null>(null)

  const load = useCallback(() => {
    const n = ++seq.current
    client.listKinds(provider, target).then(
      (v) => {
        if (n !== seq.current) return
        known.current = { session: v.session, rev: v.rev }
        setSt((old) => {
          const removed = new Map(old.removed)
          const appeared = new Map(old.appeared)
          const now = new Set(v.kinds.map((k) => k.id))
          const before = new Set((old.view?.kinds ?? []).map((k) => k.id))
          // Still discovering (a new session knows only the described kinds
          // yet): a kind not listed is pending, not removed.
          if (v.state !== 'discovering') for (const k of old.view?.kinds ?? []) if (!now.has(k.id)) removed.set(k.id, k)
          for (const id of now) {
            if (old.view && !before.has(id)) appeared.set(id, (appeared.get(id) ?? 0) + 1)
            removed.delete(id)
          }
          return { view: v, error: null, removed, appeared }
        })
      },
      (e) => {
        if (n !== seq.current) return
        setSt((old) => ({ ...old, error: e instanceof Error ? e.message : String(e) }))
      },
    )
  }, [client, provider, target])

  useEffect(() => {
    const requests = seq
    load()
    const off = hub.subscribeKinds((p) => {
      if (p) {
        if (p.provider !== provider || p.target !== target) return
        const k = known.current
        // An announcement of what was already listed: nothing new.
        if (k && p.session === k.session && typeof p.rev === 'number' && p.rev <= k.rev) return
      }
      load()
    })
    return () => {
      off()
      requests.current++ // answers in flight are for a page that is gone
    }
  }, [hub, load, provider, target])

  const refresh = useCallback(() => {
    void client.refreshKinds(provider, target).catch(() => {})
    load()
  }, [client, load, provider, target])

  return { view: st.view, error: st.error, removed: st.removed, appeared: st.appeared, refresh }
}
