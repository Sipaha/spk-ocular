import { lazy, Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ApiError, type Client } from '../api/client'
import type { ActionDescriptor, EditDoc, Relation, Ref, Resource } from '../api/types'
import { Menu } from '../actions/Menu'
import { EditDialog, type EditReview } from '../edit/EditDialog'
import { holdEdits, mayLeave } from '../edit/guard'
import { actionLabel, classLabel, detailLabel, relationLabel, t } from '../i18n'
import { showNotice } from '../store'
import { inTerminal } from '../keyboard'
import { consumed, focusMark, isTyping, overlayOpen, restoreFocus } from '../shortcuts'
import { refTitle } from '../refs'
import { PortsSection } from '../tunnels/Ports'
import { ValuesSection } from '../values/ValuesSection'
import { useView } from '../views/useView'
import type { ViewHub } from '../views/viewSync'
import { HealthDot, ResourceTable, healthText } from './ResourceTable'
import { errorDetail } from '../errors'

const YamlView = lazy(() => import('./YamlView'))

interface Props {
  client: Client
  hub: ViewHub
  target: { provider: string; id: string }
  /** The object shown; history is kept for relation navigation. */
  subject: Ref
  /** The tab shown first ('details' — none given); onTab hears each shown. */
  initialTab?: string
  onTab?: (tab: string) => void
  onClose: () => void
  /** Kinds with logs get a Logs button. */
  hasLogs?: (kindId: string) => boolean
  onLogs?: (ref: Ref) => void
  hasExec?: (kindId: string) => boolean
  hasForward?: (kindId: string) => boolean
  onTerminal?: (ref: Ref, dialog: boolean) => void
  /** The actions of a kind: an Actions menu acts on the object shown now. */
  actionsOf?: (kindId: string) => ActionDescriptor[]
  onAction?: (ref: Ref, action: ActionDescriptor) => void
  /** The kind listing events about objects of a kind (KindDescriptor.eventsKind); none: no events section. */
  eventsKindOf?: (kindId: string) => string | undefined
  /** Objects of the kind can be edited as text (KindDescriptor.editable). */
  editableOf?: (kindId: string) => boolean
  /** Objects of the kind keep protected values by key (KindDescriptor.values). */
  valuesOf?: (kindId: string) => boolean
  /** The kind's name for one object (the review names it). */
  kindTitleOf?: (kindId: string) => string
}

/** An edit in progress: the text as given (doc) and as edited now. */
interface Editing {
  key: string
  doc: EditDoc
  text: string
}

const toolBtn = 'rounded-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg disabled:pointer-events-none disabled:opacity-50'

type Tab = 'details' | 'yaml'

export function ResourceDrawer({ client, hub, target, subject, initialTab, onTab, onClose, hasLogs, onLogs, hasExec, onTerminal, hasForward, actionsOf, onAction, eventsKindOf, editableOf, valuesOf, kindTitleOf }: Props) {
  const [stack, setStack] = useState<Ref[]>([subject])
  const [tab, setTab] = useState<Tab>(initialTab === 'yaml' ? 'yaml' : 'details')
  useEffect(() => onTab?.(tab), [onTab, tab])
  const [res, setRes] = useState<{ key: string; r?: Resource; error?: string; gone?: boolean } | null>(null)
  const [menuAt, setMenuAt] = useState<{ x: number; y: number } | null>(null)
  const actionsBtn = useRef<HTMLButtonElement>(null)
  const current = stack[stack.length - 1]
  const key = `${current.kind}/${current.scope ?? ''}/${current.name}/${current.uid ?? ''}`
  const revision = useObjectRevision(hub, target, current)
  // The last object recorded as recently opened (once per object shown).
  const touched = useRef<string | null>(null)

  useEffect(() => {
    let live = true
    client.getResource(current).then(
      (r) => {
        if (!live) return
        setRes({ key, r })
        if (touched.current !== key) {
          touched.current = key
          void client.touchRecent({ ...r.ref, provider: target.provider, target: target.id }, refTitle(r.ref)).catch(() => {})
        }
      },
      (e) =>
        live &&
        // The object's last details stay under the message: whatever is open
        // on them (a value's draft) is left by the user, never dropped.
        setRes((old) => ({
          key,
          r: old?.key === key ? old.r : undefined,
          error: e instanceof Error ? e.message : String(e),
          gone: e instanceof ApiError && (e.code === 'not_found' || e.code === 'gone'),
        })),
    )
    return () => {
      live = false
    }
    // revision: refetch when the object changes (any field, not only table cells)
  }, [client, current, key, revision, target.provider, target.id])

  // The edit of the object shown (its text as edited), by key: another
  // object never shows it. The editor opens on the YAML tab.
  const [edit, setEdit] = useState<Editing | null>(null)
  const [editLoad, setEditLoad] = useState<{ key: string; error?: string } | null>(null)
  // The review asked for: the text as it was then (typing after it is not
  // part of what the review shows and writes).
  const [reviewing, setReviewing] = useState<EditReview | null>(null)
  const editing = edit?.key === key ? edit : null
  const editRef = useRef(editing)
  const keyRef = useRef(key)
  useEffect(() => {
    editRef.current = editing
    keyRef.current = key
  })
  const host = useRef<HTMLDivElement>(null)
  const focusEditor = () => host.current?.querySelector<HTMLElement>('.cm-content')?.focus()
  const endEdit = () => {
    setEdit(null)
    setReviewing(null)
  }
  // Edits not written are held: leaving asks first (edit/guard).
  const editingKey = editing ? key : null
  useEffect(() => {
    if (!editingKey) return
    return holdEdits({ dirty: () => !!editRef.current && editRef.current.text !== editRef.current.doc.text, discard: endEdit, focus: focusEditor })
  }, [editingKey])

  // Closing gives focus back to where details were opened from (else the table).
  const [mark] = useState(focusMark)
  const close = () =>
    mayLeave(() => {
      onClose()
      restoreFocus(mark)
    })
  const canEdit = !!editableOf?.(current.kind) && !!res?.r && res.key === key && !res.gone
  const loadingEdit = editLoad?.key === key && !editLoad.error
  const startEdit = () => {
    if (!canEdit || editing || loadingEdit) return
    setTab('yaml')
    const k = key
    setEditLoad({ key: k })
    client.getEditSource({ ...(res?.r?.ref ?? current), provider: target.provider, target: target.id }).then(
      (doc) => {
        if (keyRef.current !== k) return
        setEdit({ key: k, doc, text: doc.text })
        setEditLoad(null)
      },
      (e) => keyRef.current === k && setEditLoad({ key: k, error: t('edit.loadFailed', { class: classLabel(e instanceof ApiError ? e.code : 'internal'), detail: errorDetail(e) }) }),
    )
  }
  const review = () => {
    const ed = editRef.current
    if (ed && !reviewing) setReviewing({ ref: ed.doc.ref, base: ed.doc.base, original: ed.doc.text, edited: ed.text, kindTitle: kindTitleOf?.(current.kind) ?? current.kind })
  }
  const cancelEdit = () => mayLeave(endEdit)
  const keys = useRef({ close, depth: stack.length, startEdit, review, cancelEdit, editing: !!editing })
  useEffect(() => {
    keys.current = { close, depth: stack.length, startEdit, review, cancelEdit, editing: !!editing }
  })
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (consumed(e) || inTerminal(e.target) || overlayOpen()) return
      const k = keys.current
      // Esc steps out of the editor first (asking when edits would be lost).
      if (e.key === 'Escape' && !(e.target instanceof HTMLElement && e.target.closest('.cm-panels'))) (k.editing ? k.cancelEdit : k.close)()
      else if (e.key === 'ArrowLeft' && e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey && !isTyping(e.target)) {
        // Alt+←: back along the relations (never Backspace: it edits text);
        // never the page's history (browser mode would leave the app).
        e.preventDefault()
        if (k.depth > 1)
          mayLeave(() => {
            setStack((s) => s.slice(0, -1))
            setTab('details')
          })
      } else if (e.code === 'KeyE' && !e.repeat && !e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey && !isTyping(e.target) && !k.editing) {
        // By the physical key: a Russian layout types "у".
        e.preventDefault()
        k.startEdit()
      } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && k.editing) {
        e.preventDefault()
        k.review()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const shown = res?.key === key ? res : null
  const r = shown?.r
  const actions = actionsOf?.(current.kind) ?? []
  // The object shown now (after relation navigation: that one), with the UID read.
  // A deleted object has nothing to open or act on.
  const hasTools = !shown?.gone && ((!!onLogs && !!hasLogs?.(current.kind)) || (!!onTerminal && !!hasExec?.(current.kind)) || (!!onAction && actions.length > 0) || !!editableOf?.(current.kind))
  // Shown by its title (a container's name) once read; the key stays the name.
  const title = refTitle(r?.ref ?? current)
  const shownRef = (): Ref => ({ ...(r?.ref ?? current), provider: target.provider, target: target.id })
  const go = (ref: Ref) =>
    mayLeave(() => {
      setStack((s) => [...s, ref])
      setTab('details')
    })
  const pickTab = (tb: Tab) => (tb === tab ? undefined : mayLeave(() => setTab(tb)))
  // The live object moved on since its text was read.
  const moved = !!editing?.doc.version && !!revision && revision !== editing.doc.version

  return (
    <aside role="dialog" aria-label={`${current.kind} ${title}`} data-area="details" tabIndex={-1} className="absolute inset-y-0 right-0 z-10 flex w-[min(720px,55%)] flex-col border-l border-line bg-app shadow-2xl outline-none">
      {/* Name first, whole; the object's tools on a line of their own. */}
      <header className="border-b border-line px-4 py-2">
        {stack.length > 1 && (
          // Where the relations led: each step is a way back.
          <nav aria-label={t('drawer.path')} className="mb-1 flex min-w-0 flex-wrap items-center gap-1 text-xs text-fg-subtle">
            {stack.slice(0, -1).map((ref, i) => (
              <span key={i} className="flex min-w-0 items-center gap-1">
                <button
                  className="max-w-48 truncate rounded px-1 hover:bg-hover hover:text-fg"
                  title={`${ref.kind} ${refTitle(ref)}`}
                  onClick={() =>
                    mayLeave(() => {
                      setStack((s) => s.slice(0, i + 1))
                      setTab('details')
                    })
                  }
                >
                  {refTitle(ref)}
                </button>
                <span aria-hidden>›</span>
              </span>
            ))}
            <span aria-current="page" className="max-w-48 truncate px-1 text-fg-muted">
              {title}
            </span>
          </nav>
        )}
        <div className="flex items-center gap-2">
          {stack.length > 1 && (
            <button className="rounded px-1.5 text-fg-muted hover:bg-hover hover:text-fg" onClick={() => mayLeave(() => setStack((s) => s.slice(0, -1)))} aria-label={t('drawer.back')} title={`${t('drawer.back')} (Alt+←)`}>
              ←
            </button>
          )}
          <span className="shrink-0 text-xs uppercase tracking-wide text-fg-subtle">{current.kind}</span>
          <h2 className="min-w-0 flex-1 truncate font-semibold" title={title}>
            {title}
          </h2>
          <button className="rounded px-2 text-lg leading-none text-fg-muted hover:bg-hover hover:text-fg" onClick={close} aria-label={t('drawer.close')}>
            ×
          </button>
        </div>
        {hasTools && (
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            {onLogs && hasLogs?.(current.kind) && (
              <button
                className="rounded-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg"
                onClick={() => onLogs({ ...(r?.ref ?? current), provider: target.provider, target: target.id })}
                title={t('logs.openHint')}
              >
                {t('logs.open')}
              </button>
            )}
            {onTerminal && hasExec?.(current.kind) && (
              // A split button: the terminal at once, or ▾ to choose the
              // container and the command first.
              <span className="inline-flex">
                <button
                  className="rounded-l-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg"
                  onClick={() => onTerminal({ ...(r?.ref ?? current), provider: target.provider, target: target.id }, false)}
                  title={t('term.openHint')}
                >
                  {t('term.open')}
                </button>
                <button
                  className="-ml-px rounded-r-md border border-line px-1.5 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg"
                  onClick={() => onTerminal({ ...(r?.ref ?? current), provider: target.provider, target: target.id }, true)}
                  title={t('term.dialogHint')}
                  aria-label={t('term.dialog')}
                >
                  ▾
                </button>
              </span>
            )}
            {editableOf?.(current.kind) && (
              <button className={toolBtn} disabled={!canEdit || !!editing || loadingEdit} onClick={startEdit} title={t('edit.openHint')}>
                {t('edit.open')}
              </button>
            )}
            {onAction && actions.length > 0 && (
              <button
                ref={actionsBtn}
                className="rounded-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg"
                aria-haspopup="menu"
                aria-expanded={!!menuAt}
                title={t('action.menuHint')}
                onClick={() => {
                  const b = actionsBtn.current?.getBoundingClientRect()
                  setMenuAt({ x: b?.left ?? 0, y: (b?.bottom ?? 0) + 2 })
                }}
              >
                {t('action.menu')} ▾
              </button>
            )}
            {menuAt && onAction && (
              <Menu
                label={t('action.menu')}
                at={menuAt}
                onClose={() => setMenuAt(null)}
                items={actions.map((a) => ({ id: a.id, label: actionLabel(a) + (a.param ? '…' : ''), danger: a.destructive, onSelect: () => onAction(shownRef(), a) }))}
              />
            )}
          </div>
        )}
      </header>
      <nav className="flex gap-1 border-b border-line px-3" role="tablist">
        {(['details', 'yaml'] as Tab[]).map((tb) => (
          <button
            key={tb}
            role="tab"
            aria-selected={tab === tb}
            onClick={() => pickTab(tb)}
            className={['border-b-2 px-3 py-1.5', tab === tb ? 'border-accent text-fg' : 'border-transparent text-fg-muted hover:text-fg'].join(' ')}
          >
            {t(tb === 'details' ? 'drawer.details' : 'drawer.yaml')}
          </button>
        ))}
      </nav>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {!shown && <p className="p-4 text-fg-subtle">{t('app.loading')}</p>}
        {shown?.error &&
          (shown.gone ? (
            <p role="status" className="m-4 rounded-md border border-line px-3 py-2 text-fg-muted">
              {t('drawer.deleted')}
            </p>
          ) : (
            <p role="alert" className="m-4 rounded-md bg-danger/10 px-3 py-2 text-danger">
              {shown.error}
            </p>
          ))}
        {r && tab === 'yaml' && editLoad?.key === key && (
          <p role={editLoad.error ? 'alert' : 'status'} className={['m-4 rounded-md px-3 py-2', editLoad.error ? 'bg-danger/10 text-danger' : 'text-fg-subtle'].join(' ')}>
            {editLoad.error ?? t('edit.loading')}
          </p>
        )}
        {r && tab === 'yaml' && !editing && (
          <div className="h-full">
            <Suspense fallback={<pre className="p-4 font-mono text-xs text-fg-muted">{r.yaml}</pre>}>
              <YamlView key="view" text={r.yaml} />
            </Suspense>
          </div>
        )}
        {tab === 'yaml' && editing && (
          <div ref={host} className="flex h-full min-h-0 flex-col">
            {moved && (
              <p role="status" className="border-b border-line bg-warning/10 px-3 py-1.5 text-xs text-warning">
                {t('edit.changedOnServer')}
              </p>
            )}
            <div className="min-h-0 flex-1">
              <Suspense fallback={<pre className="p-4 font-mono text-xs text-fg-muted">{editing.doc.text}</pre>}>
                <YamlView
                  key={`edit/${editing.doc.base}`}
                  editable
                  autoFocus
                  label={t('edit.editor')}
                  text={editing.doc.text}
                  onChange={(text) => setEdit((ed) => (ed && ed.key === editing.key ? { ...ed, text } : ed))}
                  onSubmit={review}
                />
              </Suspense>
            </div>
            <div className="flex shrink-0 items-center gap-2 border-t border-line px-3 py-2">
              <span className="min-w-0 flex-1 truncate text-xs text-fg-subtle">{editing.text === editing.doc.text ? t('edit.unchanged') : t('edit.hint')}</span>
              <button className={toolBtn} onClick={cancelEdit}>
                {t('edit.cancel')}
              </button>
              <button className="rounded-md bg-accent px-2 py-0.5 text-xs text-accent-fg disabled:opacity-50" onClick={review} title="Ctrl+Enter">
                {t('edit.review')} <span className="opacity-70">Ctrl+Enter</span>
              </button>
            </div>
          </div>
        )}
        {r && tab === 'details' && (
          <Details
            client={client}
            hub={hub}
            target={target}
            r={r}
            onGo={go}
            eventsKind={eventsKindOf?.(current.kind)}
            // Keyed by the object: its values never show for another one.
            values={
              valuesOf?.(current.kind) &&
              r.ref.uid && <ValuesSection key={key} client={client} subject={{ ...r.ref, provider: target.provider, target: target.id }} revision={revision} gone={shown?.gone} kindTitle={kindTitleOf?.(current.kind) ?? current.kind} />
            }
            // Right under the facts: the events list below is a fixed-height box.
            ports={hasForward?.(current.kind) && <PortsSection key={key} client={client} subject={{ ...r.ref, provider: target.provider, target: target.id }} />}
          />
        )}
      </div>
      {reviewing && editing && (
        <EditDialog
          client={client}
          req={reviewing}
          onBack={() => {
            setReviewing(null)
            requestAnimationFrame(focusEditor)
          }}
          onDone={(res) => {
            showNotice(res.message)
            endEdit()
          }}
        />
      )}
    </aside>
  )
}

function Details(props: { client: Client; hub: ViewHub; target: { provider: string; id: string }; r: Resource; onGo: (ref: Ref) => void; eventsKind?: string; ports?: ReactNode; values?: ReactNode }) {
  const { hub, target, r, onGo, eventsKind, ports, values } = props
  const groups = useMemo(() => {
    const m = new Map<string, Relation[]>()
    for (const rel of r.relations ?? []) m.set(rel.type, [...(m.get(rel.type) ?? []), rel])
    return [...m.entries()]
  }, [r.relations])
  return (
    <div className="space-y-5 p-4">
      {r.health.state !== 'ok' && (
        <section className={['rounded-md border border-line px-3 py-2', healthText[r.health.state]].join(' ')} aria-label={t('drawer.health')}>
          {(r.health.issues ?? [{ state: r.health.state, reason: r.health.reason ?? '', message: r.health.message }]).map((i, n) => (
            <p key={n}>
              <HealthDot state={i.state} />
              <b>{i.reason}</b>
              {i.message && <span className="text-fg-muted"> — {i.message}</span>}
            </p>
          ))}
        </section>
      )}
      <dl className="grid grid-cols-[fit-content(35%)_1fr] gap-x-6 gap-y-1.5">
        {r.facts.map((f) => (
          <div key={f.key} className="contents">
            <dt className="text-fg-muted">{detailLabel(f.key)}</dt>
            <dd className="min-w-0 font-mono text-[13px] break-all whitespace-pre-line">{f.value}</dd>
          </div>
        ))}
      </dl>
      {values}
      {ports}
      {(groups.length > 0 || r.relationsError) && (
        <section aria-label={t('drawer.related')}>
          <h3 className="mb-1.5 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t('drawer.related')}</h3>
          {groups.map(([type, rels]) => (
            <div key={type} className="mb-2">
              <p className="text-xs text-fg-subtle">{relationLabel(type)}</p>
              <ul>
                {rels.map((rel) => (
                  <li key={`${rel.ref.kind}/${rel.ref.name}/${rel.ref.uid ?? ''}`}>
                    {rel.inert ? (
                      <span className="text-fg-muted" title={t('drawer.relationInert')}>
                        {rel.ref.kind}/{refTitle(rel.ref)}
                      </span>
                    ) : (
                      <button className="text-accent hover:underline" onClick={() => onGo({ ...rel.ref, provider: target.provider, target: target.id })}>
                        {rel.ref.kind}/{refTitle(rel.ref)}
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))}
          {r.relationsError && <p className="text-xs text-warning">{t('drawer.relationsPartial', { error: r.relationsError })}</p>}
          {r.relationsTruncated && <p className="text-xs text-fg-subtle">{t('drawer.relationsTruncated')}</p>}
        </section>
      )}
      {r.ref.uid && eventsKind && <ObjectEvents hub={hub} kind={eventsKind} subject={r.ref} />}
    </div>
  )
}

/**
 * The shown object's revision, from a live view narrowed to its name: the
 * details refetch when it changes — also for fields the table does not show
 * and after navigating to a related object. Debounced: a burst of updates
 * is one refetch.
 */
function useObjectRevision(hub: ViewHub, target: { provider: string; id: string }, ref: Ref): string | undefined {
  const query = useMemo(
    () => ({ kind: ref.kind, scope: ref.scope ? { mode: 'one' as const, name: ref.scope } : { mode: 'none' as const }, name: ref.name }),
    [ref.kind, ref.scope, ref.name],
  )
  const view = useView(hub, target.provider, target.id, query)
  const row = view.rows.find((r) => !ref.uid || r.ref.uid === ref.uid)
  const rev = row?.rev
  const [debounced, setDebounced] = useState(rev)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(rev), 400)
    return () => clearTimeout(t)
  }, [rev])
  return debounced
}

/** Events about one object: a normal live view narrowed by Subject. */
function ObjectEvents({ hub, kind, subject }: { hub: ViewHub; kind: string; subject: Ref }) {
  const query = useMemo(
    () => ({ kind, scope: subject.scope ? { mode: 'one' as const, name: subject.scope } : { mode: 'all' as const }, subject }),
    [kind, subject],
  )
  const view = useView(hub, subject.provider, subject.target, query)
  const cols = view.kind?.columns ?? []
  return (
    <section aria-label={t('drawer.events')} className="flex h-72 flex-col">
      <h3 className="mb-1.5 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">
        {t('drawer.events')} <span className="font-normal">{view.rows.length}</span>
      </h3>
      {view.status.state === 'error' ? (
        <p className="text-xs text-danger">
          {classLabel(view.status.class ?? '')} {view.status.message}
        </p>
      ) : view.rows.length === 0 ? (
        <p className="text-xs text-fg-subtle">{view.status.state === 'loading' ? t('app.loading') : t('drawer.noEvents')}</p>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col rounded-md border border-line">
          <ResourceTable columns={cols} rows={view.rows} hideScope filter="" selected={null} onSelect={() => {}} />
        </div>
      )}
    </section>
  )
}
