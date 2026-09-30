import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ApiError, type Client } from '../api/client'
import type { ActionDescriptor, ActionParams, ActionPart, ActionPlan, ActionResult, Ref } from '../api/types'
import { actionLabel, classLabel, messageText, t } from '../i18n'
import { refTitle } from '../refs'
import { useScopeWords } from '../scopeNames'
import { showNotice } from '../store'
import { focusMark, restoreFocus } from '../shortcuts'

/** An action chosen on an object: the dialog reviews it, then runs it. */
export interface ActionRequest {
  ref: Ref
  action: ActionDescriptor
  /** The kind's name for one object (the provider's). */
  kindTitle: string
}

interface Props {
  client: Client
  req: ActionRequest
  onClose: () => void
  /** How long a run may take before its outcome is called unknown. */
  runTimeoutMs?: number
}

type Outcome =
  | { type: 'prepareFailed'; text: string }
  | { type: 'conflict'; text: string }
  | { type: 'unknown'; text: string }
  | { type: 'failed'; text: string }
  /** An answer after the timeout said it was done. */
  | { type: 'lateDone'; text: string }
  /** Some parts were done, then one was refused or its outcome is unknown. */
  | { type: 'partial'; text: string; parts: ActionPart[]; unknown: boolean }

const RUN_TIMEOUT_MS = 60_000

// A visible focus on every button: WebKitGTK does not show :focus-visible
// for focus set by script (the initial focus, the Tab trap), and which
// button Enter presses must be seen.
const btn = 'rounded-md px-3 py-1 outline-none focus:ring-2 focus:ring-accent focus:ring-offset-2 focus:ring-offset-panel disabled:opacity-50'

class RunTimeout extends Error {}

const partClass: Record<ActionPart['outcome'], string> = {
  done: 'text-success',
  refused: 'text-danger',
  unknown: 'text-warning',
  skipped: 'text-fg-subtle',
}

/** What a run's rejection means for the user. */
function outcomeOf(e: unknown): Outcome {
  const code = codeOf(e)
  // No coded answer (the connection failed): the request may have been applied.
  const transport = !(e instanceof ApiError) || e.transport
  return code === 'unknown' || transport
    ? { type: 'unknown', text: `${t('action.unknown')} ${detailOf(e)}` }
    : code === 'conflict'
      ? { type: 'conflict', text: t('action.conflict', { detail: detailOf(e) }) }
      : { type: 'failed', text: t('action.failed', { class: classLabel(code), detail: detailOf(e) }) }
}

/** "2 of 3 done; 1 outcome unknown; 1 not run". */
export function partsSummary(parts: ActionPart[]): string {
  const n = (o: ActionPart['outcome']) => parts.filter((p) => p.outcome === o).length
  const out = [t('action.partsDone', { done: n('done'), total: parts.length })]
  if (n('refused')) out.push(t('action.partsRefused', { n: n('refused') }))
  if (n('unknown')) out.push(t('action.partsUnknown', { n: n('unknown') }))
  if (n('skipped')) out.push(t('action.partsSkipped', { n: n('skipped') }))
  return out.join('; ')
}

/** A result that did not finish (null: done). */
function partialOf(res: ActionResult): Extract<Outcome, { type: 'partial' }> | null {
  if (!res.outcome || res.outcome === 'done') return null
  const unknown = res.outcome === 'unknown'
  return { type: 'partial', text: t(unknown ? 'action.partialUnknown' : 'action.partialRefused'), parts: res.parts ?? [], unknown }
}

const detailOf = (e: unknown) => (e instanceof ApiError ? e.detail || e.code : e instanceof Error ? e.message : String(e))
const codeOf = (e: unknown) => (e instanceof ApiError ? e.code : 'internal')

/**
 * The confirmation of an action: where (context, server, namespace, kind,
 * name), what happens, the permission check; a count is chosen first and
 * reviewed before it can run. One run per confirmation; while it runs the
 * dialog stays.
 */
export function ActionDialog({ client, req, onClose, runTimeoutMs = RUN_TIMEOUT_MS }: Props) {
  const { ref, action, kindTitle } = req
  // The object's own provider names its scopes (not the target selected now).
  const scopeWords = useScopeWords(ref.provider)
  const param = action.param
  const [plan, setPlan] = useState<ActionPlan | null>(null)
  const [count, setCount] = useState('')
  const [countError, setCountError] = useState<string | null>(null)
  const [busy, setBusy] = useState<'prepare' | 'run' | null>('prepare')
  const [outcome, setOutcome] = useState<Outcome | null>(null)
  // A run was sent for this plan: never another one (a new review makes a new plan).
  const [sent, setSent] = useState(false)
  const gen = useRef(0)
  const lock = useRef(false)
  const live = useRef(true)
  const box = useRef<HTMLFormElement>(null)
  // Runs are numbered apart from reviews; lateFor: the run whose timeout the
  // dialog shows as "unknown" (only its late answer may replace it).
  const runSeq = useRef(0)
  const lateFor = useRef(0)
  const confirmRef = useRef<HTMLButtonElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const countRef = useRef<HTMLInputElement>(null)
  const [mark] = useState(focusMark)

  useEffect(() => {
    live.current = true
    return () => {
      live.current = false
      restoreFocus(mark) // the row may be gone (deleted): then the table
    }
  }, [mark])

  // fetchPlan reads a plan; state changes only when it answers, and only
  // if no later review was started meanwhile.
  const fetchPlan = (p: ActionParams) => {
    const g = ++gen.current
    lock.current = false
    client.prepareAction(ref, action.id, p).then(
      (pl) => {
        if (!live.current || g !== gen.current) return // a later review (or none) owns the dialog
        setPlan(pl)
        setBusy(null)
        if (param && p.count === undefined && pl.current !== undefined) setCount(String(pl.current))
      },
      (e) => {
        if (!live.current || g !== gen.current) return
        setPlan(null)
        setBusy(null)
        setOutcome({ type: 'prepareFailed', text: t('action.prepareFailed', { class: classLabel(codeOf(e)), detail: detailOf(e) }) })
      },
    )
  }

  const prepare = (p: ActionParams) => {
    lateFor.current = 0
    setBusy('prepare')
    setOutcome(null)
    setSent(false)
    fetchPlan(p)
  }

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => fetchPlan({}), [])

  const planned = plan?.params.count
  // A count other than the reviewed one must be reviewed first.
  const reviewed = !param || (planned !== undefined && count.trim() === String(planned))
  const canRun = !!plan && reviewed && !busy && !sent && !plan.unavailable && plan.rights.state !== 'denied'
  const destructive = !!plan?.destructive

  // Focus: the count while choosing it; on a reviewed plan the confirmation,
  // or Cancel when it is destructive.
  useLayoutEffect(() => {
    if (busy) return
    if (param && !reviewed) {
      countRef.current?.focus()
      countRef.current?.select()
    } else if (plan && canRun && !destructive) confirmRef.current?.focus()
    else if (plan && destructive) cancelRef.current?.focus() // also from the count: a destructive plan starts at Cancel
    else if (!box.current?.contains(document.activeElement) || document.activeElement === box.current) cancelRef.current?.focus()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan, busy])

  const review = () => {
    if (!param || busy === 'run') return
    const s = count.trim()
    const n = Number(s)
    if (!/^\d+$/.test(s) || n < param.min || n > param.max) {
      setCountError(t('action.countRange', { min: param.min, max: param.max }))
      return
    }
    setCountError(null)
    prepare({ count: n })
  }

  const run = async () => {
    if (lock.current || !canRun || !plan) return
    lock.current = true
    setSent(true)
    setBusy('run')
    setOutcome(null)
    const op = ++runSeq.current
    const where = `${plan.where.targetTitle} · `
    let timer: ReturnType<typeof setTimeout> | undefined
    const answer = client.runAction(plan)
    let timedOut = false
    // After the timeout the answer still comes: it replaces this run's
    // "unknown" in its open dialog, else it is a notice with the target. It
    // never touches a newer confirmation and sends nothing again.
    const lateAnswer = (out: Outcome, message?: string) => {
      if (!timedOut) return
      if (live.current && lateFor.current === op) {
        lateFor.current = 0
        setOutcome(out)
        if (message) showNotice(message)
      } else showNotice(`${where}${out.type === 'lateDone' ? out.text : `${actionLabel(action)} ${refTitle(plan.where.ref)}: ${out.text}`}`, 10_000)
    }
    // What a result says, for a notice: the sum of its parts or its message.
    const told = (res: ActionResult) => (res.parts?.length ? `${actionLabel(action)} ${refTitle(plan.where.ref)}: ${partsSummary(res.parts)}` : res.message)
    answer.then(
      (res) => {
        const partial = partialOf(res)
        if (!partial) return lateAnswer({ type: 'lateDone', text: t('action.lateDone', { message: told(res) }) }, told(res))
        if (!timedOut) return
        if (live.current && lateFor.current === op) {
          lateFor.current = 0
          setOutcome({ ...partial, text: t('action.late', { text: partial.text }) })
          showNotice(told(res))
        } else showNotice(`${where}${actionLabel(action)} ${refTitle(plan.where.ref)}: ${t('action.late', { text: partsSummary(partial.parts) })}`, 10_000)
      },
      (e) => lateAnswer({ ...outcomeOf(e), text: t('action.late', { text: outcomeOf(e).text }) }),
    )
    try {
      const res = await Promise.race([
        answer,
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => {
            timedOut = true
            reject(new RunTimeout())
          }, runTimeoutMs)
        }),
      ])
      // Told even when the dialog is gone (another target was chosen meanwhile).
      const partial = partialOf(res)
      if (!live.current) showNotice(`${plan.where.targetTitle} · ${told(res)}`, partial ? 10_000 : undefined)
      else if (partial) {
        // The dialog stays: what was done and what not, part by part.
        showNotice(told(res), 10_000)
        setOutcome(partial)
        setBusy(null)
      } else {
        showNotice(told(res))
        onClose()
      }
    } catch (e) {
      const out: Outcome = e instanceof RunTimeout ? { type: 'unknown', text: t('action.timeout', { sec: Math.round(runTimeoutMs / 1000) }) } : outcomeOf(e)
      if (!live.current) {
        showNotice(`${where}${actionLabel(action)} ${refTitle(plan.where.ref)}: ${out.text}`, 10_000)
        return
      }
      if (e instanceof RunTimeout) lateFor.current = op
      setOutcome(out)
      setBusy(null)
    } finally {
      clearTimeout(timer)
    }
  }

  const close = () => {
    if (busy !== 'run') onClose()
  }

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      e.preventDefault()
      close()
    } else if (e.key === 'Enter' && e.repeat) {
      // Held from the key that opened the dialog: never a confirmation.
      e.preventDefault()
      e.stopPropagation()
    } else if (e.key === 'Tab') {
      const list = [...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)') ?? [])]
      if (!list.length) return
      const i = list.indexOf(document.activeElement as HTMLElement)
      const next = e.shiftKey ? (i <= 0 ? list.length - 1 : i - 1) : i < 0 || i === list.length - 1 ? 0 : i + 1
      e.preventDefault()
      list[next].focus()
    }
  }

  const where = plan?.where
  const name = refTitle(where?.ref ?? ref)
  const scope = where?.ref.scope ?? ref.scope
  const label = actionLabel(action)
  const unknownOrDone = outcome?.type === 'unknown' || outcome?.type === 'lateDone' || outcome?.type === 'partial'

  return (
    <div className="absolute inset-0 z-20 flex items-start justify-center bg-black/40 pt-20" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <form
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={`${label} ${name}`}
        aria-busy={!!busy}
        tabIndex={-1}
        onKeyDown={onKey}
        onSubmit={(e) => {
          e.preventDefault()
          review() // Enter in the count reviews; it never runs
        }}
        className="flex max-h-[80%] w-[min(600px,92%)] flex-col gap-3 overflow-y-auto rounded-lg border border-line bg-panel p-4 shadow-2xl outline-none"
      >
        <h2 className="font-semibold">
          <span className={destructive ? 'text-danger' : ''}>{label}</span> · <span className="font-mono text-fg-muted">{name}</span>
        </h2>

        <dl aria-label="where" className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-0.5 rounded-md border border-line px-3 py-2 text-xs">
          <dt className="text-fg-muted">{t('action.context')}</dt>
          <dd className="min-w-0 break-all font-mono">{where?.targetTitle ?? ref.target}</dd>
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
          <dd className="min-w-0 break-all font-mono">{kindTitle}</dd>
          <dt className="text-fg-muted">{t('action.name')}</dt>
          <dd className="min-w-0 break-all font-mono">{name}</dd>
        </dl>

        {param && (
          <label className="flex items-center gap-2 text-xs text-fg-muted">
            {t('action.count')}
            <input
              ref={countRef}
              inputMode="numeric"
              value={count}
              disabled={busy === 'run' || sent}
              onChange={(e) => {
                setCount(e.target.value)
                setCountError(null)
              }}
              aria-invalid={!!countError}
              className="w-24 rounded-md border border-line bg-app px-2 py-1 font-mono text-sm text-fg outline-none focus:border-accent"
            />
            {plan?.current !== undefined && <span>{t('action.countNow', { count: plan.current })}</span>}
            {countError && <span className="text-danger">{countError}</span>}
          </label>
        )}

        {busy === 'prepare' && <p className="text-fg-subtle">{t('action.preparing')}</p>}

        {plan && busy !== 'prepare' && reviewed && (
          <>
            {plan.unavailable && (
              <p role="alert" className="rounded-md bg-warning/10 px-3 py-2 text-warning">
                {t('action.unavailable', { reason: messageText(plan.unavailable) })}
              </p>
            )}
            {!!plan.effects?.length && (
              <section aria-label={t('action.effects')}>
                <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t('action.effects')}</h3>
                <ul className="list-disc space-y-0.5 pl-5">
                  {plan.effects.map((x, i) => (
                    <li key={i}>{messageText(x)}</li>
                  ))}
                </ul>
              </section>
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
            <p aria-label={t('action.rights')} className={plan.rights.state === 'denied' ? 'text-danger' : plan.rights.state === 'unknown' ? 'text-warning' : 'text-fg-muted'}>
              {t('action.rights')}:{' '}
              {plan.rights.state === 'allowed'
                ? t('action.rightsAllowed')
                : plan.rights.state === 'denied'
                  ? t('action.rightsDenied', { reason: plan.rights.reason ?? '' })
                  : t('action.rightsUnknown') + (plan.rights.reason ? ` (${plan.rights.reason})` : '')}
            </p>
          </>
        )}

        {busy === 'run' && <p className="text-fg-subtle">{t('action.running')}</p>}
        {outcome?.type === 'lateDone' ? (
          <p role="status" className="rounded-md bg-success/10 px-3 py-2 text-success">
            {outcome.text}
          </p>
        ) : outcome && (
          <p role="alert" className={['rounded-md px-3 py-2', outcome.type === 'unknown' || outcome.type === 'conflict' || (outcome.type === 'partial' && outcome.unknown) ? 'bg-warning/10 text-warning' : 'bg-danger/10 text-danger'].join(' ')}>
            {outcome.text}
          </p>
        )}
        {outcome?.type === 'partial' && outcome.parts.length > 0 && (
          <ul aria-label={t('action.parts')} className="space-y-0.5 rounded-md border border-line px-3 py-2 text-xs">
            {outcome.parts.map((p) => (
              <li key={p.id} className="flex gap-2">
                <span className="min-w-0 break-all font-mono">{p.title}</span>
                <span className={partClass[p.outcome]}>
                  {t(`action.part.${p.outcome}`)}
                  {p.message && p.outcome !== 'done' ? ` · ${p.message}` : ''}
                </span>
              </li>
            ))}
          </ul>
        )}

        <div className="flex justify-end gap-2">
          <button ref={cancelRef} type="button" disabled={busy === 'run'} className={`${btn} text-fg-muted hover:bg-hover`} onClick={close}>
            {sent || outcome?.type === 'prepareFailed' ? t('action.close') : t('action.cancel')}
          </button>
          {(outcome?.type === 'conflict' || outcome?.type === 'prepareFailed') && (
            <button type="button" className={`${btn} border border-line hover:bg-hover`} onClick={() => (param && count.trim() ? review() : prepare({}))}>
              {t('action.reviewAgain')}
            </button>
          )}
          {param && !reviewed && !sent && (
            // Enabled while a review is read: a disabled default button would swallow Enter in the count.
            <button type="submit" disabled={busy === 'run'} className={`${btn} bg-accent text-accent-fg`}>
              {t('action.review')}
            </button>
          )}
          {reviewed && !sent && !unknownOrDone && outcome?.type !== 'prepareFailed' && (
            <button
              ref={confirmRef}
              type="button"
              disabled={!canRun}
              onClick={() => void run()}
              className={[btn, destructive ? 'bg-danger text-white' : 'bg-accent text-accent-fg'].join(' ')}
            >
              {label}
            </button>
          )}
        </div>
      </form>
    </div>
  )
}
