import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { ActionDescriptor, ActionParams, ActionPlan, ActionResult, Message, Ref } from '../api/types'
import { actionLabel, classLabel, messageText, t } from '../i18n'
import { errorDetail } from '../errors'
import { showNotice } from '../store'
import { focusMark, restoreFocus } from '../shortcuts'
import { useScopeWords } from '../scopeNames'
import { Portions } from './ActionLists'
import { PlanDetails } from './PlanDetails'
import { btn, codeOf, outcomeOf, partialOf, partsSummary, RunTimeout } from './outcome'
import { pool } from './bulk'

/** A marked row: its id (to unmark it once done) and its object. */
export interface BulkItem {
  id: string
  ref: Ref
}

/** One action chosen on several marked objects. */
export interface BulkRequest {
  items: BulkItem[]
  action: ActionDescriptor
  /** The kind's name for one object (the provider's). */
  kindTitleOf: (kind: string) => string
}

interface Props {
  client: Client
  req: BulkRequest
  onClose: () => void
  /** The rows whose run was done (they are unmarked). */
  onDone: (ids: string[]) => void
  runTimeoutMs?: number
  prepareLimit?: number
  runLimit?: number
}

const RUN_TIMEOUT_MS = 60_000
// Each review may take up to a few seconds of reads and permission checks.
const PREPARE_LIMIT = 6
const RUN_LIMIT = 4

/** What became of one object's review. */
type Planned = { state: 'ready' | 'unavailable' | 'denied'; plan: ActionPlan }
type Review = { state: 'preparing' } | Planned | { state: 'failed'; text: string }

/** What became of one object's run. */
type Said = { state: 'done' | 'conflict' | 'unknown' | 'failed' | 'partial'; text: string }
type Run = { state: 'waiting' | 'running' | 'notRun' } | Said

const runClass: Record<Run['state'], string> = {
  waiting: 'text-fg-subtle',
  running: 'text-fg-subtle',
  done: 'text-success',
  conflict: 'text-warning',
  unknown: 'text-warning',
  partial: 'text-warning',
  failed: 'text-danger',
  notRun: 'text-fg-subtle',
}

const reviewOf = (plan: ActionPlan): Review => ({ state: plan.unavailable ? 'unavailable' : plan.rights.state === 'denied' ? 'denied' : 'ready', plan })

/**
 * The confirmation of one action on several objects: each object is
 * reviewed on its own (its own plan, Expect and rights), the plans are
 * shown together, and each ready one runs on its own. One run per review.
 */
export function BulkActionDialog({ client, req, onClose, onDone, runTimeoutMs = RUN_TIMEOUT_MS, prepareLimit = PREPARE_LIMIT, runLimit = RUN_LIMIT }: Props) {
  // The marked objects as the dialog opened: later marks do not change it.
  const [items] = useState(req.items)
  const { action } = req
  const param = action.param
  const counted = !!param && param.kind === 'count'
  const label = actionLabel(action)
  const scopeWords = useScopeWords(items[0]?.ref.provider ?? '')
  const [reviews, setReviews] = useState<Record<string, Review>>({})
  const [runs, setRuns] = useState<Record<string, Run>>({})
  const [phase, setPhase] = useState<'count' | 'review' | 'run' | 'done'>(counted ? 'count' : 'review')
  const [count, setCount] = useState('')
  const [countError, setCountError] = useState<string | null>(null)
  // The count the plans were made for (undefined: no count).
  const [reviewedCount, setReviewedCount] = useState<number | undefined>(undefined)
  const [open, setOpen] = useState<Record<string, boolean>>({})
  const [summary, setSummary] = useState<string | null>(null)
  const gen = useRef(0)
  const live = useRef(true)
  const stop = useRef(false)
  const lock = useRef(false)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const confirmRef = useRef<HTMLButtonElement>(null)
  const countRef = useRef<HTMLInputElement>(null)
  const box = useRef<HTMLFormElement>(null)
  const [mark] = useState(focusMark)

  useEffect(() => {
    live.current = true
    return () => {
      live.current = false
      restoreFocus(mark)
    }
  }, [mark])

  /** Reviews every object anew (a later review makes the earlier one's answers moot). */
  const prepareAll = (p: ActionParams) => {
    setReviews({}) // every object is being checked again
    setReviewedCount(p.count)
    setPhase('review')
    launch(p)
  }

  /** The reviews' reads; state changes only as they answer. */
  const launch = (p: ActionParams) => {
    const g = ++gen.current
    void pool(
      items,
      prepareLimit,
      async (it) => {
        let r: Review
        try {
          r = reviewOf(await client.prepareAction(it.ref, action.id, p))
        } catch (e) {
          r = { state: 'failed', text: t('action.prepareFailed', { class: classLabel(codeOf(e)), detail: errorDetail(e) }) }
        }
        if (live.current && g === gen.current) setReviews((rs) => ({ ...rs, [it.id]: r }))
      },
      () => !live.current || g !== gen.current,
    )
  }

  useEffect(() => {
    if (!counted) launch({})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const all = items.map((it) => ({ it, review: reviews[it.id] ?? ({ state: 'preparing' } as Review), run: runs[it.id] }))
  const checked = all.filter((x) => x.review.state !== 'preparing').length
  const preparing = phase === 'review' && checked < items.length
  const ready = all.flatMap((x) => (x.review.state === 'ready' ? [{ it: x.it, plan: (x.review as Planned).plan }] : []))
  const countReviewed = !counted || (reviewedCount !== undefined && count.trim() === String(reviewedCount))
  const destructive = !!action.destructive || ready.some((x) => x.plan.destructive)
  const canRun = phase === 'review' && !preparing && countReviewed && ready.length > 0

  // Warnings several objects share are said once: by key (or text), with how many and whose.
  const warnings = useMemo(() => {
    const byKey = new Map<string, { first: Message; names: string[] }>()
    for (const x of all) {
      if (x.review.state !== 'ready') continue
      for (const w of x.review.plan.warnings ?? []) {
        const k = w.key ?? w.text
        const g = byKey.get(k) ?? { first: w, names: [] }
        g.names.push(x.it.ref.name)
        byKey.set(k, g)
      }
    }
    return [...byKey.values()]
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reviews])

  useLayoutEffect(() => {
    if (phase === 'count') {
      countRef.current?.focus()
      return
    }
    if (phase !== 'review' || preparing) return
    if (counted && !countReviewed) return
    if (destructive || !canRun) cancelRef.current?.focus()
    else confirmRef.current?.focus()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phase, preparing])

  const review = () => {
    if (phase === 'run' || phase === 'done') return
    if (!counted) return prepareAll({})
    const s = count.trim()
    const n = Number(s)
    if (!param || !/^\d+$/.test(s) || n < param.min || n > param.max) {
      setCountError(t('action.countRange', { min: param?.min ?? 0, max: param?.max ?? 0 }))
      return
    }
    setCountError(null)
    prepareAll({ count: n })
  }

  /** One object's run, with the single dialog's timeout; a late answer still says what became of it. */
  const runOne = async (it: BulkItem, plan: ActionPlan): Promise<Said> => {
    let timer: ReturnType<typeof setTimeout> | undefined
    let timedOut = false
    const answer = client.runAction(plan)
    const told = (res: ActionResult): Said => {
      const partial = partialOf(res)
      return partial ? { state: 'partial', text: partsSummary(partial.parts) } : { state: 'done', text: messageText(res.message) }
    }
    answer.then(
      (res) => {
        if (!timedOut) return
        const r = told(res)
        if (live.current) {
          setRuns((rs) => ({ ...rs, [it.id]: { ...r, text: t('action.late', { text: r.text }) } }))
          if (r.state === 'done') onDone([it.id])
        } else showNotice(`${plan.where.targetTitle} · ${label} ${it.ref.name}: ${t('action.late', { text: r.text })}`, 10_000)
      },
      () => {},
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
      return told(res)
    } catch (e) {
      if (e instanceof RunTimeout) return { state: 'unknown', text: t('action.timeout', { sec: Math.round(runTimeoutMs / 1000) }) }
      const out = outcomeOf(e)
      return { state: out.type === 'conflict' ? 'conflict' : out.type === 'unknown' ? 'unknown' : 'failed', text: out.text }
    } finally {
      clearTimeout(timer)
    }
  }

  const run = async () => {
    if (lock.current || !canRun) return
    lock.current = true
    stop.current = false
    setPhase('run')
    const todo = ready
    const outcomes: Record<string, Run> = {}
    const set = (id: string, r: Run) => {
      outcomes[id] = r
      if (live.current) setRuns((rs) => ({ ...rs, [id]: r }))
    }
    todo.forEach((x) => set(x.it.id, { state: 'waiting' }))
    const left = await pool(
      todo,
      runLimit,
      async (x) => {
        set(x.it.id, { state: 'running' })
        set(x.it.id, await runOne(x.it, x.plan))
      },
      () => stop.current,
    )
    left.forEach((x) => set(x.it.id, { state: 'notRun' }))
    const n = (s: Run['state']) => Object.values(outcomes).filter((r) => r.state === s).length
    const parts = [t('action.partsDone', { done: n('done'), total: todo.length })]
    if (n('conflict')) parts.push(t('bulk.conflicts', { n: n('conflict') }))
    if (n('unknown')) parts.push(t('bulk.unknown', { n: n('unknown') }))
    if (n('partial')) parts.push(t('bulk.partial', { n: n('partial') }))
    if (n('failed')) parts.push(t('bulk.failed', { n: n('failed') }))
    if (n('notRun')) parts.push(t('bulk.notRun', { n: n('notRun') }))
    const text = parts.join('; ')
    const doneIds = todo.filter((x) => outcomes[x.it.id]?.state === 'done').map((x) => x.it.id)
    onDone(doneIds)
    const all = doneIds.length === todo.length
    showNotice(`${todo[0]?.plan.where.targetTitle ?? ''} · ${label}: ${text}`, all ? undefined : 10_000)
    if (!live.current) return
    setSummary(text)
    setPhase('done')
  }

  const close = () => {
    if (phase !== 'run') onClose()
  }

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      e.preventDefault()
      close()
    } else if (e.key === 'Enter' && e.repeat) {
      e.preventDefault()
      e.stopPropagation()
    }
  }

  // Where: one target (a table is one target's); the kinds and scopes of the objects.
  const kinds = new Map<string, number>()
  const scopes = new Set<string>()
  for (const it of items) {
    kinds.set(it.ref.kind, (kinds.get(it.ref.kind) ?? 0) + 1)
    if (it.ref.scope) scopes.add(it.ref.scope)
  }
  const scopeList = [...scopes]
  const skipped = all.filter((x) => x.review.state !== 'ready' && x.review.state !== 'preparing').length
  const targetTitle = ready[0]?.plan.where.targetTitle ?? items[0]?.ref.target

  const stateText = (x: (typeof all)[number]): { text: string; cls: string } => {
    const run = x.run
    if (run) return 'text' in run ? { text: run.text, cls: runClass[run.state] } : { text: t(`bulk.run.${run.state}`), cls: runClass[run.state] }
    const r = x.review
    switch (r.state) {
      case 'preparing':
        return { text: t('bulk.state.preparing'), cls: 'text-fg-subtle' }
      case 'failed':
        return { text: r.text, cls: 'text-danger' }
      case 'unavailable':
        return { text: t('bulk.state.unavailable', { reason: messageText(r.plan.unavailable!) }), cls: 'text-warning' }
      case 'denied':
        return { text: t('bulk.state.denied', { reason: r.plan.rights.reason ?? '' }), cls: 'text-danger' }
    }
    const w = r.plan.warnings?.length ?? 0
    const now = r.plan.current !== undefined ? ` · ${t('bulk.now', { count: r.plan.current })}` : ''
    return { text: (w ? t('bulk.state.warned', { n: w }) : t('bulk.state.ready')) + now, cls: w ? 'text-warning' : 'text-success' }
  }

  return (
    <div className="absolute inset-0 z-20 flex items-start justify-center bg-black/40 pt-16" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <form
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={t('bulk.title', { action: label, n: items.length })}
        aria-busy={preparing || phase === 'run'}
        tabIndex={-1}
        onKeyDown={onKey}
        onSubmit={(e) => {
          e.preventDefault()
          review() // Enter in the count reviews; it never runs
        }}
        className="flex max-h-[85%] w-[min(720px,94%)] flex-col gap-3 rounded-lg border border-line bg-panel p-4 shadow-2xl outline-none"
      >
        <h2 className="font-semibold">
          <span className={destructive ? 'text-danger' : ''}>{label}</span> · <span className="text-fg-muted">{t('bulk.count', { n: items.length })}</span>
        </h2>

        <dl aria-label="where" className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-0.5 rounded-md border border-line px-3 py-2 text-xs">
          <dt className="text-fg-muted">{t('action.context')}</dt>
          <dd className="min-w-0 break-all font-mono">{targetTitle}</dd>
          {scopeList.length > 0 && (
            <>
              <dt className="text-fg-muted">{scopeList.length === 1 ? scopeWords.singular : scopeWords.plural}</dt>
              <dd className="min-w-0 break-all font-mono">{scopeList.slice(0, 5).join(', ') + (scopeList.length > 5 ? ` +${scopeList.length - 5}` : '')}</dd>
            </>
          )}
          <dt className="text-fg-muted">{t('action.kind')}</dt>
          <dd className="min-w-0 break-all font-mono">{[...kinds].map(([k, n]) => `${req.kindTitleOf(k)} × ${n}`).join(', ')}</dd>
        </dl>

        {counted && (
          <label className="flex items-center gap-2 text-xs text-fg-muted">
            {t('action.count')}
            <input
              ref={countRef}
              inputMode="numeric"
              value={count}
              disabled={phase === 'run' || phase === 'done'}
              onChange={(e) => {
                setCount(e.target.value)
                setCountError(null)
              }}
              aria-invalid={!!countError}
              className="w-24 rounded-md border border-line bg-app px-2 py-1 font-mono text-sm text-fg outline-none focus:border-accent"
            />
            {countError && <span className="text-danger">{countError}</span>}
          </label>
        )}

        {phase !== 'count' && (
          <div className="-mx-1 flex min-h-0 flex-col gap-3 overflow-y-auto px-1">
            {preparing ? (
              <p className="text-fg-subtle">{t('bulk.checked', { done: checked, total: items.length })}</p>
            ) : (
              phase === 'review' && <p>{t('bulk.summary', { ready: ready.length, skipped })}</p>
            )}
            {summary && (
              <p role="status" className={['rounded-md px-3 py-2', all.every((x) => !x.run || x.run.state === 'done') ? 'bg-success/10 text-success' : 'bg-warning/10 text-warning'].join(' ')}>
                {summary}
              </p>
            )}
            {!preparing && warnings.length > 0 && phase === 'review' && (
              <section aria-label={t('action.warnings')} className="text-warning">
                <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider">{t('action.warnings')}</h3>
                <ul className="list-disc space-y-0.5 pl-5">
                  {warnings.map((w, i) => (
                    <li key={i}>
                      {w.names.length === 1
                        ? `${messageText(w.first)} (${w.names[0]})`
                        : t('bulk.warnedMany', { text: messageText(w.first), n: w.names.length, names: w.names.slice(0, 3).join(', ') + (w.names.length > 3 ? '…' : '') })}
                    </li>
                  ))}
                </ul>
              </section>
            )}
            <Portions
              items={all}
              render={(shown) => (
                <ul aria-label={t('bulk.objects')} className="divide-y divide-line/60 rounded-md border border-line text-sm">
                  {shown.map((x) => {
                    const st = stateText(x)
                    const plan = 'plan' in x.review ? x.review.plan : null
                    const expanded = !!open[x.it.id]
                    return (
                      <li key={x.it.id} className="px-3 py-1">
                        <div className="flex items-baseline gap-3">
                          <button
                            type="button"
                            aria-expanded={expanded}
                            disabled={!plan}
                            onClick={() => setOpen((o) => ({ ...o, [x.it.id]: !expanded }))}
                            className="min-w-0 shrink-0 truncate text-left font-mono outline-none hover:underline focus:underline disabled:no-underline"
                          >
                            <span aria-hidden className="mr-1 inline-block w-2 text-[10px] text-fg-subtle">{plan ? (expanded ? '▾' : '▸') : ''}</span>
                            {scopeList.length > 1 && x.it.ref.scope ? <span className="text-fg-subtle">{x.it.ref.scope}/</span> : null}
                            {x.it.ref.name}
                          </button>
                          <span className={['min-w-0 flex-1 text-xs', st.cls].join(' ')}>{st.text}</span>
                        </div>
                        {expanded && plan && (
                          <div className="flex flex-col gap-2 py-2 pl-4 text-xs">
                            <PlanDetails plan={plan} />
                          </div>
                        )}
                      </li>
                    )
                  })}
                </ul>
              )}
            />
          </div>
        )}

        <div className="flex justify-end gap-2">
          {phase === 'run' && (
            <button type="button" className={`${btn} border border-line hover:bg-hover`} onClick={() => (stop.current = true)}>
              {t('bulk.stop')}
            </button>
          )}
          <button ref={cancelRef} type="button" disabled={phase === 'run'} className={`${btn} text-fg-muted hover:bg-hover`} onClick={close}>
            {phase === 'done' ? t('action.close') : t('action.cancel')}
          </button>
          {phase === 'review' && !preparing && countReviewed && (
            <button type="button" className={`${btn} border border-line hover:bg-hover`} onClick={review}>
              {t('action.reviewAgain')}
            </button>
          )}
          {counted && !countReviewed && (phase === 'count' || phase === 'review') && (
            <button type="submit" className={`${btn} bg-accent text-accent-fg`}>
              {t('action.review')}
            </button>
          )}
          {phase === 'review' && countReviewed && !preparing && (
            <button ref={confirmRef} type="button" disabled={!canRun} onClick={() => void run()} className={[btn, destructive ? 'bg-danger text-white' : 'bg-accent text-accent-fg'].join(' ')}>
              {t('bulk.run', { action: label, n: ready.length })}
            </button>
          )}
        </div>
      </form>
    </div>
  )
}
