import { useEffect, useMemo, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { AgentAuditEntry, AgentTarget, Target } from '../api/types'
import { classLabel, isMessageKey, t, type MessageKey } from '../i18n'
import { showNotice, useStore } from '../store'
import { copyText } from '../term/clipboard'
import { errorDetail } from '../errors'
import { focusMark, restoreFocus } from '../shortcuts'
import { Select } from '../components/Select'
import { ProviderIcon } from '../components/icons'
import { GrantEditor } from './GrantEditor'
import { agents, agentTargetKey, pendingOf, shownPending, useAgents, type AgentsTab } from './store'

/** The scope or object of a journal read folded over several (store.AuditSeveral). */
const several = '*'

function severalOr(v: string | undefined, key: MessageKey): string {
  return v === several ? t(key) : (v ?? '')
}

/** The status bar's "Agents": opens the panel; waiting plans bring their dialog back. */
export function AgentsIndicator() {
  const n = useAgents((s) => s.pending.length)
  const aside = useAgents((s) => s.pending.length > 0 && shownPending(s).length === 0)
  const failed = useAgents((s) => s.status?.state === 'failed')
  const open = useAgents((s) => s.panel !== null)
  return (
    <button
      className={['rounded px-1.5 hover:bg-hover hover:text-fg', n ? 'font-semibold text-warning' : failed ? 'text-danger' : 'text-fg-muted'].join(' ')}
      onClick={() => (aside ? agents.bringBack() : agents.showPanel(open ? null : 'grants'))}
      aria-expanded={open}
      title={n ? t('agents.waitingHint') : t('agents.panel')}
    >
      {n ? t('agents.indicatorPending', { n }) : t('agents.indicator')}
    </button>
  )
}

interface Row {
  key: string
  provider: string
  target: string
  title: string
  groupTitle: string
  exists: boolean
  saved: AgentTarget | null
}

/** Every target of the configuration, then targets with grants that are gone from it. */
function useRows(): Row[] {
  const view = useStore((s) => s.view)
  const saved = useAgents((s) => s.targets)
  return useMemo(() => {
    const byKey = new Map(saved.map((x) => [agentTargetKey(x.provider, x.target), x]))
    const rows: Row[] = []
    for (const g of view?.groups ?? []) {
      for (const x of g.targets as Target[]) {
        const key = agentTargetKey(x.provider, x.id)
        rows.push({ key, provider: x.provider, target: x.id, title: x.title, groupTitle: g.title, exists: true, saved: byKey.get(key) ?? null })
        byKey.delete(key)
      }
    }
    for (const [key, x] of byKey) {
      const g = view?.groups.find((gr) => gr.provider === x.provider)
      rows.push({ key, provider: x.provider, target: x.target, title: x.title || x.target, groupTitle: g?.title ?? x.provider, exists: false, saved: x })
    }
    return rows
  }, [view, saved])
}

export function AgentsPanel({ client }: { client: Client }) {
  const tab = useAgents((s) => s.panel)
  return tab ? <Panel client={client} tab={tab} /> : null
}

function Panel({ client, tab }: { client: Client; tab: AgentsTab }) {
  const [mark] = useState(focusMark)
  const box = useRef<HTMLElement>(null)
  useEffect(() => {
    box.current?.focus()
    return () => restoreFocus(mark)
  }, [mark])
  const tabs: AgentsTab[] = ['grants', 'journal']
  return (
    <div className="fixed inset-0 z-30 flex items-center justify-center bg-black/40 p-4" onMouseDown={(e) => e.target === e.currentTarget && agents.showPanel(null)}>
      <section
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={t('agents.panel')}
        tabIndex={-1}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && !e.defaultPrevented) {
            e.preventDefault()
            agents.showPanel(null)
          } else if (e.key === 'Tab' && !e.defaultPrevented) {
            const controls = [...e.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), summary, [tabindex="0"]')]
              .filter((el) => el.getClientRects().length > 0)
            const first = controls[0]
            const last = controls[controls.length - 1]
            if (!first) {
              e.preventDefault()
            } else if (e.shiftKey && (document.activeElement === first || document.activeElement === e.currentTarget)) {
              e.preventDefault()
              last.focus()
            } else if (!e.shiftKey && document.activeElement === last) {
              e.preventDefault()
              first.focus()
            }
          }
        }}
        className="agents-dialog"
      >
        <header className="agents-heading">
          <h2 className="resource-title">{t('agents.panel')}</h2>
          <div role="tablist" aria-label={t('agents.panel')} className="agents-tabs">
            {tabs.map((x) => (
              <button
                key={x}
                role="tab"
                aria-selected={tab === x}
                className="agents-tab"
                onClick={() => agents.showPanel(x)}
              >
                {t(`agents.tab.${x}` as MessageKey)}
              </button>
            ))}
          </div>
          <button className="context-info-trigger ml-auto" onClick={() => agents.showPanel(null)} aria-label={t('drawer.close')}>
            ×
          </button>
        </header>
        <SocketStatus />
        {tab === 'grants' ? <GrantsTab client={client} /> : <JournalTab client={client} />}
      </section>
    </div>
  )
}

function SocketStatus() {
  const status = useAgents((s) => s.status)
  const error = useAgents((s) => s.error)
  const mode = useStore((s) => (s.info?.mode === 'desktop' ? 'desktop' : 'browser'))
  const [copied, setCopied] = useState(false)
  const dot = status?.state === 'serving' ? 'bg-success' : status?.state === 'other_instance' ? 'bg-warning' : 'bg-danger'
  return (
    <details className="agents-connection" open={!!error || status?.state === 'failed'}>
      <summary>
        <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${status ? dot : 'bg-fg-subtle'}`} />
        <span className="min-w-0 flex-1" role="status">
          {status?.state === 'serving' ? t('agents.connected') : !status ? t('agents.state.loading') : t(`agents.state.${status.state}` as MessageKey, { socket: status.socket, error: status.error ?? '' })}
        </span>
        <span className="agents-connection-label">{t('agents.connectionHelp')}</span>
        <span aria-hidden className="agents-chevron">›</span>
      </summary>
      <div className="agents-connection-body">
        <p className="text-fg-muted">{t('agents.about')}</p>
        {status?.state === 'serving' && <p className="break-all text-fg-subtle">{t('agents.state.serving', { socket: status.socket })}</p>}
        {status && (
          <div className="flex min-w-0 items-center gap-2">
            <code className="min-w-0 flex-1 select-all overflow-x-auto whitespace-nowrap rounded-md border border-line bg-app px-2 py-1.5 font-mono text-xs" title={status.instruction} data-instruction>
              {status.instruction}
            </code>
            <button
              type="button"
              className="agents-button"
              onClick={() => {
                void copyText(status.instruction, mode)
                setCopied(true)
                setTimeout(() => setCopied(false), 1500)
              }}
            >
              {copied ? t('agents.copied') : t('agents.copy')}
            </button>
          </div>
        )}
      </div>
      {error && <p role="alert" className="px-4 pb-2 text-danger">{error}</p>}
    </details>
  )
}

function GrantsTab({ client }: { client: Client }) {
  const rows = useRows()
  const contextScopes = useAgents((s) => s.contextScopes)
  const chosenKey = useAgents((s) => s.chosen)
  const selected = useStore((s) => s.view?.selected)
  const pending = useAgents((s) => s.pending)
  const anyGranted = useAgents((s) => s.targets.some((x) => (x.grants.length || x.groups?.length || x.disabledScopes?.length)))
  const [asking, setAsking] = useState(false)
  const key = chosenKey && rows.some((r) => r.key === chosenKey) ? chosenKey : selected ? agentTargetKey(selected.provider, selected.id) : (rows[0]?.key ?? null)
  const row = rows.find((r) => r.key === key) ?? null

  const revokeAll = async () => {
    setAsking(false)
    try {
      await client.revokeAllAgentGrants()
      showNotice(t('agents.revokedAll'))
    } catch (e) {
      showNotice(t('agents.saveFailed', { error: errorDetail(e) }), 10_000)
    }
  }

  return (
    <div className="flex min-h-0 flex-1">
      <nav aria-label={t('agents.targets')} className="agents-targets">
        <ul className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
          {rows.map((r, i) => {
            const head = i === 0 || rows[i - 1].groupTitle !== r.groupTitle
            const n = (r.saved?.grants.length ?? 0) + (r.saved?.groups ?? []).reduce((sum, g) => sum + g.grants.length, 0)
            const waiting = pendingOf(pending, r.provider, r.target)
            return (
              <li key={r.key}>
                {head && (
                  <h3 className="sidebar-group-heading">
                    <ProviderIcon provider={r.provider} className="h-3 w-3" />
                    {r.groupTitle}
                  </h3>
                )}
                <button
                  type="button"
                  aria-current={r.key === key}
                  onClick={() => agents.choose(r.key)}
                  className="agents-target"
                >
                  <span className="w-full truncate">{r.title}</span>
                  <span className="flex flex-wrap gap-1 text-xs">
                    {n > 0 && <span className="text-accent">{t('agents.granted', { n })}</span>}
                    {r.saved?.observed && <span className="text-warning">{t('agents.suspendedBadge')}</span>}
                    {!r.exists && <span className="text-warning">{t('agents.missing')}</span>}
                    {waiting > 0 && <span className="font-semibold text-warning">{t('agents.waiting', { n: waiting })}</span>}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
        <div className="border-t border-line p-2 text-xs">
          {asking ? (
            <div role="alertdialog" aria-label={t('agents.revokeAllAsk')} className="flex flex-col gap-1.5">
              <span>{t('agents.revokeAllAsk')}</span>
              <span className="flex gap-2">
                <button type="button" className="rounded-md bg-danger px-2 py-0.5 text-white" onClick={() => void revokeAll()}>
                  {t('agents.revokeYes')}
                </button>
                <button type="button" className="rounded-md px-2 py-0.5 text-fg-muted hover:bg-hover" onClick={() => setAsking(false)}>
                  {t('agents.revokeNo')}
                </button>
              </span>
            </div>
          ) : (
            <button type="button" disabled={!anyGranted} className="agents-button w-full text-fg-muted hover:text-danger" onClick={() => setAsking(true)}>
              {t('agents.revokeAll')}
            </button>
          )}
        </div>
      </nav>
      {row ? (
        <GrantEditor contextScopes={contextScopes} key={row.key} client={client} provider={row.provider} target={row.target} title={row.title} saved={row.saved} exists={row.exists} />
      ) : (
        <p className="p-4 text-xs text-fg-subtle">{t('agents.pickTarget')}</p>
      )}
    </div>
  )
}

/** What a record says: a read, an intent, an outcome in words, a refusal with its class. */
export function outcomeText(e: AgentAuditEntry): string {
  if (e.phase === 'read' || e.phase === 'intent') return t(`agents.phase.${e.phase}`)
  if (e.phase === 'refused') return `${t('agents.phase.refused')}${e.outcome ? ` · ${classLabel(e.outcome)}` : ''}`
  const k = `agents.outcome.${e.outcome}`
  return isMessageKey(k) ? t(k) : (e.outcome ?? t('agents.phase.outcome'))
}

const PAGE = 200
/** The open journal reloads its first page at most this often. */
const RELOAD_MS = 1000

const timeFormat = () => new Intl.DateTimeFormat(document.documentElement.lang || undefined, { dateStyle: 'short', timeStyle: 'medium' })

function JournalTab({ client }: { client: Client }) {
  const rows = useRows()
  const tick = useAgents((s) => s.auditTick)
  const [agent, setAgent] = useState('')
  const [target, setTarget] = useState('')
  const [entries, setEntries] = useState<AgentAuditEntry[]>([])
  const [more, setMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [agentsSeen, setAgentsSeen] = useState<string[]>([])
  const fmt = useMemo(() => timeFormat(), [])
  const filter = useMemo(() => {
    const r = rows.find((x) => x.key === target)
    return { agent: agent || undefined, provider: r?.provider, target: r?.target }
  }, [agent, target, rows])

  // The first page: again on every change of the journal (at most once a
  // second); older pages already read stay below it. Another filter
  // starts over.
  const shown = useRef<AgentAuditEntry[]>([])
  const olderMore = useRef(false)
  const lastLoad = useRef(0)
  const lastFilter = useRef('')
  useEffect(() => {
    const key = JSON.stringify(filter)
    let wait = Math.max(0, lastLoad.current + RELOAD_MS - Date.now())
    if (key !== lastFilter.current) {
      lastFilter.current = key
      shown.current = []
      olderMore.current = false
      wait = 0
    }
    let live = true
    const timer = setTimeout(() => {
      lastLoad.current = Date.now()
      client.listAgentAudit({ ...filter, limit: PAGE }).then(
        (page) => {
          if (!live) return
          const full = page.length === PAGE
          const oldest = page.length ? page[page.length - 1].id : Infinity
          const keep = full ? shown.current.filter((e) => e.id < oldest) : []
          shown.current = [...page, ...keep]
          setEntries(shown.current)
          setMore(full && (keep.length === 0 || olderMore.current))
          setAgentsSeen((seen) => [...new Set([...seen, ...page.map((e) => e.agent)])].sort())
          setLoading(false)
          setError(null)
        },
        (e) => {
          if (!live) return
          setLoading(false)
          setError(errorDetail(e))
        },
      )
    }, wait)
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [client, filter, tick])

  const loadMore = () => {
    const before = shown.current[shown.current.length - 1]?.id
    if (!before) return
    const key = lastFilter.current
    client.listAgentAudit({ ...filter, before, limit: PAGE }).then(
      (page) => {
        if (key !== lastFilter.current) return
        shown.current = [...shown.current, ...page.filter((e) => e.id < before)]
        olderMore.current = page.length === PAGE
        setEntries(shown.current)
        setMore(olderMore.current)
        setAgentsSeen((seen) => [...new Set([...seen, ...page.map((e) => e.agent)])].sort())
      },
      (e) => setError(errorDetail(e)),
    )
  }

  const agentOptions = [{ value: '', label: t('agents.journal.anyAgent') }, ...[...new Set([...agentsSeen, ...(agent ? [agent] : [])])].map((a) => ({ value: a, label: a }))]
  const targetOptions = [{ value: '', label: t('agents.journal.anyTarget') }, ...rows.map((r) => ({ value: r.key, label: `${r.title} · ${r.groupTitle}` }))]
  const titleOf = (e: AgentAuditEntry) => rows.find((r) => r.provider === e.provider && r.target === e.target)?.title ?? e.target ?? ''

  return (
    <div className="flex min-h-0 flex-1 flex-col text-xs">
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-1.5">
        <Select label={t('agents.journal.agent')} value={agent} options={agentOptions} onChange={setAgent} />
        <Select label={t('agents.journal.target')} value={target} options={targetOptions} onChange={setTarget} search />
      </div>
      {error && <p role="alert" className="px-3 py-1 text-danger">{error}</p>}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <table aria-label={t('agents.tab.journal')} className="w-full border-collapse">
          <thead className="sticky top-0 bg-panel text-left text-fg-subtle">
            <tr>
              <th className="px-3 py-1 font-normal">{t('agents.journal.time')}</th>
              <th className="px-2 py-1 font-normal">{t('agents.journal.agent')}</th>
              <th className="px-2 py-1 font-normal">{t('agents.journal.method')}</th>
              <th className="px-2 py-1 font-normal">{t('agents.journal.object')}</th>
              <th className="px-2 py-1 font-normal">{t('agents.journal.outcome')}</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => (
              <tr key={e.id} className="border-t border-line align-top">
                <td className="whitespace-nowrap px-3 py-1 text-fg-muted">{fmt.format(new Date(e.at))}</td>
                <td className="px-2 py-1">
                  <span className="break-all">{e.agent}</span> <span className="text-fg-subtle">({t('agents.unverified')})</span>
                </td>
                <td className="px-2 py-1 font-mono">
                  {e.method}
                  {e.verb && <span className="text-fg-subtle"> · {e.verb}</span>}
                </td>
                <td className="px-2 py-1">
                  {/* The object names its scope already; a folded read of several says so ("*"). */}
                  <span className="break-all">{[titleOf(e), e.object && e.object !== several ? '' : severalOr(e.scope, 'agents.journal.severalScopes'), severalOr(e.object, 'agents.journal.severalObjects')].filter(Boolean).join(' · ')}</span>
                  {e.detail && <span className="block break-all text-fg-subtle">{e.detail}</span>}
                </td>
                <td className={['px-2 py-1', e.phase === 'refused' || (e.phase === 'outcome' && e.outcome !== 'done') ? 'text-warning' : ''].join(' ')}>
                  {outcomeText(e)}
                  {e.count > 1 && <span className="text-fg-subtle"> {t('agents.journal.count', { n: e.count })}</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {loading && <p className="px-3 py-3 text-fg-subtle">{t('agents.journal.loading')}</p>}
        {!loading && !entries.length && !error && <p className="px-3 py-3 text-fg-subtle">{t('agents.journal.empty')}</p>}
        {more && (
          <button type="button" className="m-2 rounded-md border border-line px-2 py-0.5 hover:bg-hover" onClick={loadMore}>
            {t('agents.journal.more')}
          </button>
        )}
      </div>
    </div>
  )
}
