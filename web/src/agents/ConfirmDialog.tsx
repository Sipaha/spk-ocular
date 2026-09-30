import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ApiError, type Client } from '../api/client'
import type { AgentPending } from '../api/types'
import { actionLabel, messageText, t } from '../i18n'
import { refTitle } from '../refs'
import { scopeWordsOf } from '../scopeNames'
import { showNotice, useStore } from '../store'
import { errorDetail } from '../errors'
import { focusMark, restoreFocus } from '../shortcuts'
import { ActionLists } from '../actions/ActionLists'
import { EditDiff } from '../edit/EditDiff'
import { agents, shownPending, useAgents } from './store'

const btn = 'rounded-md px-3 py-1 outline-none focus:ring-2 focus:ring-accent focus:ring-offset-2 focus:ring-offset-panel disabled:opacity-50'

/** "9:05" left until t (never negative). */
export function timeLeft(until: string, now: number): string {
  const s = Math.max(0, Math.floor((Date.parse(until) - now) / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

/**
 * Agents' destructive plans waiting for the user, one at a time in the
 * order they came: the plan as the agent was shown it, "Yes" / "No".
 * Decided in another window or expired: the next one (or none).
 */
export function AgentConfirm({ client, reload }: { client: Client; reload: () => void }) {
  const pending = useAgents((s) => s.pending)
  const asideIds = useAgents((s) => s.asideIds)
  const queue = shownPending({ pending, asideIds })
  if (!queue.length) return null
  return (
    <Queue>
      <Dialog key={queue[0].id} client={client} p={queue[0]} more={queue.length - 1} reload={reload} />
    </Queue>
  )
}

/**
 * Lives while plans wait: the focus goes back where it was only when the
 * last dialog closes, not between two (the next dialog's "No" keeps it).
 */
function Queue({ children }: { children: React.ReactNode }) {
  const [mark] = useState(focusMark)
  useEffect(() => () => restoreFocus(mark), [mark])
  return children
}

function Dialog({ client, p, more, reload }: { client: Client; p: AgentPending; more: number; reload: () => void }) {
  const view = useStore((s) => s.view)
  const words = scopeWordsOf(view, p.provider)
  const [now, setNow] = useState(() => Date.now())
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const lock = useRef(false)
  const noRef = useRef<HTMLButtonElement>(null)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const i = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(i)
  }, [])
  // Destructive: the focus starts at "No".
  useLayoutEffect(() => noRef.current?.focus(), [])

  const expired = Date.parse(p.expires) <= now
  const decide = async (approve: boolean) => {
    if (lock.current) return
    lock.current = true
    setBusy(true)
    setError(null)
    try {
      await client.decideAgentPending(p.id, approve)
      showNotice(t(approve ? 'agents.confirm.approved' : 'agents.confirm.rejected', { agent: p.agent }))
    } catch (e) {
      lock.current = false
      setBusy(false)
      // Decided in another window or expired: the list moves on without it.
      if (e instanceof ApiError && e.code === 'gone') showNotice(t('agents.confirm.gone'))
      else setError(t('agents.confirm.failed', { error: errorDetail(e) }))
    }
    reload()
  }
  // Expired here: the backend's list drops it (asked once).
  useEffect(() => {
    if (expired) reload()
  }, [expired, reload])

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      agents.putAside()
    } else if (e.key === 'Enter' && e.repeat) {
      e.preventDefault()
      e.stopPropagation()
    } else if (e.key === 'Tab') {
      const list = [...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled)') ?? [])]
      if (!list.length) return
      const i = list.indexOf(document.activeElement as HTMLElement)
      const next = e.shiftKey ? (i <= 0 ? list.length - 1 : i - 1) : i < 0 || i === list.length - 1 ? 0 : i + 1
      e.preventDefault()
      list[next].focus()
    }
  }

  const plan = p.action
  const edit = p.edit
  const what = plan ? actionLabel(plan.action) : t('agents.confirm.edit')
  const where = plan?.where ?? edit?.where
  const ref = where?.ref ?? p.ref
  const warnings = plan?.warnings ?? edit?.warnings ?? []

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/50 pt-16">
      <div
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={t('agents.confirm.title', { agent: p.agent })}
        aria-busy={busy}
        onKeyDown={onKey}
        className="flex max-h-[84%] w-[min(760px,94%)] flex-col gap-3 rounded-lg border border-danger/60 bg-panel p-4 shadow-2xl outline-none"
      >
        <h2 className="font-semibold">
          {t('agents.confirm.title', { agent: p.agent })} <span className="text-xs font-normal text-fg-subtle">({t('agents.unverified')})</span>
        </h2>
        <p className="flex flex-wrap gap-x-3 text-xs text-fg-muted">
          <span className={expired ? 'text-danger' : ''}>{expired ? t('agents.confirm.expired') : t('agents.confirm.expires', { time: timeLeft(p.expires, now) })}</span>
          {more > 0 && <span>{t('agents.confirm.queue', { n: more })}</span>}
        </p>

        <dl aria-label="where" className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-0.5 rounded-md border border-line px-3 py-2 text-xs">
          <dt className="text-fg-muted">{t('action.context')}</dt>
          <dd className="min-w-0 break-all font-mono">{where?.targetTitle ?? p.targetTitle}</dd>
          {where?.endpoint && (
            <>
              <dt className="text-fg-muted">{t('action.server')}</dt>
              <dd className="min-w-0 break-all font-mono">{where.endpoint}</dd>
            </>
          )}
          {ref.scope && (
            <>
              <dt className="text-fg-muted">{words.singular}</dt>
              <dd className="min-w-0 break-all font-mono">{ref.scope}</dd>
            </>
          )}
          <dt className="text-fg-muted">{t('action.kind')}</dt>
          <dd className="min-w-0 break-all font-mono">{ref.kind}</dd>
          <dt className="text-fg-muted">{t('action.name')}</dt>
          <dd className="min-w-0 break-all font-mono">{refTitle(ref)}</dd>
        </dl>

        <div className="-mx-1 flex min-h-0 flex-col gap-3 overflow-y-auto px-1">
          <p className="font-semibold text-danger">
            {what}
            {plan?.params.count !== undefined && <span className="font-mono"> → {plan.params.count}</span>}
          </p>
          {!!plan?.effects?.length && (
            <section aria-label={t('action.effects')}>
              <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t('action.effects')}</h3>
              <ul className="list-disc space-y-0.5 pl-5">
                {plan.effects.map((x, i) => (
                  <li key={i}>{messageText(x)}</li>
                ))}
              </ul>
            </section>
          )}
          {warnings.length > 0 && (
            <section aria-label={t('action.warnings')} className="text-warning">
              <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider">{t('action.warnings')}</h3>
              <ul className="list-disc space-y-0.5 pl-5">
                {warnings.map((x, i) => (
                  <li key={i}>{messageText(x)}</li>
                ))}
              </ul>
            </section>
          )}
          {!!plan?.lists?.length && <ActionLists lists={plan.lists} />}
          {edit && <EditDiff before={edit.before} after={edit.after} />}
          <p className="text-xs text-fg-subtle">{t('agents.confirm.recheck')}</p>
        </div>

        {error && (
          <p role="alert" className="rounded-md bg-danger/10 px-3 py-2 text-danger">
            {error}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <button type="button" disabled={busy} className={`${btn} mr-auto text-fg-muted hover:bg-hover`} onClick={() => agents.putAside()}>
            {t('agents.confirm.later')}
          </button>
          <button ref={noRef} type="button" disabled={busy} className={`${btn} border border-line hover:bg-hover`} onClick={() => void decide(false)}>
            {t('agents.confirm.no')}
          </button>
          <button type="button" disabled={busy || expired} className={`${btn} bg-danger text-white`} onClick={() => void decide(true)}>
            {t('agents.confirm.yes')}
          </button>
        </div>
      </div>
    </div>
  )
}
