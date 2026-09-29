import { create } from 'zustand'
import type { Client } from './api/client'
import type { AppInfo, Target, TargetRef, TargetsView } from './api/types'

export interface State {
  info: AppInfo | null
  view: TargetsView | null
  loadError: string | null
  actionError: string | null
  filter: string
  /** Keyboard cursor in the filtered list (target key), separate from the selection. */
  cursor: string | null
}

export const initialState: State = {
  info: null,
  view: null,
  loadError: null,
  actionError: null,
  filter: '',
  cursor: null,
}

export const useStore = create<State>(() => ({ ...initialState }))

export const targetKey = (r: TargetRef) => `${r.provider}/${r.id}`

export function matchesFilter(t: Target, filter: string): boolean {
  const f = filter.trim().toLowerCase()
  if (!f) return true
  return [t.title, t.subtitle ?? ''].some((s) => s.toLowerCase().includes(f))
}

/** Targets in display order after the filter, across all groups. */
export function visibleTargets(view: TargetsView | null, filter: string): Target[] {
  if (!view) return []
  return view.groups.flatMap((g) => g.targets.filter((t) => matchesFilter(t, filter)))
}

export function selectedTarget(view: TargetsView | null): Target | null {
  const sel = view?.selected
  if (!view || !sel) return null
  for (const g of view.groups) {
    const t = g.targets.find((x) => x.provider === sel.provider && x.id === sel.id)
    if (t) return t
  }
  return null
}

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

/**
 * Actions bound to a client. Reloads are sequenced: a slow older response
 * never overwrites a newer one, and at most one extra reload is queued.
 */
export function actions(client: Client) {
  let inFlight = false
  let again = false
  // Selection writes are serialized, collapsing to the latest choice: two
  // quick clicks must not persist A after B because A's request was slower.
  let selecting = false
  let nextSelect: TargetRef | null = null

  async function reload() {
    if (inFlight) {
      again = true
      return
    }
    inFlight = true
    try {
      do {
        again = false
        try {
          const view = await client.listTargets()
          useStore.setState((s) => ({ view, loadError: null, cursor: s.cursor ?? (view.selected ? targetKey(view.selected) : null) }))
        } catch (e) {
          useStore.setState({ loadError: errText(e) })
        }
      } while (again)
    } finally {
      inFlight = false
    }
  }

  return {
    async init() {
      try {
        useStore.setState({ info: await client.appInfo() })
      } catch (e) {
        useStore.setState({ loadError: errText(e) })
      }
      await reload()
    },
    reload,
    async select(ref: TargetRef) {
      // Optimistic: the old target's pages unmount now instead of reacting
      // to their session being closed (and reopening it) meanwhile.
      useStore.setState((st) => ({
        cursor: targetKey(ref),
        actionError: null,
        view: st.view ? { ...st.view, selected: { provider: ref.provider, id: ref.id } } : st.view,
      }))
      nextSelect = { provider: ref.provider, id: ref.id }
      if (selecting) return
      selecting = true
      try {
        while (nextSelect) {
          const r = nextSelect
          nextSelect = null
          try {
            await client.selectTarget(r.provider, r.id)
          } catch (e) {
            useStore.setState({ actionError: errText(e) })
          }
        }
      } finally {
        selecting = false
      }
      await reload()
    },
    setFilter(filter: string) {
      useStore.setState((s) => {
        const visible = visibleTargets(s.view, filter)
        const keep = visible.some((t) => targetKey(t) === s.cursor)
        return { filter, cursor: keep ? s.cursor : visible[0] ? targetKey(visible[0]) : null }
      })
    },
    moveCursor(delta: number) {
      useStore.setState((s) => {
        const visible = visibleTargets(s.view, s.filter)
        if (!visible.length) return {}
        const i = visible.findIndex((t) => targetKey(t) === s.cursor)
        const next = i < 0 ? (delta > 0 ? 0 : visible.length - 1) : Math.min(visible.length - 1, Math.max(0, i + delta))
        return { cursor: targetKey(visible[next]) }
      })
    },
    dismissError() {
      useStore.setState({ actionError: null })
    },
  }
}

export type Actions = ReturnType<typeof actions>
