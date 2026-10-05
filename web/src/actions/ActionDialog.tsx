import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { ActionChoice, ActionDescriptor, ActionParams, ActionPlan, ActionResult, Message, Ref, TerminalOpen } from '../api/types'
import { actionLabel, classLabel, messageText, t } from '../i18n'
import { refTitle } from '../refs'
import { useScopeWords } from '../scopeNames'
import { showNotice } from '../store'
import { focusMark, restoreFocus } from '../shortcuts'
import { Portions } from './ActionLists'
import { PlanDetails } from './PlanDetails'
import { btn, codeOf, outcomeOf, partClass, partialOf, partsSummary, RunTimeout, type Outcome } from './outcome'
import { errorDetail } from '../errors'
import { formatAge } from '../format'
import { useNow } from '../views/useView'

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
  /** A done run asks for a terminal (a debug container's). */
  onTerminal?: (open: TerminalOpen) => void
  /** How long a run may take before its outcome is called unknown. */
  runTimeoutMs?: number
}

/** The target_state key of the text an action last ran with on a target. */
const textKey = (actionId: string) => `actionText.${actionId}`

const byteLength = (s: string) => new TextEncoder().encode(s).length

const RUN_TIMEOUT_MS = 60_000


/**
 * The confirmation of an action: where (context, server, namespace, kind,
 * name), what happens, the permission check; a count or a choice is made
 * first and reviewed before it can run (a choice is reviewed as it is made). One run per confirmation; while it runs the
 * dialog stays.
 */
export function ActionDialog({ client, req, onClose, onTerminal, runTimeoutMs = RUN_TIMEOUT_MS }: Props) {
  const { ref, action, kindTitle } = req
  // The object's own provider names its scopes (not the target selected now).
  const scopeWords = useScopeWords(ref.provider)
  const param = action.param
  const counted = !!param && param.kind !== 'choice'
  const choosing = param?.kind === 'choice'
  // A text value next to the param (a debug container's image).
  const textDesc = action.text
  const [plan, setPlan] = useState<ActionPlan | null>(null)
  // The text typed; null until the first plan says the provider's default.
  const [text, setText] = useState<string | null>(null)
  const textTouched = useRef(false)
  const chosenTouched = useRef(false)
  const countTouched = useRef(false)
  const [textError, setTextError] = useState<string | null>(null)
  const [count, setCount] = useState('')
  // The value chosen (a choice parameter).
  const [chosen, setChosen] = useState<string | null>(null)
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
  const textRef = useRef<HTMLInputElement>(null)
  const choicesRef = useRef<HTMLDivElement>(null)
  const outcomeRef = useRef<HTMLParagraphElement>(null)
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
        if (counted && !countTouched.current && p.count === undefined && pl.current !== undefined) setCount(String(pl.current))
        // The provider's defaults (the image, the target) are this plan's:
        // taken as if chosen, so the plan is reviewed at once.
        if (textDesc && !textTouched.current && pl.params.text !== undefined) setText(pl.params.text)
        if (choosing && !chosenTouched.current && pl.params.choice !== undefined) setChosen(pl.params.choice)
      },
      (e) => {
        if (!live.current || g !== gen.current) return
        setPlan(null)
        setBusy(null)
        setOutcome({ type: 'prepareFailed', text: t('action.prepareFailed', { class: classLabel(codeOf(e)), detail: errorDetail(e) }) })
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

  useEffect(() => {
    if (!textDesc) return fetchPlan({})
    // The text last run on this target is the first reviewed.
    client.getTargetState(ref.provider, ref.target).then(
      (st) => {
        let last: unknown
        try {
          last = JSON.parse(st[textKey(action.id)] ?? 'null')
        } catch {
          last = null
        }
        fetchPlan(typeof last === 'string' && last.trim() && byteLength(last) <= textDesc.max ? { text: last } : {})
      },
      () => fetchPlan({}),
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // The values under review: the text typed and the choice made.
  const values = (extra: ActionParams = {}): ActionParams => ({
    ...(chosen !== null ? { choice: chosen } : {}),
    ...(textDesc && text !== null ? { text: text.trim() } : {}),
    ...extra,
  })

  const planned = plan?.params.count
  // A count or a text other than the reviewed one must be reviewed first; a
  // choice runs only with its own plan.
  const textReviewed = !textDesc || (text !== null && plan?.params.text === text.trim())
  const reviewed =
    textReviewed && (!param || (choosing ? chosen !== null && plan?.params.choice === chosen : planned !== undefined && count.trim() === String(planned)))
  const canRun = !!plan && reviewed && !busy && !sent && !plan.unavailable && plan.rights.state !== 'denied'
  const destructive = !!plan?.destructive

  // Focus: the count while choosing it; on a reviewed plan the confirmation,
  // or Cancel when it is destructive.
  useLayoutEffect(() => {
    if (busy) return
    // Choosing with the arrows: each choice is reviewed as it is made, the focus stays.
    if (choosing && choicesRef.current?.contains(document.activeElement)) return
    if (choosing && chosen === null) {
      // The first choice that can be made (the current one cannot).
      const first = choicesRef.current?.querySelector<HTMLInputElement>('input:not(:disabled)')
      if (first) first.focus()
      else cancelRef.current?.focus() // nothing to choose: Esc and Enter still reach the dialog
    } else if (textDesc && !textReviewed) {
      textRef.current?.focus()
    } else if (choosing && !reviewed) {
      return // a choice's review is on its way
    } else if (counted && !reviewed) {
      if (!countTouched.current) {
        countRef.current?.focus()
        countRef.current?.select()
      }
    } else if (plan && canRun && !destructive) confirmRef.current?.focus()
    else if (plan && destructive) cancelRef.current?.focus() // also from the count: a destructive plan starts at Cancel
    else if (!box.current?.contains(document.activeElement) || document.activeElement === box.current) cancelRef.current?.focus()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan, busy])

  // An outcome comes below the plan: bring it into view (only the middle scrolls).
  useEffect(() => {
    if (outcome) outcomeRef.current?.scrollIntoView({ block: 'start' })
  }, [outcome])

  // Why the text cannot be reviewed (null: it can).
  const textProblem = () => {
    if (!textDesc) return null
    const s = (text ?? '').trim()
    return !s ? t('action.textEmpty') : byteLength(s) > textDesc.max ? t('action.textTooLong', { max: textDesc.max }) : null
  }

  const choose = (v: string) => {
    if (busy === 'run' || sent) return
    chosenTouched.current = true
    setChosen(v)
    const bad = textProblem()
    setTextError(bad)
    if (!bad) prepare(values({ choice: v }))
  }

  const review = () => {
    if (busy === 'run') return
    if (textDesc) {
      const bad = textProblem()
      setTextError(bad)
      if (bad) return
      if (!counted) return prepare(values())
    }
    if (!param || !counted) return
    const s = count.trim()
    const n = Number(s)
    if (!/^\d+$/.test(s) || n < param.min || n > param.max) {
      setCountError(t('action.countRange', { min: param.min, max: param.max }))
      return
    }
    setCountError(null)
    prepare(values({ count: n }))
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
    const told = (res: ActionResult) => (res.parts?.length ? `${actionLabel(action)} ${refTitle(plan.where.ref)}: ${partsSummary(res.parts)}` : messageText(res.message))
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
        if (textDesc && plan.params.text !== undefined) {
          void client.setTargetState(ref.provider, ref.target, textKey(action.id), JSON.stringify(plan.params.text)).catch(() => {})
        }
        onClose()
        if (res.terminal) onTerminal?.(res.terminal)
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
      // A group of choices is one stop (its checked one, else its first): arrows move within it.
      const radios = [...(box.current?.querySelectorAll<HTMLInputElement>('input[type=radio]:not(:disabled)') ?? [])]
      const stop = radios.find((r) => r.checked) ?? radios[0]
      const list = [...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)') ?? [])].filter(
        (el) => !(el instanceof HTMLInputElement && el.type === 'radio') || el === stop,
      )
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
        className="flex max-h-[80%] w-[min(600px,92%)] flex-col gap-3 rounded-lg border border-line bg-panel p-4 shadow-2xl outline-none"
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

        {/* Only the middle scrolls: a long plan or result never hides what and where, nor the buttons. */}
        <div className="-mx-1 flex min-h-0 flex-col gap-3 overflow-y-auto px-1">

          {textDesc && (
            <label className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
              {messageText(textDesc.title)}
              <input
                ref={textRef}
                value={text ?? ''}
                placeholder={textDesc.default}
                disabled={busy === 'run' || sent}
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => {
                  textTouched.current = true
                  setText(e.target.value)
                  setTextError(null)
                }}
                aria-invalid={!!textError}
                className="min-w-0 flex-1 rounded-md border border-line bg-app px-2 py-1 font-mono text-sm text-fg outline-none focus:border-accent"
              />
              {textError && <span className="text-danger">{textError}</span>}
            </label>
          )}

          {choosing && !!plan?.choices?.length && (
            <Choices title={param?.title} choices={plan.choices} chosen={chosen} disabled={busy === 'run' || sent || (chosen === null && !!plan.unavailable)} onChoose={choose} boxRef={choicesRef} />
          )}

          {counted && (
            <label className="flex items-center gap-2 text-xs text-fg-muted">
              {t('action.count')}
              <input
                ref={countRef}
                inputMode="numeric"
                value={count}
                disabled={busy === 'run' || sent}
                onChange={(e) => {
                  countTouched.current = true
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

          {plan && busy !== 'prepare' && (reviewed || (choosing && chosen === null)) && (
            <>
              <PlanDetails plan={plan} />
            </>
          )}

          {busy === 'run' && <p className="text-fg-subtle">{t('action.running')}</p>}
          {outcome?.type === 'lateDone' ? (
            <p ref={outcomeRef} role="status" className="rounded-md bg-success/10 px-3 py-2 text-success">
              {outcome.text}
            </p>
          ) : outcome && (
            <p ref={outcomeRef} role="alert" className={['rounded-md px-3 py-2', outcome.type === 'unknown' || outcome.type === 'conflict' || (outcome.type === 'partial' && outcome.unknown) ? 'bg-warning/10 text-warning' : 'bg-danger/10 text-danger'].join(' ')}>
              {outcome.text}
            </p>
          )}
          {outcome?.type === 'partial' && outcome.parts.length > 0 && (
            <div>
              <Portions
                items={outcome.parts}
                render={(shown) => (
                  <ul aria-label={t('action.parts')} className="space-y-0.5 rounded-md border border-line px-3 py-2 text-xs">
                    {shown.map((p) => {
                      const why = p.why && messageText(p.why)
                      return (
                        <li key={p.id} className="flex gap-2">
                          <span className="min-w-0 break-all font-mono">{p.title}</span>
                          <span className={partClass[p.outcome]}>
                            {t(`action.part.${p.outcome}`)}
                            {why && p.outcome !== 'done' ? ` · ${why}` : ''}
                          </span>
                        </li>
                      )
                    })}
                  </ul>
                )}
              />
            </div>
          )}
        </div>

        <div className="flex justify-end gap-2">
          <button ref={cancelRef} type="button" disabled={busy === 'run'} className={`${btn} text-fg-muted hover:bg-hover`} onClick={close}>
            {sent || outcome?.type === 'prepareFailed' ? t('action.close') : t('action.cancel')}
          </button>
          {(outcome?.type === 'conflict' || outcome?.type === 'prepareFailed' || (choosing && chosen !== null && reviewed && !!plan?.unavailable && !sent)) && (
            <button type="button" className={`${btn} border border-line hover:bg-hover`} onClick={() => ((counted && count.trim()) || (textDesc && text !== null) ? review() : prepare(values()))}>
              {t('action.reviewAgain')}
            </button>
          )}
          {(counted || (textDesc && !textReviewed)) && !reviewed && !sent && (
            // Enabled while a review is read: a disabled default button would swallow Enter in the count.
            <button type="submit" disabled={busy === 'run'} className={`${btn} bg-accent text-accent-fg`}>
              {t('action.review')}
            </button>
          )}
          {(reviewed || choosing) && !sent && !unknownOrDone && outcome?.type !== 'prepareFailed' && (
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

const dateTime = () => new Intl.DateTimeFormat(document.documentElement.lang || undefined, { dateStyle: 'short', timeStyle: 'medium' })

/** The values a choice parameter may take, newest first as the provider gives them; one that cannot be chosen says why. */
function Choices({ title, choices, chosen, disabled, onChoose, boxRef }: { title?: Message; choices: ActionChoice[]; chosen: string | null; disabled: boolean; onChoose: (v: string) => void; boxRef: React.RefObject<HTMLDivElement | null> }) {
  const now = useNow(10_000)
  const heading = title ? messageText(title) : t('action.choices')
  return (
    <div ref={boxRef} role="radiogroup" aria-label={heading} className="flex flex-col gap-1">
      <h3 className="mb-0.5 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{heading}</h3>
      {choices.map((c) => {
        const why = c.unavailable && messageText(c.unavailable)
        return (
          <label
            key={c.value}
            title={why || undefined}
            className={['flex items-start gap-2 rounded-md border px-2 py-1', chosen === c.value ? 'border-accent bg-accent/10' : 'border-line', c.unavailable ? 'opacity-60' : 'cursor-pointer hover:bg-hover'].join(' ')}
          >
            <input
              type="radio"
              name="action-choice"
              value={c.value}
              checked={chosen === c.value}
              disabled={disabled || !!c.unavailable}
              onChange={() => onChoose(c.value)}
              // A visible focus: WebKitGTK shows no :focus-visible for focus set by script.
              className="mt-1 accent-accent outline-none focus:ring-2 focus:ring-accent focus:ring-offset-2 focus:ring-offset-panel"
            />
            <span className="flex min-w-0 flex-col">
              <span>
                <span className="font-medium">{messageText(c.title)}</span>
                {c.current && <span className="ml-2 rounded bg-fg/10 px-1 text-xs text-fg-muted">{t('action.choiceCurrent')}</span>}
                {!!c.at && (
                  <span className="ml-2 text-xs text-fg-subtle" title={dateTime().format(c.at)}>
                    {t('action.choiceAge', { age: formatAge(now - c.at) })}
                  </span>
                )}
                {c.value !== messageText(c.title) && <span className="ml-2 font-mono text-xs text-fg-subtle">{c.value}</span>}
              </span>
              {!!c.details?.length && <span className="break-all text-xs text-fg-muted">{c.details.map((d) => messageText(d)).join(' · ')}</span>}
            </span>
          </label>
        )
      })}
    </div>
  )
}
