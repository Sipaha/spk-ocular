import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ApiError, type Client } from '../api/client'
import type { EditPlan, EditPrepareRequest, EditResult } from '../api/types'
import { classLabel, messageText, t } from '../i18n'
import { refTitle } from '../refs'
import { useScopeWords } from '../scopeNames'
import { EditDiff } from './EditDiff'
import { withoutVersion } from './text'
import { errorDetail } from '../errors'

/** An edit to review: the object, its signed base, the text as given and as edited. */
export interface EditReview extends EditPrepareRequest {
  /** The kind's name for one object (the provider's). */
  kindTitle: string
}

interface Props {
  client: Client
  req: EditReview
  /** Back to the text: the edits stay. */
  onBack: () => void
  /** Written: the editor closes. */
  onDone: (res: EditResult) => void
}

type Outcome =
  | { type: 'prepareFailed'; text: string }
  | { type: 'conflict'; text: string }
  | { type: 'unknown'; text: string }
  | { type: 'failed'; text: string }

const btn = 'rounded-md px-3 py-1 outline-none focus:ring-2 focus:ring-accent focus:ring-offset-2 focus:ring-offset-panel disabled:opacity-50'

const codeOf = (e: unknown) => (e instanceof ApiError ? e.code : 'internal')

/** What a write's rejection means for the user. */
function outcomeOf(e: unknown): Outcome {
  const code = codeOf(e)
  // No coded answer (the connection failed): the write may have been applied.
  if (code === 'unknown' || !(e instanceof ApiError) || e.transport) return { type: 'unknown', text: `${t('edit.unknown')} ${errorDetail(e)}` }
  if (code === 'conflict') return { type: 'conflict', text: t('edit.conflict', { detail: errorDetail(e) }) }
  return { type: 'failed', text: t('edit.failed', { class: classLabel(code), detail: errorDetail(e) }) }
}

/**
 * The review of an edit: where, what the server (or, unproven, a local
 * overlay) says the object becomes, warnings, permission and the diff; then
 * one write of exactly what was reviewed. A conflict is reviewed again; a
 * result other than the expected one is said.
 */
export function EditDialog({ client, req, onBack, onDone }: Props) {
  const scopeWords = useScopeWords(req.ref.provider)
  const [plan, setPlan] = useState<EditPlan | null>(null)
  const [busy, setBusy] = useState<'prepare' | 'run' | null>('prepare')
  const [outcome, setOutcome] = useState<Outcome | null>(null)
  // A write was sent for this plan: never another one (a new review makes a new plan).
  const [sent, setSent] = useState(false)
  // Written, and the object differs from the expected result.
  const [written, setWritten] = useState<EditResult | null>(null)
  const gen = useRef(0)
  const lock = useRef(false)
  const live = useRef(true)
  const box = useRef<HTMLDivElement>(null)
  const applyRef = useRef<HTMLButtonElement>(null)
  const backRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    live.current = true
    return () => {
      live.current = false
    }
  }, [])

  // fetchPlan reads a plan; state changes only when it answers, and only
  // if no later review was started meanwhile.
  const fetchPlan = () => {
    const g = ++gen.current
    lock.current = false
    client.prepareEdit({ ref: req.ref, base: req.base, original: req.original, edited: req.edited }).then(
      (pl) => {
        if (!live.current || g !== gen.current) return
        setPlan(pl)
        setBusy(null)
      },
      (e) => {
        if (!live.current || g !== gen.current) return
        setBusy(null)
        setOutcome({ type: 'prepareFailed', text: t('edit.prepareFailed', { class: classLabel(codeOf(e)), detail: errorDetail(e) }) })
      },
    )
  }
  const prepare = () => {
    setBusy('prepare')
    setOutcome(null)
    setSent(false)
    setPlan(null)
    fetchPlan()
  }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => fetchPlan(), [])

  const canRun = !!plan?.token && plan.changed && !busy && !sent && !plan.unavailable && plan.rights.state !== 'denied'
  const destructive = !!plan?.destructive

  // Focus: Apply on a plan that can be written; Back on a destructive one
  // and otherwise. While busy the dialog itself holds it: keys (typing,
  // a held Ctrl+Enter) must not reach the editor behind it.
  useLayoutEffect(() => {
    if (busy) {
      if (!box.current?.contains(document.activeElement)) box.current?.focus()
      return
    }
    if (canRun && !destructive) applyRef.current?.focus()
    else if (!box.current?.contains(document.activeElement) || document.activeElement === box.current || destructive) backRef.current?.focus()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan, busy, written])

  const run = async () => {
    if (lock.current || !canRun || !plan?.token) return
    lock.current = true
    setSent(true)
    setBusy('run')
    setOutcome(null)
    try {
      const res = await client.runEdit({ ref: req.ref, base: req.base, original: req.original, edited: req.edited, token: plan.token })
      if (!live.current) return
      setBusy(null)
      if (res.actual !== undefined && withoutVersion(res.actual) !== withoutVersion(plan.after)) setWritten(res)
      else onDone(res)
    } catch (e) {
      if (!live.current) return
      setBusy(null)
      setOutcome(outcomeOf(e))
    }
  }

  const back = () => {
    if (busy === 'run') return
    if (written) onDone(written)
    else onBack()
  }

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      e.preventDefault()
      back()
    } else if (e.key === 'Enter' && e.repeat) {
      // Held from the key that asked for the review: never a confirmation.
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

  const where = plan?.where
  const name = refTitle(where?.ref ?? req.ref)
  const scope = where?.ref.scope ?? req.ref.scope

  return (
    <div className="fixed inset-0 z-30 flex items-start justify-center bg-black/40 pt-10" onMouseDown={(e) => e.target === e.currentTarget && back()}>
      <div
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={`${t('edit.title')} ${name}`}
        aria-busy={!!busy}
        tabIndex={-1}
        onKeyDown={onKey}
        className="flex max-h-[88%] w-[min(900px,94%)] flex-col gap-3 overflow-y-auto rounded-lg border border-line bg-panel p-4 shadow-2xl outline-none"
      >
        <h2 className="font-semibold">
          <span className={destructive ? 'text-danger' : ''}>{t('edit.title')}</span> · <span className="font-mono text-fg-muted">{name}</span>
        </h2>

        <dl aria-label="where" className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-0.5 rounded-md border border-line px-3 py-2 text-xs">
          <dt className="text-fg-muted">{t('action.context')}</dt>
          <dd className="min-w-0 break-all font-mono">{where?.targetTitle ?? req.ref.target}</dd>
          {where?.endpoint && (
            <>
              <dt className="text-fg-muted">{t('action.server')}</dt>
              <dd className="min-w-0 break-all font-mono">{where.endpoint}</dd>
            </>
          )}
          {scope && (
            <>
              <dt className="text-fg-muted">{scopeWords.singular}</dt>
              <dd className="min-w-0 break-all font-mono">{scope}</dd>
            </>
          )}
          <dt className="text-fg-muted">{t('action.kind')}</dt>
          <dd className="min-w-0 break-all font-mono">{req.kindTitle}</dd>
          <dt className="text-fg-muted">{t('action.name')}</dt>
          <dd className="min-w-0 break-all font-mono">{name}</dd>
        </dl>

        {busy === 'prepare' && <p className="text-fg-subtle">{t('edit.preparing')}</p>}

        {plan && !written && busy !== 'prepare' && (
          <>
            {!plan.changed ? (
              <p role="status" className="rounded-md border border-line px-3 py-2 text-fg-muted">
                {t('edit.noChanges')}
              </p>
            ) : (
              <p aria-label={t('edit.mode')} className={plan.checked ? 'text-fg-muted' : 'text-warning'}>
                {plan.checked ? t('edit.checked') : t('edit.local')}
              </p>
            )}
            {plan.unavailable && (
              <p role="alert" className="rounded-md bg-warning/10 px-3 py-2 text-warning">
                {t('edit.unavailable', { reason: messageText(plan.unavailable) })}
              </p>
            )}
            {!!plan.warnings?.length && (
              <section aria-label={t('action.warnings')} className="text-warning">
                <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider">{t('action.warnings')}</h3>
                <ul className="list-disc space-y-0.5 pl-5">
                  {plan.warnings.map((x, i) => (
                    <li key={i}>{messageText(x)}</li>
                  ))}
                </ul>
              </section>
            )}
            {plan.changed && (
              <p aria-label={t('action.rights')} className={plan.rights.state === 'denied' ? 'text-danger' : plan.rights.state === 'unknown' ? 'text-warning' : 'text-fg-muted'}>
                {t('action.rights')}:{' '}
                {plan.rights.state === 'allowed'
                  ? t('action.rightsAllowed')
                  : plan.rights.state === 'denied'
                    ? t('action.rightsDenied', { reason: plan.rights.reason ?? '' })
                    : t('action.rightsUnknown') + (plan.rights.reason ? ` (${plan.rights.reason})` : '')}
              </p>
            )}
            {plan.changed && !plan.unavailable && <EditDiff before={plan.before} after={plan.after} />}
          </>
        )}

        {written && plan && (
          <>
            <p role="status" className="rounded-md bg-warning/10 px-3 py-2 text-warning">
              {written.message} · {t('edit.mismatch')}
            </p>
            <EditDiff before={withoutVersion(plan.after)} after={withoutVersion(written.actual ?? '')} label={t('edit.mismatchDiff')} />
          </>
        )}

        {busy === 'run' && <p className="text-fg-subtle">{t('edit.running')}</p>}
        {outcome && (
          <p role="alert" className={['rounded-md px-3 py-2', outcome.type === 'unknown' || outcome.type === 'conflict' ? 'bg-warning/10 text-warning' : 'bg-danger/10 text-danger'].join(' ')}>
            {outcome.text}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <button ref={backRef} type="button" disabled={busy === 'run'} className={`${btn} text-fg-muted hover:bg-hover`} onClick={back}>
            {written ? t('edit.close') : t('edit.back')}
          </button>
          {(outcome?.type === 'conflict' || outcome?.type === 'prepareFailed') && (
            <button type="button" className={`${btn} border border-line hover:bg-hover`} onClick={prepare}>
              {t('edit.reviewAgain')}
            </button>
          )}
          {!written && plan?.changed && !sent && (
            <button ref={applyRef} type="button" disabled={!canRun} onClick={() => void run()} className={[btn, destructive ? 'bg-danger text-white' : 'bg-accent text-accent-fg'].join(' ')}>
              {t('edit.apply')}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
