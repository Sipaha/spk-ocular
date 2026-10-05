import { create } from 'zustand'
import type { Client } from './api/client'
import { mayLeave } from './edit/guard'
import type { AppInfo, FavoriteKind, Target, TargetRef, TargetsView } from './api/types'
import { t } from './i18n'

export interface State {
  info: AppInfo | null
  view: TargetsView | null
  loadError: string | null
  actionError: string | null
  /** A short status message ("deployment web: restart requested"). */
  notice: string | null
  filter: string
  resourceFilter: string
  /** Keyboard cursor in the filtered list (target key), separate from the selection. */
  cursor: string | null
  favoriteKinds: FavoriteKind[]
  favoritesReady: boolean
  navSections: Record<string, boolean>
  navSectionsReady: boolean
}

export const initialState: State = {
  info: null,
  view: null,
  loadError: null,
  actionError: null,
  notice: null,
  filter: '',
  resourceFilter: '',
  cursor: null,
  favoriteKinds: [],
  favoritesReady: false,
  navSections: {},
  navSectionsReady: false,
}

export const useStore = create<State>(() => ({ ...initialState }))

let noticeTimer: ReturnType<typeof setTimeout> | undefined

/** Shows msg in the status bar for a few seconds. */
export function showNotice(msg: string, ms = 5000) {
  clearTimeout(noticeTimer)
  useStore.setState({ notice: msg })
  noticeTimer = setTimeout(() => useStore.setState({ notice: null }), ms)
}

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

/**
 * A live resource (terminal, tunnel) opened with configuration revision rev
 * whose target now has another one: it still talks to the old configuration.
 */
export function reconfigured(view: TargetsView | null, provider: string, id: string, rev: string | undefined): boolean {
  if (!view || !rev) return false
  for (const g of view.groups) {
    const t = g.targets.find((x) => x.provider === provider && x.id === id)
    if (t) return !!t.configRev && t.configRev !== rev
  }
  return false
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
  let favoritesQueue = Promise.resolve()
  let confirmedFavorites: FavoriteKind[] | null = null
  type FavoriteChange = { apply: (items: FavoriteKind[]) => FavoriteKind[]; write: () => Promise<void> }
  const favoritePending: FavoriteChange[] = []
  const changeFavorites = (change: FavoriteChange) => {
    if (!useStore.getState().favoritesReady) return
    confirmedFavorites ??= useStore.getState().favoriteKinds.slice()
    favoritePending.push(change)
    useStore.setState((s) => ({ favoriteKinds: change.apply(s.favoriteKinds) }))
    favoritesQueue = favoritesQueue.then(async () => {
      try {
        await change.write()
        confirmedFavorites = change.apply(confirmedFavorites!)
      } catch { showNotice(t('nav.favoritesSaveFailed')) }
      favoritePending.shift()
      useStore.setState({ favoriteKinds: favoritePending.reduce((items, op) => op.apply(items), confirmedFavorites!) })
    })
    return favoritesQueue
  }
  let favoritesLoad: Promise<void> | null = null
  let navSectionsLoad: Promise<void> | null = null
  let navQueue = Promise.resolve()
  let confirmedSections: Record<string, boolean> | null = null
  const navPending: { key: string; open: boolean }[] = []

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

  async function select(ref: TargetRef) {
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
  }

  return {
    async init() {
      try {
        useStore.setState({ info: await client.appInfo() })
      } catch (e) {
        useStore.setState({ loadError: errText(e) })
      }
      if (!useStore.getState().favoritesReady) {
        favoritesLoad ??= client.getFavoriteKinds().then(
          (favoriteKinds) => { useStore.setState({ favoriteKinds, favoritesReady: true }) },
          () => { showNotice(t('nav.favoritesLoadFailed')) },
        ).finally(() => { favoritesLoad = null })
        await favoritesLoad
      }
      if (!useStore.getState().navSectionsReady) {
        navSectionsLoad ??= client.getNavSections().then(
          (navSections) => { useStore.setState({ navSections, navSectionsReady: true }) },
          () => { showNotice(t('nav.sectionsLoadFailed')) },
        ).finally(() => { navSectionsLoad = null })
        await navSectionsLoad
      }
      await reload()
    },
    setNavSection(key: string, open: boolean) {
      if (!useStore.getState().navSectionsReady) return
      confirmedSections ??= { ...useStore.getState().navSections }
      navPending.push({ key, open })
      useStore.setState((s) => ({ navSections: { ...s.navSections, [key]: open } }))
      navQueue = navQueue.then(async () => {
        try {
          await client.setNavSection(key, open)
          confirmedSections = { ...confirmedSections, [key]: open }
        } catch { showNotice(t('nav.sectionsSaveFailed')) }
        navPending.shift()
        useStore.setState({ navSections: navPending.reduce((sections, op) => ({ ...sections, [op.key]: op.open }), confirmedSections!) })
      })
      return navQueue
    },
    setKindFavorite(provider: string, kind: string, favorite: boolean) {
      const matches = (f: FavoriteKind) => f.provider === provider && f.kind === kind
      return changeFavorites({
        apply: (items) => favorite ? items.some(matches) ? items : [...items, { provider, kind }] : items.filter((f) => !matches(f)),
        write: () => client.setKindFavorite(provider, kind, favorite),
      })
    },
    moveFavoriteKind(provider: string, kind: string, before: string) {
      return changeFavorites({
        apply: (items) => {
          const item = items.find((f) => f.provider === provider && f.kind === kind)
          if (!item || before === kind) return items
          const next = items.filter((f) => f !== item)
          const index = before ? next.findIndex((f) => f.provider === provider && f.kind === before) : -1
          next.splice(index < 0 ? next.length : index, 0, item)
          return next
        },
        write: () => client.moveFavoriteKind(provider, kind, before),
      })
    },
    reload,
    // Another target drops the open editor's edits: asked first (edit/guard).
    select(ref: TargetRef): Promise<void> {
      const now = useStore.getState().view?.selected
      if (now?.provider === ref.provider && now.id === ref.id) return select(ref)
      return new Promise((resolve) => mayLeave(() => resolve(select(ref)), resolve))
    },
    // Closing a session is not a selection: no edit guard. The server's
    // targets_changed lands with the reload.
    async closeTarget(ref: TargetRef) {
      try {
        await client.closeTarget(ref.provider, ref.id)
      } catch (e) {
        useStore.setState({ actionError: errText(e) })
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
