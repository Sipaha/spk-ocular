import { lazy, Suspense } from 'react'
const RbacWorkspace = lazy(() => import('../rbac/RbacWorkspace'))
const TimelineWorkspace = lazy(() => import('../timeline/TimelineWorkspace'))
const GraphWorkspace = lazy(() => import('../graph/GraphWorkspace'))
import { groupLabel, kindLabel } from '../presentation'
import { HelmWorkspace } from '../helm/HelmWorkspace'
import { agents } from '../agents/store'
import { useInstancePicker, runningChannel } from './useInstancePicker'
import { PanelResize, usePanelWidths } from './PanelResize'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { ActionDescriptor, KindDescriptor, KindsView, MetricsView, Ref, Row, ScopeSel, ScopesView, SourceCoverage, Target } from '../api/types'
import { ApiError } from '../api/client'
import { actionLabel, classLabel, getLanguage, t } from '../i18n'
import { showNotice, targetKey, useStore } from '../store'
import { memoOf, pageMemoKey, remember as rememberPage, rememberSort, seedPersisted } from './pageMemo'
import { parseMemo, useMemoPersist } from './pageMemoPersist'
import { persistNavigation, persistTargetEntries } from './navigationPersist'
import { columnWidthsKey, parseColumnWidths } from './columnWidths'
import { useScopeWords } from '../scopeNames'
import { refTitle } from '../refs'
import { ActionDialog, type ActionRequest } from '../actions/ActionDialog'
import { Menu, type MenuItem } from '../actions/Menu'
import { BulkActionDialog, type BulkItem, type BulkRequest } from '../actions/BulkActionDialog'
import { bulkActions } from '../actions/bulk'
import { ScopeSelect } from './ScopeSelect'
import { createSelectMemory, type SelectMemory } from './Select'
import { parseScope, scopeSet, selectedScopes } from '../scopes'
import { metricsState, useMetrics } from '../views/useMetrics'
import { useView } from '../views/useView'
import { useKinds } from '../views/useKinds'
import type { ViewHub } from '../views/viewSync'
import { ResourceDrawer } from './ResourceDrawer'
import { matchesRow, ResourceTable } from './ResourceTable'
import { SearchIcon, StarIcon, WarningIcon } from './icons'
import { dock } from '../dock/store'
import { editsHeld, mayLeave, useEditHolder } from '../edit/guard'
import { ToolDialog } from './ToolDialog'
import { TerminalDialog } from '../term/TerminalDialog'
import { lend, type PaletteHost } from '../palette/store'

/** A request from the palette to the page: applied once, by seq. */
interface PageReq<T> {
  value: T
  seq: number
}

interface UIState {
  kind: string
  scope: ScopeSel
}

/** Stable global keys, independent of target and interface language. */
const subKey = (group: string, sub: string) => `${group}/${sub}`
const groupKey = (group: string) => `group:${group}`
const favoritesKey = 'favorites'

function parseState(st: Record<string, string>, fallback: UIState): UIState {
  const parse = (v: string | undefined) => {
    try {
      return v ? JSON.parse(v) : undefined
    } catch {
      return undefined
    }
  }
  const kind = parse(st.kind)
  const scope = parse(st.scope)
  return {
    kind: typeof kind === 'string' && kind !== '__overview' ? kind : fallback.kind,
    scope: parseScope(scope) ?? fallback.scope,
  }
}

/** The selected target: kind navigation + the current table. */
export function Workspace({ client, hub, target, onFavorite, onMoveFavorite, onNavSection }: {
  client: Client; hub: ViewHub; target: Target
  onFavorite: (provider: string, kind: string, favorite: boolean) => void
  onMoveFavorite: (provider: string, kind: string, before: string) => void
  onNavSection: (key: string, open: boolean) => void
}) {
  const navigationWidth = usePanelWidths((s) => s.navigation)
  const favoriteKinds = useStore((s) => s.favoriteKinds)
  const favoritesReady = useStore((s) => s.favoritesReady)
  const navSections = useStore((s) => s.navSections)
  const navSectionsReady = useStore((s) => s.navSectionsReady)
  const resourceFilter = useStore((s) => s.resourceFilter)
  const [dragFavorite, setDragFavorite] = useState<string | null>(null)
  const [favoriteDrop, setFavoriteDrop] = useState<{ id: string; edge: 'before' | 'after' } | null>(null)
  const favorites = useMemo(() => new Set(favoriteKinds.filter((f) => f.provider === target.provider).map((f) => f.kind)), [favoriteKinds, target.provider])
  const toggleFavorite = (id: string) => onFavorite(target.provider, id, !favorites.has(id))
  const catalog = useKinds(client, hub, target.provider, target.id)
  const kinds = catalog.view?.kinds ?? null
  const kindsError = catalog.error
  const [scopes, setScopes] = useState<ScopesView | null>(null)
  const [scopeMenu] = useState(createSelectMemory)
  // The target as it was left in this run (P18): shown at once, before
  // target_state answers.
  const tkey = targetKey({ provider: target.provider, id: target.id })
  const searching = !!resourceFilter.trim()
  const sectionOpen = (key: string, fallback: boolean) => searching || (navSections[key] ?? fallback)
  const favoritesOpen = sectionOpen(favoritesKey, false)
  const defaultScope = target.defaultScope
  // Last kind and scope per target (SQLite target_state); null until loaded,
  // so the default view is not opened only to be replaced.
  const [ui, setUI] = useState<UIState | null>(() => memoOf(tkey).ui ?? null)
  // A remembered kind the target no longer serves (a CRD removed while the
  // target was left) is not forced: its default kind instead (P18).
  const [checkLeft, setCheckLeft] = useState(() => !!memoOf(tkey).ui)
  if (checkLeft && ui && kinds && catalog.view?.state !== 'discovering') {
    setCheckLeft(false)
    if (ui.kind && !kinds.some((k) => k.id === ui.kind)) {
      const next = { ...ui, kind: '' }
      rememberPage(tkey, { ui: next })
      setUI(next)
    }
  }
  // Per kind, how many times its page was started anew (the page key).
  const [renewed, setRenewed] = useState<ReadonlyMap<string, number>>(new Map())
  const renew = useCallback((k: string) => setRenewed((old) => new Map(old).set(k, (old.get(k) ?? 0) + 1)), [])
  // No remembered kind: the provider's default one (else the first in the navigation).
  const defaultKind = kinds?.find((k) => k.default && !k.hidden)?.id ?? kinds?.find((k) => !k.hidden)?.id ?? ''
  // Old installs may have left the removed Overview page selected.
  const kind = ui?.kind && ui.kind !== '__overview' ? ui.kind : defaultKind
  const selectedScope = ui?.scope ?? null
  const scope: ScopeSel = selectedScope ?? { mode: 'all' }
  // P19: the page snapshot (filter/sort/details) is written back to
  // target_state debounced, and survives an app restart.
  useMemoPersist(client, target.provider, target.id, tkey)
  const language = getLanguage()
  const matchingKinds = useMemo(() => {
    const q = resourceFilter.trim().toLowerCase()
    return (kinds ?? []).filter((k) => !k.hidden && (!q || [k.id, k.title, kindLabel(target.provider, k), k.singular, k.group, groupLabel(target.provider, k.group, language), k.subgroup, ...(k.aliases ?? [])].some((name) => name?.toLowerCase().includes(q))))
  }, [kinds, resourceFilter, target.provider, language])
  // A kind lives in exactly one place: Favorites or its original group.
  const groups = useMemo(() => navGroups(matchingKinds.filter((k) => !favorites.has(k.id))), [matchingKinds, favorites])
  const visibleFavorites = useMemo(() => favoriteKinds.filter((f) => f.provider === target.provider)
    .map((f) => matchingKinds.find((k) => k.id === f.kind)).filter((k): k is KindDescriptor => !!k), [favoriteKinds, target.provider, matchingKinds])
  const clearFavoriteDrag = () => { setDragFavorite(null); setFavoriteDrop(null) }
  // A kind a later listing no longer has keeps its page: it says why.
  const current = kinds?.find((k) => k.id === kind) ?? catalog.removed.get(kind)
  // The remembered kind may be a discovered one not listed yet.
  const waiting = !current && !!kind && catalog.view?.state === 'discovering'
  // A new kind or scope is a new page: selection, filter and an open drawer
  // belong to the table they were made in. A renewed one (a kind served
  // again after removed, a view that gave up; below) is a new page too.
  const pageScope = current?.scoped ? scope : { mode: 'none' as const }
  const pageKey = current ? `${current.id}#${renewed.get(current.id) ?? 0}/${JSON.stringify(pageScope)}` : ''
  // The page shown. Whatever changes it on its own (a kind renewed, served
  // again with another scope) waits while the open editor holds edits: a new
  // page drops the editor, and nobody asked to leave. The user's navigation
  // asks first (mayLeave), so it is never held here.
  const holder = useEditHolder()
  const live = current ? { key: pageKey, kind: current, scope: pageScope } : null
  const [shown, setShown] = useState(live)
  const keep = !!shown && shown.key !== live?.key && editsHeld()
  if (!keep && (shown?.key !== live?.key || shown?.kind !== live?.kind)) setShown(live)
  const page = keep ? shown : live
  // The page (by key) whose view gave up (ViewState.halted).
  const [halted, setHalted] = useState<string | null>(null)
  const onHalted = useCallback((key: string, h: boolean) => setHalted((old) => (h ? key : old === key ? null : old)), [])
  const remember = (next: UIState) => {
    // Navigation drops the palette's requests: they were for the page it opened.
    setFilterReq(null)
    setOpenReq(null)
    setUI(next)
    rememberPage(tkey, { ui: next })
    void persistNavigation(client, target.provider, target.id, next).catch(() => showNotice(t('scope.notSaved')))
  }
  // Asked for again while its view gave up: a new page, if the kind is served.
  const again = (k: string) => {
    if (k === kind && halted === pageKey && kinds?.some((d) => d.id === k)) renew(k)
  }
  // Another page drops the open editor's edits: asked first (edit/guard).
  const newPage = (k: string) => k !== kind || halted === pageKey
  const leaveFor = (k: string, go: () => void) => (newPage(k) ? mayLeave(go) : go())
  const setKind = (k: string) =>
    leaveFor(k, () => {
      again(k)
      remember({ kind: k, scope })
    })
  const setScope = (s: ScopeSel) => (JSON.stringify(s) === JSON.stringify(scope) ? remember({ kind, scope: s }) : mayLeave(() => remember({ kind, scope: s })))
  // Palette requests to the page (a filter for its table, an object to open);
  // the page applies each once (by seq).
  const [filterReq, setFilterReq] = useState<PageReq<string> | null>(null)
  const [openReq, setOpenReq] = useState<PageReq<Ref> | null>(null)
  const reqSeq = useRef(0)

  // Tabs live in the dock above this keyed Workspace and survive target switches.
  const targetRef = useMemo(() => ({ provider: target.provider, id: target.id }), [target.provider, target.id])
  const { pickExec, pickLogs, popup: instancePopup, cancel:cancelPicker } = useInstancePicker(client, `${target.provider}/${target.id}`)
  const [logDialog, setLogDialog] = useState<Ref | null>(null)
  const openLogs = useCallback((ref: Ref, dialog = false) => dialog ? (cancelPicker(false), setLogDialog(ref)) : pickLogs(ref, (selected, channel) => dock.openLogs(targetRef, target.title, selected, channel)), [pickLogs, targetRef, target.title, setLogDialog, cancelPicker])
  const [termDialog, setTermDialog] = useState<Ref | null>(null)
  const openTerminal = useCallback(
    (ref: Ref, dialog: boolean) => (dialog ? (cancelPicker(false), setTermDialog(ref)) : pickExec(ref, instance => dock.openTerminal(targetRef, target.title, { ref, instance:instance.id, channel:runningChannel(instance)?.id }))),
    [pickExec, targetRef, target.title, setTermDialog, cancelPicker],
  )
  const hasLogs = useCallback((kindId: string) => !!kinds?.find((k) => k.id === kindId)?.logs, [kinds])
  const hasExec = useCallback((kindId: string) => !!kinds?.find((k) => k.id === kindId)?.exec, [kinds])
  const hasForward = useCallback((kindId: string) => !!kinds?.find((k) => k.id === kindId)?.forward, [kinds])
  const actionsOf = useCallback((kindId: string) => kinds?.find((k) => k.id === kindId)?.actions ?? [], [kinds])
  const eventsKindOf = useCallback((kindId: string) => kinds?.find((k) => k.id === kindId)?.eventsKind, [kinds])
  const editableOf = useCallback((kindId: string) => !!kinds?.find((k) => k.id === kindId)?.editable, [kinds])
  const valuesOf = useCallback((kindId: string) => !!kinds?.find((k) => k.id === kindId)?.values, [kinds])
  const kindTitleOf = useCallback((kindId: string) => ((k) => k?.singular ?? k?.title ?? kindId)(kinds?.find((k) => k.id === kindId)), [kinds])
  const [actionReq, setActionReq] = useState<(ActionRequest & { seq: number }) | null>(null)
  const actionSeq = useRef(0)
  const openAction = useCallback(
    (ref: Ref, action: ActionDescriptor) =>
      setActionReq({ ref, action, kindTitle: kindTitleOf(ref.kind), seq: ++actionSeq.current }),
    [kindTitleOf, setActionReq],
  )
  // One action on several marked objects; onDone unmarks the rows done.
  const [bulkReq, setBulkReq] = useState<(BulkRequest & { seq: number; onDone: (ids: string[]) => void }) | null>(null)
  const openBulk = useCallback(
    (items: BulkItem[], action: ActionDescriptor, onDone: (ids: string[]) => void) =>
      setBulkReq({ items, action, kindTitleOf, onDone, seq: ++actionSeq.current }),
    [kindTitleOf, setBulkReq],
  )

  useEffect(() => {
    // Workspace is keyed by target: state starts fresh for each one.
    let live = true
    const fallback: UIState = { kind: '', scope: defaultScope ? { mode: 'one', name: defaultScope } : { mode: 'all' } }
    // What this run remembers of the target beats target_state (P18).
    if (!memoOf(tkey).ui)
      client.getTargetState(target.provider, target.id).then(
        (st) => {
          if (!live) return
          const ui = parseState(st, fallback)
          const persisted = parseMemo(st.pageMemo)
          if (persisted) seedPersisted(tkey, persisted)
          setUI(ui)
          rememberPage(tkey, { ui, columnWidths: parseColumnWidths(st) })
        },
        () => live && setUI(fallback),
      )
    client.listScopes(target.provider, target.id).then(
      (s) => live && setScopes(s),
      () => live && setScopes({ scopes: [], error: { code: 'unavailable', detail: '' } }),
    )
    return () => {
      live = false
    }
  }, [client, target.provider, target.id, defaultScope, tkey])

  // A page whose view gave up while a listing serves its kind (a new session
  // that discovered it again, one it opened too early in, a kind deleted and
  // created again between two listings) gets one new page per listing: no
  // polling, no loop. Explicit navigation to it renews it too (setKind).
  // A kind served again (after removed) is of a new appearance: its page is
  // renewed too; other kinds' appearances are only noted (going to one is a
  // new page anyway). Renewals on their own wait while the open editor holds
  // edits (a new page drops it, and nobody asked to leave); they run once it
  // lets go.
  const renewedAt = useRef(new Map<string, string>())
  const seenAppeared = useRef(new Map<string, number>())
  const stamp = catalog.view ? `${catalog.view.session}/${catalog.view.rev}` : ''
  useEffect(() => {
    for (const [id, n] of catalog.appeared) if (id !== current?.id) seenAppeared.current.set(id, n)
    if (!current || !kinds?.some((k) => k.id === current.id) || editsHeld()) return
    const appeared = catalog.appeared.get(current.id) ?? 0
    const cameBack = (seenAppeared.current.get(current.id) ?? 0) !== appeared
    const gaveUp = halted === pageKey && renewedAt.current.get(current.id) !== stamp
    if (!cameBack && !gaveUp) return
    seenAppeared.current.set(current.id, appeared)
    if (gaveUp) renewedAt.current.set(current.id, stamp)
    renew(current.id)
  }, [current, halted, pageKey, kinds, stamp, renew, holder, catalog.appeared])

  // The palette acts through the latest state of this workspace.
  const paletteActs = useRef<Pick<PaletteHost, 'openKind' | 'setScope' | 'openObject'> | null>(null)
  useEffect(() => {
    paletteActs.current = {
      openKind: (k, filter) =>
        leaveFor(k, () => {
          if (k !== kind) {
            again(k)
            remember({ kind: k, scope })
          } else again(k)
          setFilterReq({ value: filter, seq: ++reqSeq.current })
        }),
      setScope,
      openObject: (ref) =>
        mayLeave(() => {
          // Open the object's own table and keep the target's scope selection.
          // A vanished kind can still show its recent object's details in the
          // current table; without one, use the first available host.
          const k = kinds?.some((d) => d.id === ref.kind) ? ref.kind : current ? kind : (kinds?.find((d) => !d.hidden)?.id ?? kind)
          again(k)
          if (k !== kind || !current) remember({ kind: k, scope })
          setOpenReq({ value: ref, seq: ++reqSeq.current })
        }),
    }
  })
  // null: scopes cannot be listed (the palette takes a typed one as is).
  const scopeNames = useMemo(() => (scopes?.error ? null : (scopes?.scopes ?? []).map((s) => s.name)), [scopes])
  useEffect(
    () =>
      lend('host', {
        target: targetRef,
        kinds: kinds ?? [],
        scopes: scopeNames,
        selectedScope,
        openKind: (k, f) => paletteActs.current?.openKind(k, f),
        setScope: (s) => paletteActs.current?.setScope(s),
        openObject: (r) => paletteActs.current?.openObject(r),
      }),
    [targetRef, kinds, scopeNames, selectedScope],
  )

  return (
    <div className="flex min-h-0 flex-1">
      <div className="relative shrink-0" style={{ width: navigationWidth, maxWidth: '25vw' }}>
      <nav aria-label="resources" data-area="nav" onKeyDown={onNavKey} className="resource-nav">
        <label className="resource-nav-filter field-shell">
          <SearchIcon className="h-3.5 w-3.5 shrink-0 text-fg-subtle" />
          <input value={resourceFilter} onChange={(e) => useStore.setState({ resourceFilter: e.target.value })}
            data-resource-filter data-area-focus={resourceFilter ? '' : undefined}
            aria-label={t('nav.filter')} placeholder={t('nav.filter')} spellCheck={false}
            className="min-w-0 w-full bg-transparent text-fg outline-none placeholder:text-fg-subtle"
            onKeyDown={(e) => {
              if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); useStore.setState({ resourceFilter: '' }) }
              else if (e.key === 'ArrowDown' || e.key === 'Enter') {
                e.preventDefault(); e.stopPropagation()
                const item = e.currentTarget.closest('nav')?.querySelector<HTMLButtonElement>('[data-nav-item]:not([aria-expanded])')
                item?.focus()
                if (e.key === 'Enter' && matchingKinds.length === 1) item?.click()
              }
            }} />
        </label>
        <div className="resource-nav-list min-h-0 flex-1 overflow-y-auto pb-3" data-resource-nav-list>
        <section className="mt-3" aria-label={t('nav.favorites')}>
          <NavSectionHeading label={t('nav.favorites')} open={favoritesOpen} count={visibleFavorites.length}
            disabled={!navSectionsReady || searching} onToggle={(o) => onNavSection(favoritesKey, o)} />
          {visibleFavorites.map((k, i) => (favoritesOpen || kind === k.id) && (
            <NavItem key={k.id} active={kind === k.id} onClick={() => setKind(k.id)} label={kindLabel(target.provider, k)} hint={k.subgroup}
              favorite onFavorite={() => toggleFavorite(k.id)} favoriteDisabled={!favoritesReady}
              moveUp={i > 0 ? () => onMoveFavorite(target.provider, k.id, visibleFavorites[i - 1].id) : undefined}
              moveDown={i < visibleFavorites.length - 1 ? () => onMoveFavorite(target.provider, k.id, visibleFavorites[i + 2]?.id ?? '') : undefined}
              drag={{ active: dragFavorite === k.id, edge: favoriteDrop?.id === k.id ? favoriteDrop.edge : undefined,
                onStart: (e) => {
                  if ((e.target as Element).closest('.nav-favorite')) { e.preventDefault(); return }
                  e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', k.id)
                  setDragFavorite(k.id)
                },
                onEnd: clearFavoriteDrag,
                onOver: (e) => {
                  if (!dragFavorite || dragFavorite === k.id) return
                  e.preventDefault(); e.dataTransfer.dropEffect = 'move'
                  const r = e.currentTarget.getBoundingClientRect()
                  const edge = e.clientY < r.top + r.height / 2 ? 'before' : 'after'
                  setFavoriteDrop((old) => old?.id === k.id && old.edge === edge ? old : { id: k.id, edge })
                },
                onDrop: (e) => {
                  if (!dragFavorite) return
                  e.preventDefault(); e.stopPropagation()
                  if (dragFavorite !== k.id) {
                    const r = e.currentTarget.getBoundingClientRect()
                    const rest = visibleFavorites.filter((f) => f.id !== dragFavorite)
                    const before = e.clientY < r.top + r.height / 2 ? k.id : rest[rest.findIndex((f) => f.id === k.id) + 1]?.id ?? ''
                    onMoveFavorite(target.provider, dragFavorite, before)
                  }
                  clearFavoriteDrag()
                },
              }} />
          ))}
          {favoritesOpen && !resourceFilter.trim() && !matchingKinds.some((k) => favorites.has(k.id)) && <p className="px-2 text-[12px] text-fg-subtle">{t(favoritesReady ? 'nav.favoritesHint' : 'nav.favoritesUnavailable')}</p>}
        </section>
        {resourceFilter.trim() && !matchingKinds.length && <p role="status" className="px-2 py-2 text-fg-subtle">{t('nav.noMatches')}</p>}
        {groups.map((g) => {
          const open = sectionOpen(groupKey(g.group), g.group === 'Workloads')
          const activeIn = g.items.flatMap((it) => ('kind' in it ? [it.kind] : it.kinds)).find((k) => k.id === kind)
          return (
            <section key={g.group} className="mt-3" aria-label={groupLabel(target.provider, g.group)}>
              <NavSectionHeading label={groupLabel(target.provider, g.group)} open={open} count={g.count}
                disabled={!navSectionsReady || searching} onToggle={(o) => onNavSection(groupKey(g.group), o)} />
              {!open && activeIn && !favorites.has(activeIn.id) && <NavItem active onClick={() => setKind(activeIn.id)} label={kindLabel(target.provider, activeIn)} hint={activeIn.subgroup}
                favorite={false} onFavorite={() => toggleFavorite(activeIn.id)} favoriteDisabled={!favoritesReady} />}
              {open &&
                g.items.map((it) =>
                  'kind' in it ? (
                    <NavItem key={it.kind.id} active={kind === it.kind.id && !favorites.has(it.kind.id)} onClick={() => setKind(it.kind.id)} label={kindLabel(target.provider, it.kind)} hint={it.kind.subgroup}
                      favorite={favorites.has(it.kind.id)} onFavorite={() => toggleFavorite(it.kind.id)} favoriteDisabled={!favoritesReady} />
                  ) : (
                    <NavSubgroup
                      key={it.sub}
                      label={it.sub}
                      kinds={it.kinds}
                      open={sectionOpen(subKey(g.group, it.sub), false)}
                      disabled={!navSectionsReady || searching}
                      onToggle={(o) => onNavSection(subKey(g.group, it.sub), o)}
                      active={kind}
                      onPick={setKind}
                      favorites={favorites}
                      onFavorite={toggleFavorite}
                      favoritesReady={favoritesReady}
                    />
                  ),
                )}
            </section>
          )
        })}
        <CatalogNote view={catalog.view} />
        {kindsError && <p className="mt-3 px-2 text-xs text-danger">{kindsError}</p>}
        <button
          data-refresh-kinds
          tabIndex={-1}
          onClick={catalog.refresh}
          title={t('nav.refreshHint')}
          className="mt-3 w-full truncate rounded-md px-2 py-1 text-left text-xs text-fg-subtle hover:bg-hover hover:text-fg"
        >
          {t('nav.refresh')} <span className="opacity-70">F5</span>
        </button>
        </div>
      </nav>
      <PanelResize label={t('panels.navigation')} value={navigationWidth} min={140} max={() => Math.min(360, window.innerWidth * 0.25)} onDone={(navigation) => usePanelWidths.setState({ navigation })} />
      </div>
      <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <div className="flex min-h-0 flex-1 flex-col">
        {!ui || waiting || (!kinds && !kindsError) ? (
          <LoadingState />
        ) : !page ? (
          <div role={kindsError ? 'alert' : 'status'} className="p-4 text-fg-muted">
            {kindsError || t('nav.noKinds')}
          </div>
        ) : page.kind.workspace === 'rbac' ? (
          <Suspense fallback={<LoadingState />}><RbacWorkspace key={page.key} client={client} target={target} scope={scope}
            scopePicker={<ScopePicker scope={scope} scopes={scopes} scopeMenu={scopeMenu} onScope={setScope} />}
            drawer={{client,hub,target,hasLogs,onLogs:openLogs,hasExec,onTerminal:openTerminal,hasForward,actionsOf,onAction:openAction,eventsKindOf,editableOf,valuesOf,kindTitleOf}}
          /></Suspense>
        ) : page.kind.workspace === 'timeline' ? (
          <Suspense fallback={<LoadingState />}><TimelineWorkspace key={page.key} client={client} target={target} scope={scope}
            scopePicker={<ScopePicker scope={scope} scopes={scopes} scopeMenu={scopeMenu} onScope={setScope} />}
            drawer={{client,hub,target,hasLogs,onLogs:openLogs,hasExec,onTerminal:openTerminal,hasForward,actionsOf,onAction:openAction,eventsKindOf,editableOf,valuesOf,kindTitleOf}}
          /></Suspense>
        ) : page.kind.workspace === 'graph' ? (
          <Suspense fallback={<LoadingState />}><GraphWorkspace key={page.key} client={client} target={target} scope={scope}
            scopePicker={<ScopePicker scope={scope} scopes={scopes} scopeMenu={scopeMenu} onScope={setScope} />}
            drawer={{client,hub,target,hasLogs,onLogs:openLogs,hasExec,onTerminal:openTerminal,hasForward,actionsOf,onAction:openAction,eventsKindOf,editableOf,valuesOf,kindTitleOf}}
          /></Suspense>
        ) : page.kind.workspace?.startsWith('helm-') ? (
          <HelmWorkspace key={page.key} client={client} target={target} scope={scope} onResource={ref => paletteActs.current?.openObject(ref)} onSection={tab => setKind(`ocular.helm.${tab}`)} initialTab={page.kind.workspace === 'helm-charts' ? 'charts' : 'releases'} scopePicker={<ScopePicker scope={scope} scopes={scopes} scopeMenu={scopeMenu} onScope={setScope} />} />
        ) : (
          <ResourcePage
            key={page.key}
            pageKey={page.key}
            onHalted={onHalted}
            client={client}
            hub={hub}
            target={target}
            kind={page.kind}
            scope={page.scope}
            scopes={scopes}
            scopeMenu={scopeMenu}
            onScope={setScope}
            hasLogs={hasLogs}
            onLogs={openLogs}
            hasExec={hasExec}
            onTerminal={openTerminal}
            hasForward={hasForward}
            actionsOf={actionsOf}
            onAction={openAction}
            onBulk={openBulk}
            eventsKindOf={eventsKindOf}
            editableOf={editableOf}
            valuesOf={valuesOf}
            kindTitleOf={kindTitleOf}
            filterReq={filterReq}
            openReq={openReq}
            memoKey={tkey}
          />
        )}
        </div>
        {instancePopup}
        {logDialog && <ToolDialog client={client} subject={logDialog} mode="logs" onClose={()=>setLogDialog(null)} onOpen={selection=>{setLogDialog(null);dock.openLogs(targetRef,target.title,selection.ref,selection.channel)}}/>}
        {termDialog && (
          <TerminalDialog
            client={client}
            subject={termDialog}
            onClose={() => setTermDialog(null)}
            onOpen={(open) => {
              setTermDialog(null)
              dock.openTerminal(targetRef, target.title, open)
            }}
          />
        )}
        {bulkReq && (
          <BulkActionDialog
            key={bulkReq.seq}
            client={client}
            req={bulkReq}
            onClose={() => setBulkReq(null)}
            onDone={bulkReq.onDone}
          />
        )}
        {actionReq && (
          <ActionDialog
            // A new request is a new dialog: nothing of the last one carries over.
            key={actionReq.seq}
            client={client}
            req={actionReq}
            onClose={() => setActionReq(null)}
            onTerminal={(open) =>
              dock.openTerminal(targetRef, target.title, { ref: open.ref, instance: open.instance, channel: open.channel, attach: open.attach }, open.channel ? `${open.channel} · ${refTitle(open.ref)}` : undefined)
            }
          />
        )}
      </main>
    </div>
  )
}

/** Views by arrows, Home and End (one tab stop: the current view); Enter opens. */
function onNavKey(e: React.KeyboardEvent<HTMLElement>) {
  if (e.target instanceof HTMLInputElement) return
  const items = [...e.currentTarget.querySelectorAll<HTMLElement>('[data-nav-item]')]
  const i = items.indexOf(document.activeElement as HTMLElement)
  const to = e.key === 'ArrowDown' ? i + 1 : e.key === 'ArrowUp' ? i - 1 : e.key === 'Home' ? 0 : e.key === 'End' ? items.length - 1 : null
  if (to === null || e.altKey || e.ctrlKey || e.metaKey || !items.length) return
  e.preventDefault()
  items[Math.max(0, Math.min(items.length - 1, to))].focus()
}

type NavEntry = { kind: KindDescriptor } | { sub: string; kinds: KindDescriptor[] }

interface NavGroup {
  group: string
  items: NavEntry[]
  /** The group has subgroups (its header counts its kinds). */
  subgrouped: boolean
  count: number
}

/** The navigation in the provider's order: groups, and inside a group its
 * subgroups (a subgroup of one kind is that kind, without a level). */
export function navGroups(kinds: KindDescriptor[]): NavGroup[] {
  const out: NavGroup[] = []
  const byGroup = new Map<string, NavGroup>()
  const bySub = new Map<string, { sub: string; kinds: KindDescriptor[] }>()
  for (const k of kinds) {
    if (k.hidden) continue
    let g = byGroup.get(k.group)
    if (!g) {
      g = { group: k.group, items: [], subgrouped: false, count: 0 }
      byGroup.set(k.group, g)
      out.push(g)
    }
    g.count++
    if (!k.subgroup) {
      g.items.push({ kind: k })
      continue
    }
    g.subgrouped = true
    const key = subKey(k.group, k.subgroup)
    let s = bySub.get(key)
    if (!s) {
      s = { sub: k.subgroup, kinds: [] }
      bySub.set(key, s)
      g.items.push(s)
    }
    s.kinds.push(k)
  }
  for (const g of out) g.items = g.items.map((it) => ('sub' in it && it.kinds.length === 1 ? { kind: it.kinds[0] } : it))
  return out
}

/** Every section, including Favorites, has the same keyboard/mouse control. */
function NavSectionHeading({ label, open, count, disabled, onToggle }: {
  label: string; open: boolean; count: number; disabled: boolean; onToggle: (open: boolean) => void
}) {
  return <button type="button" data-nav-item tabIndex={-1} aria-label={`${label} (${count})`} aria-expanded={open} aria-disabled={disabled}
    onClick={() => { if (!disabled) onToggle(!open) }}
    onKeyDown={(e) => {
      if (!disabled && ((e.key === 'ArrowRight' && !open) || (e.key === 'ArrowLeft' && open))) {
        e.preventDefault()
        onToggle(!open)
      }
    }}
    className="nav-group-heading w-full items-center gap-0.5 rounded-md text-left hover:text-fg">
    <span aria-hidden className={['inline-block w-2 shrink-0 text-[9px] transition-transform', open ? 'rotate-90' : ''].join(' ')}>▶</span>
    <span className="min-w-0 flex-1 truncate">{label}</span>
    <span className="font-normal" title={t('nav.kinds', { count })}>{count}</span>
  </button>
}

function NavItem({ active, onClick, label, hint, nested, favorite, onFavorite, favoriteDisabled, moveUp, moveDown, drag }: {
  active: boolean; onClick: () => void; label: string; hint?: string; nested?: boolean
  favorite?: boolean; onFavorite?: () => void; favoriteDisabled?: boolean
  moveUp?: () => void; moveDown?: () => void
  drag?: { active: boolean; edge?: 'before' | 'after'; onStart: (e: React.DragEvent<HTMLDivElement>) => void; onEnd: () => void; onOver: (e: React.DragEvent<HTMLDivElement>) => void; onDrop: (e: React.DragEvent<HTMLDivElement>) => void }
}) {
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null)
  const favoriteLabel = t(favorite ? 'nav.favoriteRemove' : 'nav.favoriteAdd', { kind: label })
  // A view opened elsewhere (the palette, a remembered one) is shown in the navigation.
  const ref = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: 'nearest' })
  }, [active])
  return (
    <div className="resource-nav-row group/nav relative" draggable={drag ? true : undefined}
      data-favorite-dragging={drag?.active || undefined} data-favorite-drop={drag?.edge}
      onDragStart={drag?.onStart} onDragEnd={drag?.onEnd} onDragOver={drag?.onOver} onDrop={drag?.onDrop}>
    <button
      ref={ref}
      onClick={onClick}
      aria-keyshortcuts={onFavorite ? 'Shift+F10' : undefined}
      onContextMenu={onFavorite && !favoriteDisabled ? (e) => {
        e.preventDefault()
        e.currentTarget.focus()
        setMenu({ x: e.clientX, y: e.clientY })
      } : undefined}
      onKeyDown={onFavorite && !favoriteDisabled ? (e) => {
        if ((e.shiftKey && e.key === 'F10') || e.key === 'ContextMenu') {
          e.preventDefault(); e.stopPropagation()
          const r = e.currentTarget.getBoundingClientRect()
          setMenu({ x: r.left, y: r.bottom })
        }
      } : undefined}
      data-nav-item
      tabIndex={active ? 0 : -1}
      data-area-focus={active ? '' : undefined}
      aria-current={active ? 'page' : undefined}
      title={hint ? `${label} · ${hint}` : label}
      className={['nav-item block w-full truncate rounded-md py-1 text-left', nested ? 'pr-2 pl-5' : 'px-2', active ? 'text-fg' : 'text-fg-muted hover:bg-hover hover:text-fg', onFavorite ? 'pr-8' : ''].join(' ')}
    >
      {label}
    </button>
    {onFavorite && <button type="button" tabIndex={-1} aria-label={favoriteLabel} title={favoriteLabel} aria-pressed={!!favorite}
      disabled={favoriteDisabled} onClick={onFavorite}
      className={`nav-favorite absolute right-1 top-1/2 -translate-y-1/2 rounded p-1 hover:bg-hover disabled:opacity-30 ${favorite ? 'text-accent' : 'text-fg-subtle opacity-0 group-hover/nav:opacity-100 group-focus-within/nav:opacity-100'}`}>
      <StarIcon className="h-3.5 w-3.5" filled={favorite} />
    </button>}
    {menu && onFavorite && <Menu label={t('nav.resourceMenu')} at={menu} onClose={() => setMenu(null)}
      items={[
        { id: 'favorite', label: favoriteLabel, onSelect: onFavorite },
        ...(moveUp ? [{ id: 'up', label: t('nav.favoriteUp'), onSelect: moveUp }] : []),
        ...(moveDown ? [{ id: 'down', label: t('nav.favoriteDown'), onSelect: moveDown }] : []),
      ]} />}
    </div>
  )
}

/** A collapsible level of the navigation; collapsed, it still shows the
 * open kind (so the page's place is never hidden). → opens, ← closes. */
function NavSubgroup(props: {
  label: string
  kinds: KindDescriptor[]
  open: boolean
  disabled: boolean
  onToggle: (open: boolean) => void
  active: string
  onPick: (kind: string) => void
  favorites: ReadonlySet<string>
  onFavorite: (kind: string) => void
  favoritesReady: boolean
}) {
  const { label, kinds, open, disabled, onToggle, active, onPick, favorites, onFavorite, favoritesReady } = props
  const shown = open ? kinds : kinds.filter((k) => k.id === active)
  return (
    <div role="group" aria-label={label}>
      <button
        data-nav-item
        tabIndex={-1}
        aria-expanded={open}
        aria-disabled={disabled}
        title={label}
        onClick={() => { if (!disabled) onToggle(!open) }}
        onKeyDown={(e) => {
          if (!disabled && ((e.key === 'ArrowRight' && !open) || (e.key === 'ArrowLeft' && open))) {
            e.preventDefault()
            onToggle(!open)
          }
        }}
        className="flex w-full items-center gap-1 rounded-md px-2 py-1 text-left text-fg-muted hover:bg-hover hover:text-fg"
      >
        <span aria-hidden className={['inline-block w-2.5 shrink-0 text-[10px] transition-transform', open ? 'rotate-90' : ''].join(' ')}>
          ▶
        </span>
        <span className="min-w-0 flex-1 truncate">{label}</span>
        <span className="shrink-0 text-[12px] text-fg-subtle">{kinds.length}</span>
      </button>
      {shown.map((k) => (
        <NavItem key={k.id} nested active={active === k.id && !favorites.has(k.id)} onClick={() => onPick(k.id)} label={k.title} hint={label}
          favorite={favorites.has(k.id)} onFavorite={() => onFavorite(k.id)} favoriteDisabled={!favoritesReady} />
      ))}
    </div>
  )
}

/** What the catalog cannot vouch for now: still discovering, groups that
 * did not answer (their last known kinds kept), or no discovery at all. */
function CatalogNote({ view }: { view: KindsView | null }) {
  if (!view || view.state === 'ready') return null
  const text =
    view.state === 'discovering'
      ? t('nav.discovering')
      : view.state === 'failed'
        ? t('nav.discoveryFailed')
        : t('nav.unconfirmed', { groups: (view.unconfirmed ?? []).map((g) => (g === '*' ? t('nav.allGroups') : g)).join(', ') })
  return (
    <p role="note" aria-label="catalog" title={view.state === 'partial' ? t('nav.unconfirmedHint') : undefined} className={['mt-3 px-2 text-xs', view.state === 'discovering' ? 'text-fg-subtle' : 'text-warning'].join(' ')}>
      {text}
    </p>
  )
}

function ResourcePage(props: {
  pageKey: string
  /** Tells the workspace whether the page's view gave up. */
  onHalted: (key: string, halted: boolean) => void
  client: Client
  hub: ViewHub
  target: Target
  kind: KindDescriptor
  scope: ScopeSel
  scopes: ScopesView | null
  scopeMenu: SelectMemory
  onScope: (s: ScopeSel) => void
  hasLogs: (kindId: string) => boolean
  onLogs: (ref: Ref, dialog?: boolean) => void
  hasExec: (kindId: string) => boolean
  onTerminal: (ref: Ref, dialog: boolean) => void
  hasForward: (kindId: string) => boolean
  actionsOf: (kindId: string) => ActionDescriptor[]
  onAction: (ref: Ref, action: ActionDescriptor) => void
  onBulk: (items: BulkItem[], action: ActionDescriptor, onDone: (ids: string[]) => void) => void
  eventsKindOf: (kindId: string) => string | undefined
  editableOf: (kindId: string) => boolean
  valuesOf: (kindId: string) => boolean
  kindTitleOf: (kindId: string) => string
  filterReq?: PageReq<string> | null
  openReq?: PageReq<Ref> | null
  /** The target's key in pageMemo (P18): this page's part is restored once. */
  memoKey: string
}) {
  const { pageKey, onHalted, client, hub, target, kind, scope, scopes, scopeMenu, onScope, hasLogs, onLogs, hasExec, onTerminal, hasForward, actionsOf, onAction, onBulk, eventsKindOf, editableOf, valuesOf, kindTitleOf, filterReq, openReq, memoKey: tkey } = props
  const scopeKey = JSON.stringify(scope)
  const query = useMemo(() => ({ kind: kind.id, scope: JSON.parse(scopeKey) as ScopeSel }), [kind.id, scopeKey])
  const view = useView(hub, target.provider, target.id, query)
  const [contentShown, setContentShown] = useState(false)
  const initialLoading = !contentShown && view.status.state === 'loading' && view.rows.length === 0
  // Keep the mounted table's sort, widths and focus on subsequent refreshes.
  if (!contentShown && !initialLoading) setContentShown(true)
  useEffect(() => {
    onHalted(pageKey, view.halted)
    return () => onHalted(pageKey, false)
  }, [onHalted, pageKey, view.halted])
  // The page as it was left, when this is that page (its kind and scope).
  const memoKey = pageMemoKey(kind.id, query.scope)
  const [left] = useState(() => ((p) => (p?.key === memoKey ? p : undefined))(memoOf(tkey).page))
  const [filter, setFilter] = useState(filterReq?.value ?? left?.filter ?? '')
  const [selected, setSelected] = useState<string | null>(openReq ? null : left?.selected ?? null)
  const [pendingOpen, setPendingOpen] = useState<Ref | null>(openReq?.value ?? null)
  const [reveal, setReveal] = useState<{ id: string } | null>(null)
  const [open, setOpenNow] = useState<Ref | null>(openReq?.value ?? left?.open ?? null)
  const [drawerTab, setDrawerTab] = useState(left?.tab)
  useEffect(() => {
    rememberPage(tkey, { page: { key: memoKey, filter, selected, open, tab: drawerTab } })
  }, [tkey, memoKey, filter, selected, open, drawerTab])
  const [leftSort] = useState(() => memoOf(tkey).sorts[kind.id])
  const [leftWidths] = useState(() => memoOf(tkey).columnWidths?.[kind.id])
  // Another object's details drop the open editor's edits: asked first.
  const setOpen = (ref: Ref | null) => mayLeave(() => setOpenNow(ref))
  // A palette request applies once, also to a page already open.
  const [applied, setApplied] = useState({ filter: filterReq?.seq ?? 0, open: openReq?.seq ?? 0 })
  if ((filterReq && filterReq.seq !== applied.filter) || (openReq && openReq.seq !== applied.open)) {
    if (filterReq && filterReq.seq !== applied.filter) setFilter(filterReq.value)
    // Asked for already (the palette's openObject): applied as is.
    if (openReq && openReq.seq !== applied.open) {
      setOpenNow(openReq.value)
      setPendingOpen(openReq.value)
      setSelected(null)
    }
    setApplied({ filter: filterReq?.seq ?? applied.filter, open: openReq?.seq ?? applied.open })
  }
  // Details can open before the table loads. Match the full identity and
  // use the row's id (Problems rows do not use the object's UID as id).
  if (pendingOpen && openReq?.seq === applied.open) {
    const row = view.rows.find(({ ref }) =>
      ref.provider === pendingOpen.provider && ref.target === pendingOpen.target &&
      ref.kind === pendingOpen.kind && (ref.scope ?? '') === (pendingOpen.scope ?? '') &&
      ref.name === pendingOpen.name && (!pendingOpen.uid || ref.uid === pendingOpen.uid))
    if (row) {
      setSelected(row.id)
      setReveal({ id: row.id })
      setPendingOpen(null)
      if (filter.trim() && !matchesRow(row, filter.trim())) setFilter('')
    }
  }
  // The palette offers this table's rows.
  useEffect(() => lend('rows', view.rows), [view.rows])
  const columns = view.kind?.columns ?? kind.columns
  const [visibleRows, setVisibleRows] = useState<string[]>([])
  // A sample of a row since changed (a service's last replica stopped) is
  // not shown as current.
  const stateOf = useMemo(() => new Map(view.rows.map((r) => [r.id, metricsState(r)])), [view.rows])
  const visibleStates = visibleRows.map((id) => stateOf.get(id) ?? '')
  const metrics = useMetrics(client, view.viewId, columns.some((c) => c.metric), visibleRows, visibleStates)
  // What a row offers is its object's kind's (a Problems row is a pod, a
  // deployment, an event…), not the table's.
  const deleteOf = (r: Row) => actionsOf(r.ref.kind).find((a) => a.id === 'delete')
  // Marks (for one action on several rows): only rows shown now — a row that
  // goes, or that the filter hides, drops its mark (an action never touches
  // what is not seen).
  const [markedAll, setMarked] = useState<ReadonlySet<string>>(() => new Set())
  const shownRows = useMemo(() => {
    const f = filter.trim()
    return f ? view.rows.filter((r) => matchesRow(r, f)) : view.rows
  }, [view.rows, filter])
  const marked = useMemo(() => {
    const shown = new Set(shownRows.map((r) => r.id))
    const kept = [...markedAll].filter((id) => shown.has(id))
    return kept.length === markedAll.size ? markedAll : new Set(kept)
  }, [markedAll, shownRows])
  if (marked !== markedAll) setMarked(marked) // dropped for good: they do not come back with the filter
  const markedRows = shownRows.filter((r) => marked.has(r.id))
  const unmark = (ids: string[]) => setMarked((m) => new Set([...m].filter((id) => !ids.includes(id))))
  // An action on the marked rows: one row — its own dialog; several — the bulk one.
  const actOnMarked = (rows: Row[], a: ActionDescriptor) => {
    if (rows.length === 1) onAction(rows[0].ref, actionsOf(rows[0].ref.kind).find((x) => x.id === a.id) ?? a)
    else onBulk(rows.map((r) => ({ id: r.id, ref: r.ref })), a, unmark)
  }
  const markedMenu = (): MenuItem[] => {
    const rows = markedRows
    const acts = bulkActions(rows.map((r) => actionsOf(r.ref.kind)))
    const items: MenuItem[] = acts.map((a) => ({ id: `bulk-${a.id}`, label: actionLabel(a) + (a.param ? '…' : ''), danger: a.destructive, onSelect: () => actOnMarked(rows, a) }))
    if (!items.length) items.push({ id: 'bulk-none', label: t('bulk.noCommon'), disabled: true, onSelect: () => {} })
    items.push({ id: 'unmark', label: t('bulk.unmark'), separator: true, onSelect: () => setMarked(new Set()) })
    return items
  }
  const [barMenu, setBarMenu] = useState<{ x: number; y: number } | null>(null)
  // The row's menu, for the row it opens on (its ref as of now); a marked
  // row among several: the marks' menu.
  const rowMenu = (r: Row): MenuItem[] | { label: string; items: MenuItem[] } => {
    if (marked.size >= 2 && marked.has(r.id)) return { label: t('bulk.menu'), items: markedMenu() }
    const items: MenuItem[] = [{ id: 'details', label: t('row.details'), onSelect: () => setOpen(r.ref) }]
    if (hasLogs(r.ref.kind)) items.push({ id: 'logs', label: t('row.logs'), hint: 'L', onSelect: () => onLogs(r.ref) })
    if (hasExec(r.ref.kind)) items.push({ id: 'terminal', label: t('row.terminal'), hint: 'S', onSelect: () => onTerminal(r.ref, false) })
    actionsOf(r.ref.kind).forEach((a, i) =>
      items.push({ id: `action-${a.id}`, label: actionLabel(a) + (a.param ? '…' : ''), danger: a.destructive, separator: i === 0, hint: a.id === 'delete' ? 'Delete' : undefined, onSelect: () => onAction(r.ref, a) }),
    )
    return items
  }

  return (
    <>
      <header className="resource-toolbar">
        <h1 className="resource-title">{kindLabel(target.provider, kind)}</h1>
        <span className="resource-count" aria-label="count">
          {view.status.state === 'loading' && !view.rows.length ? '…' : view.rows.length}
        </span>
        {!initialLoading && view.status.state === 'loading' && <LoadingState title={kindLabel(target.provider, kind)} inline />}
        {marked.size > 0 && (
          <div role="toolbar" aria-label={t('bulk.bar')} className="flex shrink-0 items-center gap-2 whitespace-nowrap rounded-md bg-marked px-2 py-0.5 text-xs">
            <span>{t('bulk.marked', { n: marked.size, total: shownRows.length })}</span>
            <button
              type="button"
              aria-haspopup="menu"
              onClick={(e) => {
                const b = e.currentTarget.getBoundingClientRect()
                setBarMenu({ x: b.left, y: b.bottom + 2 })
              }}
              className="rounded px-1.5 py-0.5 text-accent hover:bg-hover"
            >
              {t('bulk.actions')} <span aria-hidden>▾</span>
            </button>
            <button type="button" onClick={() => setMarked(new Set())} className="rounded px-1.5 py-0.5 text-fg-muted hover:bg-hover">
              {t('bulk.unmark')}
            </button>
          </div>
        )}
        {barMenu && marked.size > 0 && <Menu items={markedMenu()} at={barMenu} label={t('bulk.menu')} onClose={() => setBarMenu(null)} />}
        {kind.scoped && (scopes?.kind && !scopes.error ? (
          <LiveScopePicker hub={hub} target={target} scopeKind={scopes.kind} scope={scope} scopes={scopes} scopeMenu={scopeMenu} onScope={onScope} />
        ) : (
          <ScopePicker scope={scope} scopes={scopes} scopeMenu={scopeMenu} onScope={onScope} />
        ))}
        {kind.scoped && (
          <button
            type="button"
            aria-label={t('agents.scopeAccess')}
            title={t('agents.scopeAccess')}
            disabled={scope.mode !== 'all' && selectedScopes(scope).length === 0}
            className="shrink-0 rounded border border-line px-2 py-1 text-xs text-fg-muted hover:bg-hover disabled:opacity-50"
            onClick={() => agents.openScopes(target.provider, target.id, scope)}
          >
            {t('agents.scopeAccess')}
          </button>
        )}
        {view.resync && <ResyncButton client={client} viewId={view.viewId} />}
        <label className="resource-filter field-shell">
          <SearchIcon className="h-3.5 w-3.5 text-fg-subtle" />
          <input
            data-primary-filter
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.preventDefault() // one Esc, one effect: not also closing details
                if (filter) setFilter('')
                else e.currentTarget.blur()
              } else if (e.key === 'ArrowDown') {
                e.preventDefault()
                document.querySelector<HTMLElement>('[data-table-scroll]')?.focus()
              }
            }}
            placeholder={t('table.filter')}
            aria-label={t('table.filterLabel')}
            className="w-full bg-transparent outline-none placeholder:text-fg-subtle"
            spellCheck={false}
          />
        </label>
      </header>
      {!initialLoading && view.status.state !== 'ready' && <StatusBanner state={view.status.state} cls={view.status.class} message={view.status.message} />}
      {!initialLoading && view.status.coverage && <CoverageNote coverage={view.status.coverage} notCovered={view.kind?.notCovered ?? kind.notCovered} compact={scope.mode === 'some'} loading={view.status.state === 'loading'} />}
      {!initialLoading && metrics && <MetricsNote metrics={metrics} />}
      <div className="relative flex min-h-0 flex-1 flex-col">
        <div data-area="table" aria-busy={view.status.state === 'loading'} className="flex min-h-0 flex-1 flex-col">
        {initialLoading ? <LoadingState title={kindLabel(target.provider, kind)} /> : <ResourceTable
          areaFocus
          emptyMessage={view.status.state === 'ready' ? view.rows.length === 0 ? emptyTableText(view.status.coverage) : t('nav.noMatches') : null}
          columns={columns}
          rows={view.rows}
          hideScope={scope.mode === 'one'}
          filter={filter}
          selected={selected}
          reveal={reveal}
          onSelect={(r: Row) => { setPendingOpen(null); setSelected(r.id) }}
          onOpen={(r: Row) => setOpen(r.ref)}
          onLogs={(r: Row) => hasLogs(r.ref.kind) && onLogs(r.ref)}
          onTerminal={(r: Row, dialog: boolean) => hasExec(r.ref.kind) && onTerminal(r.ref, dialog)}
          metrics={metrics}
          onVisibleRows={setVisibleRows}
          rowMenu={rowMenu}
          onDelete={(r: Row | undefined) => {
            // Marks first: Delete deletes what is marked.
            if (markedRows.length) {
              const del = bulkActions(markedRows.map((m) => actionsOf(m.ref.kind))).find((a) => a.id === 'delete')
              if (del) actOnMarked(markedRows, del)
              else showNotice(t('bulk.noDelete'))
              return
            }
            const del = r && deleteOf(r)
            if (r && del) onAction(r.ref, del)
          }}
          marked={marked}
          onMarked={setMarked}
          defaultSort={view.kind?.sort ?? kind.sort}
          initialSort={leftSort}
          onSort={(s) => rememberSort(tkey, kind.id, s)}
          initialWidths={leftWidths}
          onWidths={(widths) => {
            rememberPage(tkey, { columnWidths: { ...memoOf(tkey).columnWidths, [kind.id]: widths } })
            void persistTargetEntries(client, target.provider, target.id, { [columnWidthsKey(kind.id)]: JSON.stringify(widths) }).catch(() => showNotice(t('table.widthsNotSaved')))
          }}
        />}
        </div>
        {open && (
          <ResourceDrawer
            client={client}
            hub={hub}
            target={{ provider: target.provider, id: target.id }}
            subject={open}
            initialTab={drawerTab}
            onTab={setDrawerTab}
            onClose={() => { setPendingOpen(null); setOpenNow(null) }}
            hasLogs={hasLogs}
            onLogs={onLogs}
            hasExec={hasExec}
            onTerminal={onTerminal}
            hasForward={hasForward}
            actionsOf={actionsOf}
            onAction={onAction}
            eventsKindOf={eventsKindOf}
            editableOf={editableOf}
            valuesOf={valuesOf}
            kindTitleOf={kindTitleOf}
          />
        )}
      </div>
    </>
  )
}

/** Reads the view again (ViewInfo.resync: the target's live updates are best
 * effort); F5 clicks it. One request at a time; the backend coalesces. */
function ResyncButton({ client, viewId }: { client: Client; viewId: string | null }) {
  const [busy, setBusy] = useState(false)
  return (
    <button
      data-resync
      disabled={!viewId || busy}
      title={t('view.resyncHint')}
      onClick={() => {
        if (!viewId || busy) return
        setBusy(true)
        client.resyncView(viewId).then(
          () => setBusy(false),
          (e) => {
            setBusy(false)
            // A view that went away is reopened by its page: nothing to say.
            if (!(e instanceof ApiError && e.code === 'gone')) showNotice(t('view.resyncFailed', { error: e instanceof ApiError ? classLabel(e.code) : String(e) }))
          },
        )
      }}
      className="rounded-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg disabled:opacity-50"
    >
      {t('view.resync')} <span className="text-fg-subtle">F5</span>
    </button>
  )
}

/** The scope list kept live by a view of the provider's scope kind. */
function LiveScopePicker(props: {
  hub: ViewHub
  target: Target
  scopeKind: string
  scope: ScopeSel
  scopes: ScopesView
  scopeMenu: SelectMemory
  onScope: (s: ScopeSel) => void
}) {
  const { hub, target, scopeKind, scope, scopes, scopeMenu, onScope } = props
  const query = useMemo(() => ({ kind: scopeKind, scope: { mode: 'none' as const } }), [scopeKind])
  const view = useView(hub, target.provider, target.id, query)
  const live = useMemo(
    () => (view.status.state === 'ready' || view.status.state === 'stale' ? { scopes: view.rows.map((r) => ({ name: r.ref.name })).sort((a, b) => a.name.localeCompare(b.name)) } : scopes),
    [view.rows, view.status.state, scopes],
  )
  const liveNames = useMemo(() => (live === scopes ? null : live?.scopes.map((s) => s.name)) ?? null, [live, scopes])
  useEffect(() => (liveNames ? lend('liveScopes', liveNames) : undefined), [liveNames])
  return <ScopePicker scope={scope} scopes={live} scopeMenu={scopeMenu} onScope={onScope} />
}

function ScopePicker({ scope, scopes, onScope, scopeMenu }: { scope: ScopeSel; scopes: ScopesView | null; scopeMenu: SelectMemory; onScope: (s: ScopeSel) => void }) {
  const [typed, setTyped] = useState(selectedScopes(scope).join(', '))
  const words = useScopeWords()
  if (scopes?.error) {
    // Listing scopes is forbidden (or failed): explicit names still work, separated by commas.
    return (
      <form
        className="flex items-center gap-1"
        onSubmit={(e) => {
          e.preventDefault()
          onScope(typed.trim() ? scopeSet(typed.split(',').map((n) => n.trim()).filter(Boolean)) : { mode: 'all' })
        }}
      >
        <input
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          placeholder={t('scope.type', { scope: words.singular.toLowerCase() })}
          aria-label={words.singular}
          title={`${t('scope.cannotList', { scopes: words.plural, error: scopes.error.detail || scopes.error.code })}. ${t('scope.typeMany')}`}
          aria-description={t('scope.typeMany')}
          className="w-44 rounded-md border border-line bg-app px-2 py-1 outline-none focus:border-accent"
        />
      </form>
    )
  }
  const names = [...new Set([...(scopes?.scopes ?? []).map((s) => s.name), ...selectedScopes(scope)])].sort()
  const choose = (next: ScopeSel) => {
    onScope(next)
    if (!scopeMenu.read().open) setTimeout(() => {
      if (document.activeElement === document.body || !document.activeElement) document.querySelector<HTMLElement>('[data-table-scroll]')?.focus()
    })
  }
  return <ScopeSelect value={scope} names={names} label={words.singular} allLabel={words.all} onChange={choose} memory={scopeMenu} />
}

function LoadingState({ title, inline = false }: { title?: string; inline?: boolean }) {
  const label = title ? t('table.loading', { kind: title }) : t('app.loading')
  return <div role="status" aria-label={t('app.loading')} title={inline ? label : undefined} className={inline ? 'flex shrink-0 items-center text-fg-muted' : 'flex min-h-0 flex-1 items-center justify-center gap-3 px-4 py-8 text-fg-muted'}>
    <span aria-hidden="true" className={`${inline ? 'h-3.5 w-3.5' : 'h-5 w-5'} shrink-0 animate-spin rounded-full border-2 border-line border-t-accent motion-reduce:animate-none`} />
    <span className={inline ? 'sr-only' : undefined}>{label}</span>
  </div>
}

function emptyTableText(coverage?: SourceCoverage[]) {
  // Empty results refer only to the sources the view could actually observe.
  return !coverage ? t('table.empty') : coverage.every(c => c.state === 'ready') ? t('coverage.noneFound') : t('coverage.noneInObserved')
}

function StatusBanner({ state, cls, message }: { state: string; cls?: string; message?: string }) {
  if (state === 'loading') return null
  const isErr = state === 'error'
  return (
    <div role="alert" className={['mx-4 mt-3 flex items-start gap-2 rounded-md px-3 py-2 text-xs', isErr ? 'bg-danger/10 text-danger' : 'bg-warning/10 text-warning'].join(' ')}>
      <WarningIcon className="mt-px h-3.5 w-3.5 shrink-0" />
      <span>
        <b>{t(isErr ? 'status.error' : 'status.stale')}</b>
        {cls && ` · ${classLabel(cls)}`}
        {message && <span className="block opacity-90">{message}</span>}
      </span>
    </div>
  )
}

/** What a view of several sources looks at and what not (always, quietly),
 * and — apart and more visible — the sources it could not observe now. */
/** Why metric columns are empty, or that they cover only the first rows. */
function MetricsNote({ metrics }: { metrics: MetricsView }) {
  if (metrics.coverage?.length) return <p role="note" aria-label={t('metrics.label')} className="mx-4 my-1 text-xs text-warning"
    title={metrics.coverage.map((c) => `${c.source}: ${c.message ?? c.class}`).join('\n')}>
    {t('metrics.label')}: {t('coverage.notObserved')} — {metrics.coverage.map((c) => `${c.source} (${classLabel(c.class ?? 'unavailable')})`).join(', ')}
  </p>
  if (metrics.status === 'ok' && !metrics.limit) return null
  const failed = metrics.status !== 'ok'
  return (
    <p role="note" aria-label={t('metrics.label')} className="mx-4 my-1 text-xs text-fg-subtle" title={failed ? metrics.message : undefined}>
      {failed ? t('metrics.failed', { reason: classLabel(metrics.status) }) : t('metrics.limited', { n: metrics.limit ?? 0 })}
    </p>
  )
}

function CoverageNote({ coverage, notCovered, compact = false, loading = false }: { coverage: SourceCoverage[]; notCovered?: string[]; compact?: boolean; loading?: boolean }) {
  const missing = coverage.filter((c) => c.state !== 'ready' && (!loading || c.state !== 'loading'))
  if (compact && !missing.length && !notCovered?.length) return null
  const why = (c: SourceCoverage) =>
    c.state === 'denied' ? classLabel(c.class ?? 'forbidden') : c.state === 'error' ? classLabel(c.class ?? 'internal') : t(c.state === 'stale' ? 'coverage.stale' : 'coverage.loading')
  return (
    <div className="mx-4 my-2 space-y-0.5 text-xs">
      {missing.length > 0 && (
        <p role="note" aria-label={t('coverage.notObserved')} className="text-warning" title={missing.map((c) => `${c.source}: ${c.message ?? c.state}`).join('\n')}>
          {t('coverage.notObserved')}: {missing.map((c) => `${c.source} (${why(c)})`).join(', ')}
        </p>
      )}
      {!compact || notCovered?.length ? <p role="note" aria-label={t('coverage.label')} className="text-fg-subtle">
        {t('coverage.checked')}: {coverage.map((c) => c.source).join(', ')}
        {notCovered?.length ? ` · ${t('coverage.notChecked')}: ${notCovered.join(', ')}` : ''}
      </p> : null}
    </div>
  )
}
