import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { ActionDescriptor, KindDescriptor, MetricsView, Ref, Row, ScopeSel, ScopesView, SourceCoverage, Target } from '../api/types'
import { ApiError } from '../api/client'
import { actionLabel, classLabel, t } from '../i18n'
import { showNotice } from '../store'
import { useScopeWords } from '../scopeNames'
import { ActionDialog, type ActionRequest } from '../actions/ActionDialog'
import type { MenuItem } from '../actions/Menu'
import { metricsState, useMetrics } from '../views/useMetrics'
import { useView } from '../views/useView'
import type { ViewHub } from '../views/viewSync'
import { ResourceDrawer } from './ResourceDrawer'
import { ResourceTable } from './ResourceTable'
import { TargetDetails } from './TargetDetails'
import { SearchIcon, WarningIcon } from './icons'
import { dock } from '../dock/store'
import { TerminalDialog } from '../term/TerminalDialog'
import { lend, type PaletteHost } from '../palette/store'

const OVERVIEW = '__overview'

/** A request from the palette to the page: applied once, by seq. */
interface PageReq<T> {
  value: T
  seq: number
}

interface UIState {
  kind: string
  scope: ScopeSel
}

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
    kind: typeof kind === 'string' ? kind : fallback.kind,
    scope: scope && typeof scope.mode === 'string' ? (scope as ScopeSel) : fallback.scope,
  }
}

/** The selected target: kind navigation + the current table. */
export function Workspace({ client, hub, target }: { client: Client; hub: ViewHub; target: Target }) {
  const [kinds, setKinds] = useState<KindDescriptor[] | null>(null)
  const [kindsError, setKindsError] = useState<string | null>(null)
  const [scopes, setScopes] = useState<ScopesView | null>(null)
  const defaultScope = target.defaultScope
  // Last kind and scope per target (SQLite target_state); null until loaded,
  // so the default view is not opened only to be replaced.
  const [ui, setUI] = useState<UIState | null>(null)
  // No remembered kind: the provider's default one (else the first in the navigation).
  const defaultKind = kinds?.find((k) => k.default && !k.hidden)?.id ?? kinds?.find((k) => !k.hidden)?.id ?? ''
  const kind = ui?.kind || defaultKind
  const scope: ScopeSel = ui?.scope ?? { mode: 'all' }
  const remember = (next: UIState) => {
    // Navigation drops the palette's requests: they were for the page it opened.
    setFilterReq(null)
    setOpenReq(null)
    setUI(next)
    void client.setTargetState(target.provider, target.id, 'kind', JSON.stringify(next.kind)).catch(() => {})
    void client.setTargetState(target.provider, target.id, 'scope', JSON.stringify(next.scope)).catch(() => {})
  }
  const setKind = (k: string) => remember({ kind: k, scope })
  const setScope = (s: ScopeSel) => remember({ kind, scope: s })
  // Palette requests to the page (a filter for its table, an object to open);
  // the page applies each once (by seq).
  const [filterReq, setFilterReq] = useState<PageReq<string> | null>(null)
  const [openReq, setOpenReq] = useState<PageReq<Ref> | null>(null)
  const reqSeq = useRef(0)

  // Tabs live in the dock above this (keyed) Workspace: log tabs close when
  // another target is selected, terminals stay.
  const targetRef = useMemo(() => ({ provider: target.provider, id: target.id }), [target.provider, target.id])
  const openLogs = useCallback((ref: Ref) => dock.openLogs(targetRef, target.title, ref), [targetRef, target.title])
  const [termDialog, setTermDialog] = useState<Ref | null>(null)
  const openTerminal = useCallback(
    (ref: Ref, dialog: boolean) => (dialog ? setTermDialog(ref) : dock.openTerminal(targetRef, target.title, { ref })),
    [targetRef, target.title],
  )
  const hasLogs = useCallback((kindId: string) => !!kinds?.find((k) => k.id === kindId)?.logs, [kinds])
  const hasExec = useCallback((kindId: string) => !!kinds?.find((k) => k.id === kindId)?.exec, [kinds])
  const hasForward = useCallback((kindId: string) => !!kinds?.find((k) => k.id === kindId)?.forward, [kinds])
  const actionsOf = useCallback((kindId: string) => kinds?.find((k) => k.id === kindId)?.actions ?? [], [kinds])
  const eventsKindOf = useCallback((kindId: string) => kinds?.find((k) => k.id === kindId)?.eventsKind, [kinds])
  const [actionReq, setActionReq] = useState<(ActionRequest & { seq: number }) | null>(null)
  const actionSeq = useRef(0)
  const openAction = useCallback(
    (ref: Ref, action: ActionDescriptor) =>
      setActionReq({ ref, action, kindTitle: ((k) => k?.singular ?? k?.title ?? ref.kind)(kinds?.find((k) => k.id === ref.kind)), seq: ++actionSeq.current }),
    [kinds],
  )

  useEffect(() => {
    // Workspace is keyed by target: state starts fresh for each one.
    let live = true
    const fallback: UIState = { kind: '', scope: defaultScope ? { mode: 'one', name: defaultScope } : { mode: 'all' } }
    client.getTargetState(target.provider, target.id).then(
      (st) => {
        if (!live) return
        setUI(parseState(st, fallback))
      },
      () => live && setUI(fallback),
    )
    client.listKinds(target.provider, target.id).then(
      (k) => live && setKinds(k),
      (e) => live && setKindsError(e instanceof Error ? e.message : String(e)),
    )
    client.listScopes(target.provider, target.id).then(
      (s) => live && setScopes(s),
      () => live && setScopes({ scopes: [], error: { code: 'unavailable', detail: '' } }),
    )
    return () => {
      live = false
    }
  }, [client, target.provider, target.id, defaultScope])

  const groups = useMemo(() => {
    const m = new Map<string, KindDescriptor[]>()
    for (const k of kinds ?? []) if (!k.hidden) m.set(k.group, [...(m.get(k.group) ?? []), k])
    return [...m.entries()]
  }, [kinds])
  const current = kinds?.find((k) => k.id === kind)

  // The palette acts through the latest state of this workspace.
  const paletteActs = useRef<Pick<PaletteHost, 'openKind' | 'setScope' | 'openObject'> | null>(null)
  useEffect(() => {
    paletteActs.current = {
      openKind: (k, filter) => {
        if (k !== kind) setKind(k)
        setFilterReq({ value: filter, seq: ++reqSeq.current })
      },
      setScope,
      openObject: (ref) => {
        // The overview has no table to open details over: the object's kind's table.
        if (kind === OVERVIEW || !current) setKind(kinds?.some((k) => k.id === ref.kind) ? ref.kind : (kinds?.find((k) => !k.hidden)?.id ?? kind))
        setOpenReq({ value: ref, seq: ++reqSeq.current })
      },
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
        openKind: (k, f) => paletteActs.current?.openKind(k, f),
        setScope: (s) => paletteActs.current?.setScope(s),
        openObject: (r) => paletteActs.current?.openObject(r),
      }),
    [targetRef, kinds, scopeNames],
  )

  return (
    <div className="flex min-h-0 flex-1">
      <nav aria-label="resources" data-area="nav" onKeyDown={onNavKey} className="w-40 shrink-0 overflow-y-auto border-r border-line bg-sidebar/60 px-2 py-3">
        <NavItem active={kind === OVERVIEW} onClick={() => setKind(OVERVIEW)} label={t('nav.overview')} />
        {groups.map(([group, list]) => (
          <section key={group} className="mt-3">
            <h3 className="px-2 pb-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{group}</h3>
            {list.map((k) => (
              <NavItem key={k.id} active={kind === k.id} onClick={() => setKind(k.id)} label={k.title} />
            ))}
          </section>
        ))}
        {kindsError && <p className="mt-3 px-2 text-xs text-danger">{kindsError}</p>}
      </nav>
      <main className="flex min-w-0 flex-1 flex-col">
        <div className="flex min-h-0 flex-1 flex-col">
        {!ui ? null : kind === OVERVIEW || !current ? (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <TargetDetails />
          </div>
        ) : (
          <ResourcePage
            // A new kind or scope is a new page: selection, filter and an open
            // drawer belong to the table they were made in.
            key={`${current.id}/${JSON.stringify(current.scoped ? scope : { mode: 'none' })}`}
            client={client}
            hub={hub}
            target={target}
            kind={current}
            scope={current.scoped ? scope : { mode: 'none' }}
            scopes={scopes}
            onScope={setScope}
            hasLogs={hasLogs}
            onLogs={openLogs}
            hasExec={hasExec}
            onTerminal={openTerminal}
            hasForward={hasForward}
            actionsOf={actionsOf}
            onAction={openAction}
            eventsKindOf={eventsKindOf}
            filterReq={filterReq}
            openReq={openReq}
          />
        )}
        </div>
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
        {actionReq && (
          <ActionDialog
            // A new request is a new dialog: nothing of the last one carries over.
            key={actionReq.seq}
            client={client}
            req={actionReq}
            onClose={() => setActionReq(null)}
          />
        )}
      </main>
    </div>
  )
}

/** Views by arrows, Home and End (one tab stop: the current view); Enter opens. */
function onNavKey(e: React.KeyboardEvent<HTMLElement>) {
  const items = [...e.currentTarget.querySelectorAll<HTMLElement>('[data-nav-item]')]
  const i = items.indexOf(document.activeElement as HTMLElement)
  const to = e.key === 'ArrowDown' ? i + 1 : e.key === 'ArrowUp' ? i - 1 : e.key === 'Home' ? 0 : e.key === 'End' ? items.length - 1 : null
  if (to === null || e.altKey || e.ctrlKey || e.metaKey || !items.length) return
  e.preventDefault()
  items[Math.max(0, Math.min(items.length - 1, to))].focus()
}

function NavItem({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return (
    <button
      onClick={onClick}
      data-nav-item
      tabIndex={active ? 0 : -1}
      data-area-focus={active ? '' : undefined}
      aria-current={active ? 'page' : undefined}
      className={['block w-full truncate rounded-md px-2 py-1 text-left', active ? 'bg-active text-fg' : 'text-fg-muted hover:bg-hover hover:text-fg'].join(' ')}
    >
      {label}
    </button>
  )
}

function ResourcePage(props: {
  client: Client
  hub: ViewHub
  target: Target
  kind: KindDescriptor
  scope: ScopeSel
  scopes: ScopesView | null
  onScope: (s: ScopeSel) => void
  hasLogs: (kindId: string) => boolean
  onLogs: (ref: Ref) => void
  hasExec: (kindId: string) => boolean
  onTerminal: (ref: Ref, dialog: boolean) => void
  hasForward: (kindId: string) => boolean
  actionsOf: (kindId: string) => ActionDescriptor[]
  onAction: (ref: Ref, action: ActionDescriptor) => void
  eventsKindOf: (kindId: string) => string | undefined
  filterReq?: PageReq<string> | null
  openReq?: PageReq<Ref> | null
}) {
  const { client, hub, target, kind, scope, scopes, onScope, hasLogs, onLogs, hasExec, onTerminal, hasForward, actionsOf, onAction, eventsKindOf, filterReq, openReq } = props
  const scopeKey = JSON.stringify(scope)
  const query = useMemo(() => ({ kind: kind.id, scope: JSON.parse(scopeKey) as ScopeSel }), [kind.id, scopeKey])
  const view = useView(hub, target.provider, target.id, query)
  const [filter, setFilter] = useState(filterReq?.value ?? '')
  const [selected, setSelected] = useState<string | null>(null)
  const [open, setOpen] = useState<Ref | null>(openReq?.value ?? null)
  // A palette request applies once, also to a page already open.
  const [applied, setApplied] = useState({ filter: filterReq?.seq ?? 0, open: openReq?.seq ?? 0 })
  if ((filterReq && filterReq.seq !== applied.filter) || (openReq && openReq.seq !== applied.open)) {
    if (filterReq && filterReq.seq !== applied.filter) setFilter(filterReq.value)
    if (openReq && openReq.seq !== applied.open) setOpen(openReq.value)
    setApplied({ filter: filterReq?.seq ?? applied.filter, open: openReq?.seq ?? applied.open })
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
  // The row's menu, for the row it opens on (its ref as of now).
  const rowMenu = (r: Row): MenuItem[] => {
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
      <header className="flex shrink-0 items-center gap-3 border-b border-line px-4 py-2">
        <h1 className="text-[15px] font-semibold">{kind.title}</h1>
        <span className="text-xs text-fg-subtle" aria-label="count">
          {view.rows.length}
        </span>
        {kind.scoped && (scopes?.kind && !scopes.error ? (
          <LiveScopePicker hub={hub} target={target} scopeKind={scopes.kind} scope={scope} scopes={scopes} onScope={onScope} />
        ) : (
          <ScopePicker scope={scope} scopes={scopes} onScope={onScope} />
        ))}
        {view.resync && <ResyncButton client={client} viewId={view.viewId} />}
        <label className="ml-auto flex w-64 items-center gap-2 rounded-md border border-line bg-app px-2 py-1 focus-within:border-accent">
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
      <StatusBanner state={view.status.state} cls={view.status.class} message={view.status.message} empty={view.rows.length === 0} coverage={view.status.coverage} />
      {view.status.coverage && <CoverageNote coverage={view.status.coverage} notCovered={view.kind?.notCovered ?? kind.notCovered} />}
      {metrics && <MetricsNote metrics={metrics} />}
      <div className="relative flex min-h-0 flex-1 flex-col">
        <div data-area="table" className="flex min-h-0 flex-1 flex-col">
        <ResourceTable
          areaFocus
          columns={columns}
          rows={view.rows}
          hideScope={scope.mode === 'one'}
          filter={filter}
          selected={selected}
          onSelect={(r: Row) => setSelected(r.id)}
          onOpen={(r: Row) => setOpen(r.ref)}
          onLogs={(r: Row) => hasLogs(r.ref.kind) && onLogs(r.ref)}
          onTerminal={(r: Row, dialog: boolean) => hasExec(r.ref.kind) && onTerminal(r.ref, dialog)}
          metrics={metrics}
          onVisibleRows={setVisibleRows}
          rowMenu={rowMenu}
          onDelete={(r: Row) => {
            const del = deleteOf(r)
            if (del) onAction(r.ref, del)
          }}
          defaultSort={view.kind?.sort ?? kind.sort}
        />
        </div>
        {open && (
          <ResourceDrawer
            key={`${open.kind}/${open.scope}/${open.name}/${open.uid}`}
            client={client}
            hub={hub}
            target={{ provider: target.provider, id: target.id }}
            subject={open}
            onClose={() => setOpen(null)}
            hasLogs={hasLogs}
            onLogs={onLogs}
            hasExec={hasExec}
            onTerminal={onTerminal}
            hasForward={hasForward}
            actionsOf={actionsOf}
            onAction={onAction}
            eventsKindOf={eventsKindOf}
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
  onScope: (s: ScopeSel) => void
}) {
  const { hub, target, scopeKind, scope, scopes, onScope } = props
  const query = useMemo(() => ({ kind: scopeKind, scope: { mode: 'none' as const } }), [scopeKind])
  const view = useView(hub, target.provider, target.id, query)
  const live = useMemo(
    () => (view.status.state === 'ready' || view.status.state === 'stale' ? { scopes: view.rows.map((r) => ({ name: r.ref.name })).sort((a, b) => a.name.localeCompare(b.name)) } : scopes),
    [view.rows, view.status.state, scopes],
  )
  const liveNames = useMemo(() => (live === scopes ? null : live?.scopes.map((s) => s.name)) ?? null, [live, scopes])
  useEffect(() => (liveNames ? lend('liveScopes', liveNames) : undefined), [liveNames])
  return <ScopePicker scope={scope} scopes={live} onScope={onScope} />
}

function ScopePicker({ scope, scopes, onScope }: { scope: ScopeSel; scopes: ScopesView | null; onScope: (s: ScopeSel) => void }) {
  const [typed, setTyped] = useState(scope.mode === 'one' ? (scope.name ?? '') : '')
  const words = useScopeWords()
  if (scopes?.error) {
    // Listing scopes is forbidden (or failed): the user can still type one.
    return (
      <form
        className="flex items-center gap-1"
        onSubmit={(e) => {
          e.preventDefault()
          onScope(typed.trim() ? { mode: 'one', name: typed.trim() } : { mode: 'all' })
        }}
      >
        <input
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          placeholder={t('scope.type', { scope: words.singular.toLowerCase() })}
          aria-label={words.singular}
          title={t('scope.cannotList', { scopes: words.plural, error: scopes.error.detail || scopes.error.code })}
          className="w-44 rounded-md border border-line bg-app px-2 py-1 outline-none focus:border-accent"
        />
      </form>
    )
  }
  const value = scope.mode === 'one' ? (scope.name ?? '') : ''
  const names = (scopes?.scopes ?? []).map((s) => s.name)
  if (value && !names.includes(value)) names.unshift(value)
  return (
    <select
      aria-label={words.singular}
      value={value}
      onChange={(e) => onScope(e.target.value ? { mode: 'one', name: e.target.value } : { mode: 'all' })}
      className="rounded-md border border-line bg-app px-2 py-1 outline-none focus:border-accent"
    >
      <option value="">{words.all}</option>
      {names.map((n) => (
        <option key={n} value={n}>
          {n}
        </option>
      ))}
    </select>
  )
}

function StatusBanner({ state, cls, message, empty, coverage }: { state: string; cls?: string; message?: string; empty: boolean; coverage?: SourceCoverage[] }) {
  if (state === 'ready') {
    if (!empty) return null
    // A view of several sources: nothing found is only "nothing" where it could look.
    const text = !coverage ? t('table.empty') : coverage.every((c) => c.state === 'ready') ? t('coverage.noneFound') : t('coverage.noneInObserved')
    return <p className="px-4 py-6 text-center text-fg-subtle">{text}</p>
  }
  if (state === 'loading') return empty ? <p className="px-4 py-6 text-center text-fg-subtle">{t('app.loading')}</p> : null
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
  if (metrics.status === 'ok' && !metrics.limit) return null
  const failed = metrics.status !== 'ok'
  return (
    <p role="note" aria-label={t('metrics.label')} className="mx-4 my-1 text-xs text-fg-subtle" title={failed ? metrics.message : undefined}>
      {failed ? t('metrics.failed', { reason: classLabel(metrics.status) }) : t('metrics.limited', { n: metrics.limit ?? 0 })}
    </p>
  )
}

function CoverageNote({ coverage, notCovered }: { coverage: SourceCoverage[]; notCovered?: string[] }) {
  const missing = coverage.filter((c) => c.state !== 'ready')
  const why = (c: SourceCoverage) =>
    c.state === 'denied' ? classLabel(c.class ?? 'forbidden') : c.state === 'error' ? classLabel(c.class ?? 'internal') : t(c.state === 'stale' ? 'coverage.stale' : 'coverage.loading')
  return (
    <div className="mx-4 my-2 space-y-0.5 text-xs">
      {missing.length > 0 && (
        <p role="note" aria-label={t('coverage.notObserved')} className="text-warning" title={missing.map((c) => `${c.source}: ${c.message ?? c.state}`).join('\n')}>
          {t('coverage.notObserved')}: {missing.map((c) => `${c.source} (${why(c)})`).join(', ')}
        </p>
      )}
      <p role="note" aria-label={t('coverage.label')} className="text-fg-subtle">
        {t('coverage.checked')}: {coverage.map((c) => c.source).join(', ')}
        {notCovered?.length ? ` · ${t('coverage.notChecked')}: ${notCovered.join(', ')}` : ''}
      </p>
    </div>
  )
}
