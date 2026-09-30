import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { ApiError, type Client } from '../api/client'
import type { Ref, Value, ValueEditRequest, ValueEncoding, ValueList, ValueOp, ValuePlan, ValueResult } from '../api/types'
import { holdEdits, mayLeave } from '../edit/guard'
import { classLabel, messageText, t } from '../i18n'
import { refTitle } from '../refs'
import { useScopeWords } from '../scopeNames'
import { asText, fromBase64, toBase64, typedBytes, utf8, validKey } from './bytes'

/** What the dialog does: set a key's value (a new key without key) or delete a key. */
export interface ValueDialogMode {
  op: ValueOp
  key?: string
}

interface Props {
  client: Client
  subject: Ref
  /** The keys as listed when the dialog opened: its base (a later list does not move the draft). */
  list: ValueList
  mode: ValueDialogMode
  /** The key's value as shown in the list (of the listed version): the draft starts from it. */
  shown?: Value
  revision?: string
  kindTitle: string
  onClose: () => void
  onDone: (res: ValueResult, op: ValueOp, key: string) => void
}

const btn = 'rounded-md px-3 py-1 outline-none focus:ring-2 focus:ring-accent focus:ring-offset-2 focus:ring-offset-panel disabled:opacity-50'
const smallBtn = 'rounded-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg disabled:opacity-50'
const codeOf = (e: unknown) => (e instanceof ApiError ? e.code : 'internal')
const detailOf = (e: unknown) => (e instanceof ApiError ? e.detail || e.code : e instanceof Error ? e.message : String(e))

/** The draft's value as the shown one gives it. */
const draftOf = (v: Value | undefined) => v?.value ?? ''

/**
 * A key's change: the value (text or base64, bytes kept exactly), then a
 * review without any value (sizes, what else changes, who reads it,
 * rights), then one write of exactly what was reviewed. An existing key's
 * value starts not loaded: nothing is written until it is loaded or a new
 * one typed (an untouched field is not an empty value).
 */
export function ValueDialog({ client, subject, list: listed, mode, shown, revision, kindTitle, onClose, onDone }: Props) {
  const isNew = mode.op === 'set' && !mode.key
  // The base the draft lies on; "Load current" moves it to the version it loaded.
  const [list, setList] = useState(listed)
  const [keyName, setKeyName] = useState(mode.key ?? '')
  const known = list.keys.find((k) => k.key === (mode.key ?? ''))
  // A value that is not text is edited as base64 only: a text field would change its bytes.
  const textOnlyBase64 = !!known && !known.text
  const [encoding, setEncoding] = useState<ValueEncoding>(known && !known.text ? 'base64' : 'text')
  const [value, setValue] = useState(draftOf(shown))
  const [loaded, setLoaded] = useState(!!shown)
  const [touched, setTouched] = useState(false)
  const [note, setNote] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  // The review asked for: a snapshot of the draft then.
  const [review, setReview] = useState<ValueEditRequest | null>(mode.op === 'delete' ? { ref: subject, base: list.base, key: mode.key ?? '', op: 'delete' } : null)
  const box = useRef<HTMLDivElement>(null)
  const field = useRef<HTMLTextAreaElement>(null)
  const keyField = useRef<HTMLInputElement>(null)
  const live = useRef(true)
  // The draft's epoch: typing, switching the encoding or starting a review
  // makes a pending "Load current" stale (its answer never replaces them).
  const epoch = useRef(0)

  const dirty = mode.op === 'set' && (touched || (isNew && keyName !== ''))
  const dirtyRef = useRef(dirty)
  useEffect(() => {
    dirtyRef.current = dirty
  })
  useLayoutEffect(() => {
    live.current = true
    // The draft is held like the YAML editor's text: leaving asks first.
    const release = holdEdits({ dirty: () => dirtyRef.current, discard: onClose, focus: () => field.current?.focus() })
    return () => {
      live.current = false
      release()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useLayoutEffect(() => {
    if (review) return
    ;(isNew ? keyField.current : field.current)?.focus()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [review === null])

  const typed = useMemo(() => typedBytes(value, encoding), [value, encoding])
  const keyProblem = isNew && keyName !== '' ? (!validKey(keyName) ? t('values.keyInvalid') : list.keys.some((k) => k.key === keyName) ? t('values.keyExists', { key: keyName }) : null) : null
  const canReview = mode.op === 'set' && validKey(keyName) && !keyProblem && (loaded || touched) && 'bytes' in typed

  const cancel = () => mayLeave(onClose)
  const startReview = () => {
    if (!canReview) return
    epoch.current++
    setReview({ ref: subject, base: list.base, key: keyName, op: 'set', value, encoding })
  }

  const switchTo = (enc: ValueEncoding) => {
    if (enc === encoding) return
    epoch.current++
    setNote(null)
    if (enc === 'base64') {
      setValue(toBase64(utf8(value)))
      setEncoding(enc)
      return
    }
    const b = fromBase64(value)
    if (!b) return setNote(t('values.badBase64'))
    const text = asText(b)
    if (text === null) return setNote(t('values.notText'))
    setValue(text)
    setEncoding(enc)
  }

  // The current value, with the keys of the same version: the draft's base moves to it.
  const loadCurrent = () => {
    if (!mode.key || loading) return
    setLoading(true)
    setNote(null)
    const key = mode.key
    const at = ++epoch.current
    client
      .getValues(subject)
      .then(async (fresh) => ({ fresh, v: await client.revealValue({ ...subject, uid: fresh.ref.uid ?? subject.uid }, key) }))
      .then(
        ({ fresh, v }) => {
          if (!live.current) return
          setLoading(false)
          if (at !== epoch.current) return setNote(t('values.loadDropped'))
          if (v.uid !== subject.uid || v.version !== fresh.version) return setNote(t('values.changed'))
          const k = fresh.keys.find((x) => x.key === key)
          setList(fresh)
          setEncoding(k?.text ? 'text' : 'base64')
          setValue(v.value)
          setLoaded(true)
          setTouched(false)
          requestAnimationFrame(() => field.current?.focus())
        },
        (e) => {
          if (!live.current) return
          setLoading(false)
          setNote(t('values.readFailed', { class: classLabel(codeOf(e)), detail: detailOf(e) }))
        },
      )
  }

  const title = t(mode.op === 'delete' ? 'values.dialogDelete' : isNew ? 'values.dialogAdd' : 'values.dialogEdit')
  const moved = !!revision && revision !== list.version

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      e.preventDefault()
      cancel()
    } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.repeat) {
      e.preventDefault()
      e.stopPropagation()
      startReview()
    } else if (e.key === 'Tab') trapTab(e, box.current)
  }

  if (review)
    return (
      <ValueReview
        client={client}
        req={review}
        kindTitle={kindTitle}
        title={title}
        onBack={() => (mode.op === 'delete' ? onClose() : setReview(null))}
        onDone={(res) => onDone(res, review.op, review.key)}
      />
    )

  return (
    <Overlay onOutside={cancel}>
      <div
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={`${title} ${refTitle(subject)}`}
        tabIndex={-1}
        onKeyDown={onKey}
        className="flex max-h-[88%] w-[min(760px,94%)] flex-col gap-3 overflow-y-auto rounded-lg border border-line bg-panel p-4 shadow-2xl outline-none"
      >
        <h2 className="font-semibold">
          {title} · <span className="font-mono text-fg-muted">{refTitle(subject)}</span>
        </h2>
        {moved && (
          <p role="status" className="rounded-md bg-warning/10 px-3 py-1.5 text-xs text-warning">
            {t('values.changedOnServer')}
          </p>
        )}
        <label className="flex flex-col gap-1 text-xs text-fg-muted">
          {t('values.key')}
          {isNew ? (
            <input
              ref={keyField}
              value={keyName}
              spellCheck={false}
              autoComplete="off"
              onChange={(e) => setKeyName(e.target.value)}
              className="rounded-md border border-line bg-app px-2 py-1 font-mono text-[13px] text-fg outline-none focus:border-accent"
            />
          ) : (
            <span className="font-mono text-[13px] text-fg">{keyName}</span>
          )}
        </label>
        {keyProblem && <p className="text-xs text-danger">{keyProblem}</p>}
        <div className="flex items-center gap-2 text-xs">
          <span className="text-fg-muted">{t('values.value')}</span>
          <div role="radiogroup" className="inline-flex">
            {(['text', 'base64'] as ValueEncoding[]).map((enc, i) => (
              <button
                key={enc}
                type="button"
                role="radio"
                aria-checked={encoding === enc}
                disabled={enc === 'text' && textOnlyBase64}
                onClick={() => switchTo(enc)}
                className={['border border-line px-2 py-0.5', i === 0 ? 'rounded-l-md' : '-ml-px rounded-r-md', encoding === enc ? 'bg-accent text-accent-fg' : 'text-fg-muted hover:bg-hover', 'disabled:opacity-50'].join(' ')}
              >
                {t(enc === 'text' ? 'values.asText' : 'values.asBase64')}
              </button>
            ))}
          </div>
          <span className="flex-1" />
          {!isNew && (
            <button type="button" className={smallBtn} disabled={loading} onClick={loadCurrent}>
              {t('values.load')}
            </button>
          )}
        </div>
        {textOnlyBase64 && <p className="text-xs text-fg-subtle">{t('values.binaryOnly')}</p>}
        {!isNew && !loaded && !touched && <p className="text-xs text-warning">{t('values.notLoaded')}</p>}
        <textarea
          ref={field}
          aria-label={t('values.value')}
          value={value}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => {
            epoch.current++
            setValue(e.target.value)
            setTouched(true)
          }}
          className="min-h-40 resize-y rounded-md border border-line bg-app px-2 py-1 font-mono text-xs break-all text-fg outline-none focus:border-accent"
        />
        <p className="text-xs text-fg-subtle">
          {'bytes' in typed ? t('values.bytes', { n: typed.bytes.length }) : <span className="text-danger">{t(typed.error === 'tooLong' ? 'values.tooLong' : 'values.badBase64')}</span>}
        </p>
        {note && (
          <p role="alert" className="text-xs text-danger">
            {note}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className={`${btn} text-fg-muted hover:bg-hover`} onClick={cancel}>
            {t('values.cancel')}
          </button>
          <button type="button" className={`${btn} bg-accent text-accent-fg`} disabled={!canReview} onClick={startReview} title="Ctrl+Enter">
            {t('values.review')} <span className="opacity-70">Ctrl+Enter</span>
          </button>
        </div>
      </div>
    </Overlay>
  )
}

function Overlay({ onOutside, children }: { onOutside: () => void; children: React.ReactNode }) {
  return (
    <div className="fixed inset-0 z-30 flex items-start justify-center bg-black/40 pt-10" onMouseDown={(e) => e.target === e.currentTarget && onOutside()}>
      {children}
    </div>
  )
}

/** Tab stays in the dialog. */
function trapTab(e: React.KeyboardEvent, box: HTMLElement | null) {
  const list = [...(box?.querySelectorAll<HTMLElement>('button:not(:disabled), input, textarea') ?? [])]
  if (!list.length) return
  const i = list.indexOf(document.activeElement as HTMLElement)
  const next = e.shiftKey ? (i <= 0 ? list.length - 1 : i - 1) : i < 0 || i === list.length - 1 ? 0 : i + 1
  e.preventDefault()
  list[next].focus()
}

type Outcome = { type: 'prepareFailed' | 'conflict' | 'unknown' | 'failed'; text: string }

function outcomeOf(e: unknown): Outcome {
  const code = codeOf(e)
  // No coded answer (the connection failed): the write may have been applied.
  if (code === 'unknown' || !(e instanceof ApiError) || e.transport) return { type: 'unknown', text: `${t('edit.unknown')} ${detailOf(e)}` }
  if (code === 'conflict') return { type: 'conflict', text: t('edit.conflict', { detail: detailOf(e) }) }
  return { type: 'failed', text: t('edit.failed', { class: classLabel(code), detail: detailOf(e) }) }
}

/**
 * The review of a key's change: where, the key and its sizes, what the
 * server's check keeps otherwise, who reads it, warnings and rights; then
 * one write of exactly what was reviewed.
 */
function ValueReview({ client, req, kindTitle, title, onBack, onDone }: { client: Client; req: ValueEditRequest; kindTitle: string; title: string; onBack: () => void; onDone: (res: ValueResult) => void }) {
  const scopeWords = useScopeWords(req.ref.provider)
  const [plan, setPlan] = useState<ValuePlan | null>(null)
  const [busy, setBusy] = useState<'prepare' | 'run' | null>('prepare')
  const [outcome, setOutcome] = useState<Outcome | null>(null)
  // A write was sent for this plan: never another one.
  const [sent, setSent] = useState(false)
  // Written, but not quite as reviewed: said before closing.
  const [written, setWritten] = useState<ValueResult | null>(null)
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

  const fetchPlan = () => {
    const g = ++gen.current
    lock.current = false
    client.prepareValueEdit(req).then(
      (pl) => {
        if (!live.current || g !== gen.current) return
        setPlan(pl)
        setBusy(null)
      },
      (e) => {
        if (!live.current || g !== gen.current) return
        setBusy(null)
        setOutcome({ type: 'prepareFailed', text: t('edit.prepareFailed', { class: classLabel(codeOf(e)), detail: detailOf(e) }) })
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

  // Focus: Apply on a plan that can be written and is not dangerous; else Back.
  useLayoutEffect(() => {
    if (busy) {
      if (!box.current?.contains(document.activeElement)) box.current?.focus()
      return
    }
    if (canRun && !destructive) applyRef.current?.focus()
    else backRef.current?.focus()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan, busy, written])

  const run = async () => {
    if (lock.current || !canRun || !plan?.token) return
    lock.current = true
    setSent(true)
    setBusy('run')
    setOutcome(null)
    try {
      const res = await client.runValueEdit({ ...req, token: plan.token })
      if (!live.current) return
      setBusy(null)
      if (res.differs?.length || res.serverChanges?.length) setWritten(res)
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
    } else if (e.key === 'Tab') trapTab(e, box.current)
  }

  const where = plan?.where
  const name = refTitle(where?.ref ?? req.ref)
  const scope = where?.ref.scope ?? req.ref.scope
  // From what the review leaves (the server's, when checked), not from the
  // operation asked: a server may keep, lengthen or drop the key.
  const sizes = !plan
    ? ''
    : plan.after < 0
      ? plan.before < 0
        ? ''
        : t('values.opDelete', { before: plan.before })
      : plan.before < 0
        ? t('values.opAdd', { after: plan.after })
        : t('values.opSet', { before: plan.before, after: plan.after })
  const removes = !!plan && plan.before >= 0 && plan.after < 0

  return (
    <Overlay onOutside={back}>
      <div
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={`${title} ${name}`}
        aria-busy={!!busy}
        tabIndex={-1}
        onKeyDown={onKey}
        className="flex max-h-[88%] w-[min(760px,94%)] flex-col gap-3 overflow-y-auto rounded-lg border border-line bg-panel p-4 shadow-2xl outline-none"
      >
        <h2 className="font-semibold">
          <span className={destructive ? 'text-danger' : ''}>{title}</span> · <span className="font-mono text-fg-muted">{name}</span>
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
          <dd className="min-w-0 break-all font-mono">{kindTitle}</dd>
          <dt className="text-fg-muted">{t('action.name')}</dt>
          <dd className="min-w-0 break-all font-mono">{name}</dd>
          <dt className="text-fg-muted">{t('values.key')}</dt>
          <dd className="min-w-0 break-all font-mono">{req.key}</dd>
        </dl>

        {busy === 'prepare' && <p className="text-fg-subtle">{t('edit.preparing')}</p>}

        {plan && !written && busy !== 'prepare' && (
          <>
            {!plan.changed ? (
              <p role="status" className="rounded-md border border-line px-3 py-2 text-fg-muted">
                {t('edit.noChanges')}
              </p>
            ) : (
              <>
                {sizes && (
                  <p aria-label={t('values.value')} className={['font-mono', removes ? 'text-danger' : ''].join(' ')}>
                    {sizes}
                  </p>
                )}
                <p aria-label={t('edit.mode')} className={plan.checked ? 'text-fg-muted' : 'text-warning'}>
                  {plan.checked ? t('edit.checked') : t('edit.local')}
                </p>
              </>
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
            {plan.consumers && (
              <section aria-label={t('values.consumers')} className="text-xs">
                <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t('values.consumers')}</h3>
                {!!plan.consumers.items?.length && (
                  <ul className="list-disc pl-5 font-mono">
                    {plan.consumers.items.map((c) => (
                      <li key={c}>{c}</li>
                    ))}
                  </ul>
                )}
                {plan.consumers.known ? (
                  !plan.consumers.items?.length && <p className="text-fg-muted">{t('values.consumersNone')}</p>
                ) : (
                  <p className="text-warning">{t('values.consumersUnknown', { why: plan.consumers.why ?? '' })}</p>
                )}
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
          </>
        )}

        {written && (
          <p role="status" className="rounded-md bg-warning/10 px-3 py-2 text-warning">
            {written.differs?.length ? t('values.differs', { keys: written.differs.join(', ') }) : t('values.serverChanged', { keys: (written.serverChanges ?? []).join(', ') })}
          </p>
        )}

        {busy === 'run' && <p className="text-fg-subtle">{t('edit.running')}</p>}
        {outcome && (
          <p role="alert" className={['rounded-md px-3 py-2', outcome.type === 'unknown' || outcome.type === 'conflict' ? 'bg-warning/10 text-warning' : 'bg-danger/10 text-danger'].join(' ')}>
            {outcome.text}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <button ref={backRef} type="button" disabled={busy === 'run'} className={`${btn} text-fg-muted hover:bg-hover`} onClick={back}>
            {written ? t('edit.close') : req.op === 'delete' ? t('values.cancel') : t('values.back')}
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
    </Overlay>
  )
}
