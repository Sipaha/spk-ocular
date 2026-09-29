import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { ActionDescriptor, KindDescriptor, Ref, Row, ScopeSel, ScopesView, Target } from '../api/types'
import { actionLabel, classLabel, t } from '../i18n'
import { ActionDialog, type ActionRequest } from '../actions/ActionDialog'
import type { MenuItem } from '../actions/Menu'
import { useMetrics } from '../views/useMetrics'
import { useView } from '../views/useView'
import type { ViewHub } from '../views/viewSync'
import { ResourceDrawer } from './ResourceDrawer'
import { ResourceTable } from './ResourceTable'
import { TargetDetails } from './TargetDetails'
import { SearchIcon, WarningIcon } from './icons'
import { dock } from '../dock/store'
import { TerminalDialog } from '../term/TerminalDialog'

const OVERVIEW = '__overview'

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
  const defaultNs = target.details?.find((d) => d.key === 'defaultNamespace')?.value
  // Last kind and scope per target (SQLite target_state); null until loaded,
  // so the default view is not opened only to be replaced.
  const [ui, setUI] = useState<UIState | null>(null)
  const kind = ui?.kind ?? 'pods'
  const scope: ScopeSel = ui?.scope ?? { mode: 'all' }
  const remember = (next: UIState) => {
    setUI(next)
    void client.setTargetState(target.provider, target.id, 'kind', JSON.stringify(next.kind)).catch(() => {})
    void client.setTargetState(target.provider, target.id, 'scope', JSON.stringify(next.scope)).catch(() => {})
  }
  const setKind = (k: string) => remember({ kind: k, scope })
  const setScope = (s: ScopeSel) => remember({ kind, scope: s })

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
  const [actionReq, setActionReq] = useState<(ActionRequest & { seq: number }) | null>(null)
  const actionSeq = useRef(0)
  const openAction = useCallback(
    (ref: Ref, action: ActionDescriptor) =>
      setActionReq({ ref, action, kindTitle: kinds?.find((k) => k.id === ref.kind)?.title ?? ref.kind, seq: ++actionSeq.current }),
    [kinds],
  )

  useEffect(() => {
    // Workspace is keyed by target: state starts fresh for each one.
    let live = true
    const fallback: UIState = { kind: 'pods', scope: defaultNs ? { mode: 'one', name: defaultNs } : { mode: 'all' } }
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
  }, [client, target.provider, target.id, defaultNs])

  const groups = useMemo(() => {
    const m = new Map<string, KindDescriptor[]>()
    for (const k of kinds ?? []) if (!k.hidden) m.set(k.group, [...(m.get(k.group) ?? []), k])
    return [...m.entries()]
  }, [kinds])
  const current = kinds?.find((k) => k.id === kind)

  return (
    <div className="flex min-h-0 flex-1">
      <nav aria-label="resources" className="w-40 shrink-0 overflow-y-auto border-r border-line bg-sidebar/60 px-2 py-3">
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

function NavItem({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return (
    <button
      onClick={onClick}
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
}) {
  const { client, hub, target, kind, scope, scopes, onScope, hasLogs, onLogs, hasExec, onTerminal, hasForward, actionsOf, onAction } = props
  const scopeKey = JSON.stringify(scope)
  const query = useMemo(() => ({ kind: kind.id, scope: JSON.parse(scopeKey) as ScopeSel }), [kind.id, scopeKey])
  const view = useView(hub, target.provider, target.id, query)
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const [open, setOpen] = useState<Ref | null>(null)
  const columns = view.kind?.columns ?? kind.columns
  const metrics = useMetrics(client, view.viewId, columns.some((c) => c.metric))
  const actions = actionsOf(kind.id)
  const del = actions.find((a) => a.id === 'delete')
  // The row's menu, for the row it opens on (its ref as of now).
  const rowMenu = (r: Row): MenuItem[] => {
    const items: MenuItem[] = [{ id: 'details', label: t('row.details'), onSelect: () => setOpen(r.ref) }]
    if (kind.logs) items.push({ id: 'logs', label: t('row.logs'), hint: 'L', onSelect: () => onLogs(r.ref) })
    if (kind.exec) items.push({ id: 'terminal', label: t('row.terminal'), hint: 'S', onSelect: () => onTerminal(r.ref, false) })
    actions.forEach((a, i) =>
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
        <label className="ml-auto flex w-64 items-center gap-2 rounded-md border border-line bg-app px-2 py-1 focus-within:border-accent">
          <SearchIcon className="h-3.5 w-3.5 text-fg-subtle" />
          <input
            data-primary-filter
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
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
      <StatusBanner state={view.status.state} cls={view.status.class} message={view.status.message} empty={view.rows.length === 0} />
      <div className="relative flex min-h-0 flex-1 flex-col">
        <ResourceTable
          columns={columns}
          rows={view.rows}
          hideScope={scope.mode === 'one'}
          filter={filter}
          selected={selected}
          onSelect={(r: Row) => setSelected(r.id)}
          onOpen={(r: Row) => setOpen(r.ref)}
          onLogs={kind.logs ? (r: Row) => onLogs(r.ref) : undefined}
          onTerminal={kind.exec ? (r: Row, dialog: boolean) => onTerminal(r.ref, dialog) : undefined}
          metrics={metrics}
          rowMenu={rowMenu}
          onDelete={del && ((r: Row) => onAction(r.ref, del))}
        />
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
          />
        )}
      </div>
    </>
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
  return <ScopePicker scope={scope} scopes={live} onScope={onScope} />
}

function ScopePicker({ scope, scopes, onScope }: { scope: ScopeSel; scopes: ScopesView | null; onScope: (s: ScopeSel) => void }) {
  const [typed, setTyped] = useState(scope.mode === 'one' ? (scope.name ?? '') : '')
  if (scopes?.error) {
    // Listing namespaces is forbidden (or failed): the user can still type one.
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
          placeholder={t('scope.type')}
          aria-label={t('scope.label')}
          title={t('scope.cannotList', { error: scopes.error.detail || scopes.error.code })}
          className="w-44 rounded-md border border-line bg-app px-2 py-1 outline-none focus:border-accent"
        />
      </form>
    )
  }
  const value = scope.mode === 'one' ? (scope.name ?? '') : ''
  const names = scopes?.scopes.map((s) => s.name) ?? []
  if (value && !names.includes(value)) names.unshift(value)
  return (
    <select
      aria-label={t('scope.label')}
      value={value}
      onChange={(e) => onScope(e.target.value ? { mode: 'one', name: e.target.value } : { mode: 'all' })}
      className="rounded-md border border-line bg-app px-2 py-1 outline-none focus:border-accent"
    >
      <option value="">{t('scope.all')}</option>
      {names.map((n) => (
        <option key={n} value={n}>
          {n}
        </option>
      ))}
    </select>
  )
}

function StatusBanner({ state, cls, message, empty }: { state: string; cls?: string; message?: string; empty: boolean }) {
  if (state === 'ready') return empty ? <p className="px-4 py-6 text-center text-fg-subtle">{t('table.empty')}</p> : null
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
