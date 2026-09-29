import { lazy, Suspense, useEffect, useMemo, useState, type ReactNode } from 'react'
import type { Client } from '../api/client'
import type { Relation, Ref, Resource } from '../api/types'
import { classLabel, detailLabel, relationLabel, t } from '../i18n'
import { inTerminal } from '../keyboard'
import { PortsSection } from '../tunnels/Ports'
import { useView } from '../views/useView'
import type { ViewHub } from '../views/viewSync'
import { HealthDot, ResourceTable, healthText } from './ResourceTable'

const YamlView = lazy(() => import('./YamlView'))

interface Props {
  client: Client
  hub: ViewHub
  target: { provider: string; id: string }
  /** The object shown; history is kept for relation navigation. */
  subject: Ref
  onClose: () => void
  /** Kinds with logs get a Logs button. */
  hasLogs?: (kindId: string) => boolean
  onLogs?: (ref: Ref) => void
  hasExec?: (kindId: string) => boolean
  hasForward?: (kindId: string) => boolean
  onTerminal?: (ref: Ref, dialog: boolean) => void
}

type Tab = 'details' | 'yaml'

export function ResourceDrawer({ client, hub, target, subject, onClose, hasLogs, onLogs, hasExec, onTerminal, hasForward }: Props) {
  const [stack, setStack] = useState<Ref[]>([subject])
  const [tab, setTab] = useState<Tab>('details')
  const [res, setRes] = useState<{ key: string; r?: Resource; error?: string } | null>(null)
  const current = stack[stack.length - 1]
  const key = `${current.kind}/${current.scope ?? ''}/${current.name}/${current.uid ?? ''}`
  const revision = useObjectRevision(hub, target, current)

  useEffect(() => {
    let live = true
    client.getResource(current).then(
      (r) => live && setRes({ key, r }),
      (e) => live && setRes({ key, error: e instanceof Error ? e.message : String(e) }),
    )
    return () => {
      live = false
    }
    // revision: refetch when the object changes (any field, not only table cells)
  }, [client, current, key, revision])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !inTerminal(e.target) && !(e.target instanceof HTMLElement && e.target.closest('.cm-panels'))) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const shown = res?.key === key ? res : null
  const r = shown?.r
  const go = (ref: Ref) => {
    setStack((s) => [...s, ref])
    setTab('details')
  }

  return (
    <aside role="dialog" aria-label={`${current.kind} ${current.name}`} className="absolute inset-y-0 right-0 z-10 flex w-[min(720px,55%)] flex-col border-l border-line bg-app shadow-2xl">
      <header className="flex items-center gap-2 border-b border-line px-4 py-2">
        {stack.length > 1 && (
          <button className="rounded px-1.5 text-fg-muted hover:bg-hover hover:text-fg" onClick={() => setStack((s) => s.slice(0, -1))} aria-label={t('drawer.back')}>
            ←
          </button>
        )}
        <span className="text-xs uppercase tracking-wide text-fg-subtle">{current.kind}</span>
        <h2 className="min-w-0 flex-1 truncate font-semibold">{current.name}</h2>
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
          <>
            <button
              className="rounded-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg"
              onClick={() => onTerminal({ ...(r?.ref ?? current), provider: target.provider, target: target.id }, false)}
              title={t('term.openHint')}
            >
              {t('term.open')}
            </button>
            <button
              className="rounded-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg"
              onClick={() => onTerminal({ ...(r?.ref ?? current), provider: target.provider, target: target.id }, true)}
              title={t('term.dialogHint')}
              aria-label={t('term.dialog')}
            >
              …
            </button>
          </>
        )}
        <button className="rounded px-2 text-lg leading-none text-fg-muted hover:bg-hover hover:text-fg" onClick={onClose} aria-label={t('drawer.close')}>
          ×
        </button>
      </header>
      <nav className="flex gap-1 border-b border-line px-3" role="tablist">
        {(['details', 'yaml'] as Tab[]).map((tb) => (
          <button
            key={tb}
            role="tab"
            aria-selected={tab === tb}
            onClick={() => setTab(tb)}
            className={['border-b-2 px-3 py-1.5', tab === tb ? 'border-accent text-fg' : 'border-transparent text-fg-muted hover:text-fg'].join(' ')}
          >
            {t(tb === 'details' ? 'drawer.details' : 'drawer.yaml')}
          </button>
        ))}
      </nav>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {!shown && <p className="p-4 text-fg-subtle">{t('app.loading')}</p>}
        {shown?.error && (
          <p role="alert" className="m-4 rounded-md bg-danger/10 px-3 py-2 text-danger">
            {shown.error}
          </p>
        )}
        {r && tab === 'yaml' && (
          <div className="h-full">
            <Suspense fallback={<pre className="p-4 font-mono text-xs text-fg-muted">{r.yaml}</pre>}>
              <YamlView text={r.yaml} />
            </Suspense>
          </div>
        )}
        {r && tab === 'details' && (
          <Details
            client={client}
            hub={hub}
            target={target}
            r={r}
            onGo={go}
            // Right under the facts: the events list below is a fixed-height box.
            ports={hasForward?.(current.kind) && <PortsSection key={key} client={client} subject={{ ...r.ref, provider: target.provider, target: target.id }} />}
          />
        )}
      </div>
    </aside>
  )
}

function Details({ hub, target, r, onGo, ports }: { client: Client; hub: ViewHub; target: { provider: string; id: string }; r: Resource; onGo: (ref: Ref) => void; ports?: ReactNode }) {
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
      <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5">
        {r.facts.map((f) => (
          <div key={f.key} className="contents">
            <dt className="text-fg-muted">{detailLabel(f.key)}</dt>
            <dd className="min-w-0 font-mono text-[12px] break-all whitespace-pre-line">{f.value}</dd>
          </div>
        ))}
      </dl>
      {ports}
      {(groups.length > 0 || r.relationsError) && (
        <section aria-label={t('drawer.related')}>
          <h3 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{t('drawer.related')}</h3>
          {groups.map(([type, rels]) => (
            <div key={type} className="mb-2">
              <p className="text-xs text-fg-subtle">{relationLabel(type)}</p>
              <ul>
                {rels.map((rel) => (
                  <li key={`${rel.ref.kind}/${rel.ref.name}/${rel.ref.uid ?? ''}`}>
                    <button className="text-accent hover:underline" onClick={() => onGo({ ...rel.ref, provider: target.provider, target: target.id })}>
                      {rel.ref.kind}/{rel.ref.name}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
          {r.relationsError && <p className="text-xs text-warning">{t('drawer.relationsPartial', { error: r.relationsError })}</p>}
          {r.relationsTruncated && <p className="text-xs text-fg-subtle">{t('drawer.relationsTruncated')}</p>}
        </section>
      )}
      {r.ref.uid && r.ref.kind !== 'events' && <ObjectEvents hub={hub} subject={r.ref} />}
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
function ObjectEvents({ hub, subject }: { hub: ViewHub; subject: Ref }) {
  const query = useMemo(
    () => ({ kind: 'events', scope: subject.scope ? { mode: 'one' as const, name: subject.scope } : { mode: 'all' as const }, subject }),
    [subject],
  )
  const view = useView(hub, subject.provider, subject.target, query)
  const cols = view.kind?.columns ?? []
  return (
    <section aria-label={t('drawer.events')} className="flex h-72 flex-col">
      <h3 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
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
